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
	"regexp"
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
	// Both values are spliced into the search filter, and NEITHER may be
	// escaped: GroupFilter is a filter expression by design (escaping it would
	// break exactly the thing it is for) and NameAttr is an attribute name, not
	// data. They come from deploy-time configuration and never from a user, so
	// there is no injection path - a review that called this "LDAP filter
	// injection" and recommended EscapeFilter on both had the shape right and
	// the diagnosis wrong; taking that advice would have broken group lookups.
	//
	// The real failure mode is silence. A typo in either value produces a
	// filter that is syntactically fine and semantically wrong, so the console
	// shows an empty group picker and nothing anywhere says why. That is the
	// same class of failure as the directory ACL that answered every search
	// with "no such object" and cost an hour of looking in the wrong place
	// (docs/e2e-2-findings.md). Refuse to start instead.
	if !attrNameRe.MatchString(cfg.NameAttr) {
		return nil, fmt.Errorf("ldap directory: name attribute %q is not a valid LDAP attribute name (letters, digits and hyphens, starting with a letter)", cfg.NameAttr)
	}
	if _, err := ldapv3.CompileFilter(cfg.GroupFilter); err != nil {
		return nil, fmt.Errorf("ldap directory: group filter %q is not a valid LDAP filter: %w", cfg.GroupFilter, err)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Directory{cfg: cfg, log: log}, nil
}

// maxGroups bounds one directory answer; pickers page client-side.
const maxGroups = 500

// attrNameRe is the shape of an LDAP attribute description (RFC 4512 keystring:
// a letter followed by letters, digits and hyphens). Deliberately narrower than
// the RFC's full option syntax - a group-name attribute never needs options.
var attrNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

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

// maxDial bounds the connect itself, not just post-connect operations: DialURL
// otherwise inherits the OS connect timeout, around a minute, so an
// unreachable directory would hang the page instead of degrading to "no
// groups".
const maxDial = 3 * time.Second

// dialTimeout is the connect budget: maxDial, or whatever the caller has left
// if that is less.
//
// Extracted from dial because the alternative was untestable. Driving it
// through a real connect needs an address that HANGS rather than one that
// refuses, and there is no portable one: a test against TEST-NET-1 passed in
// twelve milliseconds with the timeout mutated to ninety seconds, because this
// machine answers "network unreachable" immediately. A test that cannot fail
// is worse than none, so the decision moved to where it can be checked.
func dialTimeout(ctx context.Context) time.Duration {
	dl, ok := ctx.Deadline()
	if !ok {
		return maxDial
	}
	// A deadline already past yields a non-positive remainder. Passing that to
	// net.Dialer means NO timeout, which is the opposite of what the caller
	// asked for, so an expired context keeps the ceiling and lets the dial
	// fail on its own.
	if rem := time.Until(dl); rem > 0 && rem < maxDial {
		return rem
	}
	return maxDial
}

// dial connects with a per-call deadline; ldaps URLs get TLS.
func (d *Directory) dial(ctx context.Context) (*ldapv3.Conn, error) {
	d.warnCleartextBindOnce()
	var opts []ldapv3.DialOpt
	if strings.HasPrefix(d.cfg.URL, "ldaps://") {
		// No InsecureSkipVerify knob. It existed, defaulted to false, and
		// was never wired to an environment variable, a helm value or a fleet
		// setting - so it could not be turned on, which is why a review that
		// flagged it as an operator footgun was wrong. It is gone anyway: a
		// dead field that disables certificate verification is an invitation
		// to the next person who needs to "just test something quickly", and
		// a lab with a self-signed certificate has a better answer already -
		// point identity.tlsCaCert at the CA, the same way a device does.
		opts = append(opts, ldapv3.DialWithTLSConfig(&tls.Config{
			MinVersion: tls.VersionTLS12,
		}))
	}
	opts = append(opts, ldapv3.DialWithDialer(&net.Dialer{Timeout: dialTimeout(ctx)}))
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
