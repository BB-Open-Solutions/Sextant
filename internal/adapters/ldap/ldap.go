// Package ldap implements ports.Directory against an LDAP directory
// (lldap, OpenLDAP, AD). Read-only: one bounded group search per call,
// short-lived connections, filter input always escaped.
package ldap

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
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
}

// Directory implements ports.Directory.
type Directory struct{ cfg Config }

// New validates the config and returns the directory.
func New(cfg Config) (*Directory, error) {
	if cfg.URL == "" || cfg.BaseDN == "" {
		return nil, fmt.Errorf("ldap directory needs URL and base DN")
	}
	if cfg.GroupFilter == "" {
		cfg.GroupFilter = "(objectClass=groupOfNames)"
	}
	if cfg.NameAttr == "" {
		cfg.NameAttr = "cn"
	}
	return &Directory{cfg: cfg}, nil
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
		return nil, fmt.Errorf("ldap dial: %w: %v", ports.ErrUnavailable, err)
	}
	defer conn.Close()
	if d.cfg.BindDN != "" {
		if err := conn.Bind(d.cfg.BindDN, d.cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap bind: %w: %v", ports.ErrUnavailable, err)
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

// dial connects with a per-call deadline; ldaps URLs get TLS.
func (d *Directory) dial(ctx context.Context) (*ldapv3.Conn, error) {
	var opts []ldapv3.DialOpt
	if strings.HasPrefix(d.cfg.URL, "ldaps://") {
		opts = append(opts, ldapv3.DialWithTLSConfig(&tls.Config{
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
