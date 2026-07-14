// Package ldap implements ports.Directory against an LDAP directory
// (lldap, OpenLDAP, AD). Read-only: one bounded group search per call,
// short-lived connections, filter input always escaped.
package ldap

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	ldapv3 "github.com/go-ldap/ldap/v3"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Config wires the directory connection.
type Config struct {
	// URL is the server ("ldaps://host:636" or "ldap://host:389").
	URL string
	// BindDN and BindPassword authenticate the read-only service account.
	BindDN       string
	BindPassword string
	// BaseDN roots the group search (e.g. "ou=groups,dc=bb-open,dc=com").
	BaseDN string
	// GroupFilter selects group entries. Default "(objectClass=groupOfNames)"
	// (lldap/OpenLDAP); AD uses "(objectClass=group)".
	GroupFilter string
	// NameAttr is the attribute shown and matched in bindings. Default "cn".
	NameAttr string
	// InsecureSkipVerify disables TLS verification (labs only).
	InsecureSkipVerify bool
	// Logger receives operational warnings, notably the cleartext-bind
	// warning below. Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// Directory implements ports.Directory.
type Directory struct {
	cfg      Config
	log      *slog.Logger
	warnOnce sync.Once // guards the cleartext-bind warning: log it once, not per request
}

// New validates the config and returns the directory.
func New(cfg Config) (*Directory, error) {
	if cfg.URL == "" || cfg.BaseDN == "" {
		return nil, fmt.Errorf("ldap directory needs URL and base DN")
	}
	// A Simple bind with a non-empty DN and an EMPTY password is an
	// "unauthenticated bind" (RFC 4513 5.1.2): many directories (AD,
	// OpenLDAP) accept it as a SUCCESS without checking any credential, so a
	// service account whose password went missing/blank in config would
	// silently authenticate as anonymous instead of failing loudly. Refuse
	// the misconfiguration at startup rather than let it degrade silently.
	if cfg.BindDN != "" && cfg.BindPassword == "" {
		return nil, fmt.Errorf("ldap directory: bind DN %q set with an empty bind password (unauthenticated bind); set BindPassword or clear BindDN", cfg.BindDN)
	}
	if cfg.GroupFilter == "" {
		cfg.GroupFilter = "(objectClass=groupOfNames)"
	}
	if cfg.NameAttr == "" {
		cfg.NameAttr = "cn"
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Directory{cfg: cfg, log: log}, nil
}

// maxGroups bounds one directory answer; pickers page client-side.
const maxGroups = 500

// searchFilter combines the group filter with an escaped substring match.
func searchFilter(groupFilter, nameAttr, query string) string {
	if query == "" {
		return groupFilter
	}
	return fmt.Sprintf("(&%s(%s=*%s*))",
		groupFilter, nameAttr, ldapv3.EscapeFilter(query))
}

// ListGroups implements ports.Directory.
func (d *Directory) ListGroups(ctx context.Context, query string) ([]ports.DirectoryGroup, error) {
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w: %w", ports.ErrUnavailable, err)
	}
	defer func() { _ = conn.Close() }()
	if d.cfg.BindDN != "" {
		if err := conn.Bind(d.cfg.BindDN, d.cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap bind: %w: %w", ports.ErrUnavailable, err)
		}
	}
	req := ldapv3.NewSearchRequest(
		d.cfg.BaseDN, ldapv3.ScopeWholeSubtree, ldapv3.NeverDerefAliases,
		maxGroups, int((10 * time.Second).Seconds()), false,
		searchFilter(d.cfg.GroupFilter, d.cfg.NameAttr, query),
		[]string{d.cfg.NameAttr}, nil)
	res, err := conn.Search(req)
	if err != nil {
		// A size-limit overrun still carries the first page.
		if !ldapv3.IsErrorWithCode(err, ldapv3.LDAPResultSizeLimitExceeded) || res == nil {
			return nil, fmt.Errorf("ldap search: %w", err)
		}
	}
	return mapGroups(res.Entries, d.cfg.NameAttr), nil
}

// mapGroups projects LDAP search entries onto directory groups, keyed by DN
// with the configured name attribute as the display name. Entries without a
// name are skipped (a group with no cn is not selectable). Pure, so the
// projection is unit-tested without a live directory.
func mapGroups(entries []*ldapv3.Entry, nameAttr string) []ports.DirectoryGroup {
	out := make([]ports.DirectoryGroup, 0, len(entries))
	for _, e := range entries {
		name := e.GetAttributeValue(nameAttr)
		if name == "" {
			continue
		}
		out = append(out, ports.DirectoryGroup{ID: e.DN, Name: name})
	}
	return out
}

// bindIsCleartext reports whether a bind on this config would send
// BindPassword unencrypted: a bind is actually attempted (BindDN and
// BindPassword both set) and the URL is not ldaps://. Pure, so the warning
// trigger is unit-tested without a live directory.
func bindIsCleartext(url, bindDN, bindPassword string) bool {
	return bindDN != "" && bindPassword != "" && !strings.HasPrefix(url, "ldaps://")
}

// warnCleartextBindOnce logs, at most once per Directory, that the bind
// password is about to traverse this connection unencrypted. We cannot force
// TLS here - some deployments only reach the directory over ldap:// on a
// trusted in-cluster/mesh path - but that must not be a SILENT cleartext
// credential exposure; it must show up in logs/alerting.
func (d *Directory) warnCleartextBindOnce() {
	if !bindIsCleartext(d.cfg.URL, d.cfg.BindDN, d.cfg.BindPassword) {
		return
	}
	d.warnOnce.Do(func() {
		d.log.Warn("ldap bind credentials sent in cleartext: BindPassword traverses "+
			"this ldap:// connection unencrypted; use ldaps:// or route ldap:// only "+
			"over a trusted mTLS/mesh path",
			"url", d.cfg.URL)
	})
}

// dial connects with a per-call deadline; ldaps URLs get TLS.
func (d *Directory) dial(ctx context.Context) (*ldapv3.Conn, error) {
	d.warnCleartextBindOnce()
	var opts []ldapv3.DialOpt
	if strings.HasPrefix(d.cfg.URL, "ldaps://") {
		opts = append(opts, ldapv3.DialWithTLSConfig(&tls.Config{
			// #nosec G402 - defaults to false (verified); the operator can opt into skip-verify only for a lab directory with a self-signed cert.
			InsecureSkipVerify: d.cfg.InsecureSkipVerify, // labs only
			MinVersion:         tls.VersionTLS12,
		}))
	}
	// Bound the dial itself, not just post-connect operations: DialURL
	// otherwise inherits the OS connect timeout (~60s), so an unreachable
	// directory would hang the page instead of degrading to "no groups".
	dialTimeout := 3 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem > 0 && rem < dialTimeout {
			dialTimeout = rem
		}
	}
	opts = append(opts, ldapv3.DialWithDialer(&net.Dialer{Timeout: dialTimeout}))
	conn, err := ldapv3.DialURL(d.cfg.URL, opts...)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetTimeout(time.Until(dl))
	} else {
		conn.SetTimeout(10 * time.Second)
	}
	return conn, nil
}
