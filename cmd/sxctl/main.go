// Command sxctl is the headless client for the Sextant API (/api/v1):
// the CLI half of API-first. Configuration via environment (SEXTANT_URL,
// SEXTANT_TOKEN - a secret never travels on argv) or flags for the URL.
//
// Usage: sxctl [-url U] [-json] <resource> <verb> [args]
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

const usage = `sxctl - Sextant fleet CLI

Resources and verbs:
  devices   list | get TAG | enroll TAG -hardware H [-group G] [-class C] |
            update TAG [-hardware H] [-class C] [-user U] [-groups a,b] |
            retire TAG | reactivate TAG | remove TAG |
            lock TAG | wipe TAG [-force] | unlock TAG
  groups    add NAME [-parent P] [-idp G] | update NAME [-parent P|-] [-idp G] |
            remove NAME
  apps      set SCOPE KIND [NAME ...]   (kind: packages|flatpaks|overlays)
  settings  set SCOPE KEY VALUE [-enforce] | clear SCOPE KEY
  changes   list | open ID TITLE | edit ID SCOPE KEY VALUE | diff ID |
            submit ID | merge ID | abandon ID
  rollout   status | start TARGET | tick | cancel
  status    [TAG]
  access    list | grant GROUP ROLE SCOPE | revoke GROUP SCOPE
  tokens    list | mint NAME [-ceiling R] [-ttl-days N] | revoke ID
  me        [prefs [TIMEZONE LOCALE]]   (who am I; get/set preferences)
  audit     (config commit trail)
  evidence  [FROM TO]   (audit bundle, RFC 3339; default last 30 days)
  fleet     get

SCOPE is org | group:<name> | device:<tag>. VALUE parses as JSON when
possible (true, 42, "text"), else as a string.

Environment: SEXTANT_URL (or -url), SEXTANT_TOKEN (required).
Flags: -json forces JSON output for list commands.`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("sxctl", flag.ContinueOnError)
	url := fs.String("url", os.Getenv("SEXTANT_URL"), "Sextant base URL")
	asJSON := fs.Bool("json", false, "JSON output for lists")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fs.Usage()
		return 2
	}
	token := os.Getenv("SEXTANT_TOKEN")
	if *url == "" || token == "" {
		fmt.Fprintln(os.Stderr, "sxctl: SEXTANT_URL and SEXTANT_TOKEN are required")
		return 2
	}
	c := newClient(*url, token)

	err := dispatch(c, *asJSON, rest)
	switch {
	case err == nil:
		return 0
	case errors.As(err, new(*usageError)):
		fmt.Fprintln(os.Stderr, "sxctl:", err)
		return 2
	default:
		fmt.Fprintln(os.Stderr, "sxctl:", err)
		return 1
	}
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func usagef(f string, a ...any) error {
	return &usageError{fmt.Sprintf(f, a...)}
}

// parseValue interprets CLI values: valid JSON stays typed, else string.
func parseValue(s string) any {
	var v any
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&v); err == nil && !dec.More() {
		return v
	}
	return s
}

func dispatch(c *client, asJSON bool, args []string) error {
	res, verb := args[0], ""
	if len(args) > 1 {
		verb = args[1]
	}
	rest := args[min(2, len(args)):]

	switch res {
	case "devices":
		return devicesCmd(c, asJSON, verb, rest)
	case "groups":
		return groupsCmd(c, verb, rest)
	case "apps":
		return appsCmd(c, verb, rest)
	case "settings":
		return settingsCmd(c, verb, rest)
	case "changes":
		return changesCmd(c, asJSON, verb, rest)
	case "rollout":
		return rolloutCmd(c, verb, rest)
	case "status":
		return statusCmd(c, asJSON, args[1:])
	case "access":
		return accessCmd(c, asJSON, verb, rest)
	case "tokens":
		return tokensCmd(c, asJSON, verb, rest)
	case "fleet":
		var out any
		if err := c.do("GET", "/api/v1/fleet", nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	case "me":
		path := "/api/v1/me"
		if verb == "prefs" {
			path = "/api/v1/me/preferences"
			if len(rest) == 2 { // me prefs TIMEZONE LOCALE ("" keeps default)
				return c.do("PUT", path,
					map[string]any{"timezone": rest[0], "locale": rest[1]}, nil)
			}
		}
		var out any
		if err := c.do("GET", path, nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	case "audit":
		var out any
		if err := c.do("GET", "/api/v1/audit", nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	case "evidence":
		path := "/api/v1/evidence"
		if len(rest) == 2 { // evidence FROM TO (RFC 3339)
			path += "?from=" + rest[0] + "&to=" + rest[1]
		}
		var out any
		if err := c.do("GET", path, nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	}
	return usagef("unknown resource %q", res)
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "token"
	}
	return out
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
