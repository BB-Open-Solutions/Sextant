// Command dfctl is the headless client for the Sextant API (/api/v1):
// the CLI half of API-first. Configuration via environment (SEXTANT_URL,
// SEXTANT_TOKEN - a secret never travels on argv) or flags for the URL.
//
// Usage: dfctl [-url U] [-json] <resource> <verb> [args]
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

const usage = `dfctl - Sextant fleet CLI

Resources and verbs:
  devices   list | get TAG | enroll TAG -hardware H [-group G] [-class C] | remove TAG
  settings  set SCOPE KEY VALUE [-enforce] | clear SCOPE KEY
  changes   list | open ID TITLE | edit ID SCOPE KEY VALUE | diff ID |
            submit ID | merge ID | abandon ID
  rollout   status | start TARGET | tick | cancel
  status    [TAG]
  access    list | grant GROUP ROLE SCOPE | revoke GROUP SCOPE
  fleet     get

SCOPE is org | group:<name> | device:<tag>. VALUE parses as JSON when
possible (true, 42, "text"), else as a string.

Environment: SEXTANT_URL (or -url), SEXTANT_TOKEN (required).
Flags: -json forces JSON output for list commands.`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("dfctl", flag.ContinueOnError)
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
		fmt.Fprintln(os.Stderr, "dfctl: SEXTANT_URL and SEXTANT_TOKEN are required")
		return 2
	}
	c := newClient(*url, token)

	err := dispatch(c, *asJSON, rest)
	switch {
	case err == nil:
		return 0
	case errors.As(err, new(*usageError)):
		fmt.Fprintln(os.Stderr, "dfctl:", err)
		return 2
	default:
		fmt.Fprintln(os.Stderr, "dfctl:", err)
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
	case "fleet":
		var out any
		if err := c.do("GET", "/api/v1/fleet", nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	}
	return usagef("unknown resource %q", res)
}

func devicesCmd(c *client, asJSON bool, verb string, rest []string) error {
	switch verb {
	case "list":
		var out []map[string]any
		if err := c.do("GET", "/api/v1/devices", nil, &out); err != nil {
			return err
		}
		if asJSON {
			printJSON(out)
			return nil
		}
		rows := make([][]string, 0, len(out))
		for _, d := range out {
			rows = append(rows, []string{str(d["tag"]), str(d["class"]),
				str(d["hardware"]), fmt.Sprint(d["groups"])})
		}
		table([]string{"TAG", "CLASS", "HARDWARE", "GROUPS"}, rows)
		return nil
	case "get":
		if len(rest) != 1 {
			return usagef("devices get TAG")
		}
		var out any
		if err := c.do("GET", "/api/v1/devices/"+rest[0], nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	case "enroll":
		fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
		hw := fs.String("hardware", "", "hardware profile (required)")
		group := fs.String("group", "", "group")
		class := fs.String("class", "", "class")
		if len(rest) < 1 {
			return usagef("devices enroll TAG -hardware H")
		}
		if err := fs.Parse(rest[1:]); err != nil {
			return usagef("enroll flags: %v", err)
		}
		if *hw == "" {
			return usagef("-hardware is required")
		}
		in := map[string]any{"tag": rest[0], "hardware": *hw, "class": *class}
		if *group != "" {
			in["groups"] = []string{*group}
		}
		return c.do("POST", "/api/v1/devices", in, nil)
	case "remove":
		if len(rest) != 1 {
			return usagef("devices remove TAG")
		}
		return c.do("DELETE", "/api/v1/devices/"+rest[0], nil, nil)
	}
	return usagef("devices: unknown verb %q", verb)
}

func settingsCmd(c *client, verb string, rest []string) error {
	switch verb {
	case "set":
		fs := flag.NewFlagSet("set", flag.ContinueOnError)
		enforce := fs.Bool("enforce", false, "lock for lower scopes")
		if len(rest) < 3 {
			return usagef("settings set SCOPE KEY VALUE [-enforce]")
		}
		if err := fs.Parse(rest[3:]); err != nil {
			return usagef("set flags: %v", err)
		}
		in := map[string]any{"scope": rest[0], "key": rest[1], "value": parseValue(rest[2])}
		if *enforce {
			in["enforce"] = true
		}
		return c.do("POST", "/api/v1/settings", in, nil)
	case "clear":
		if len(rest) != 2 {
			return usagef("settings clear SCOPE KEY")
		}
		return c.do("DELETE", "/api/v1/settings",
			map[string]any{"scope": rest[0], "key": rest[1]}, nil)
	}
	return usagef("settings: unknown verb %q", verb)
}

func changesCmd(c *client, asJSON bool, verb string, rest []string) error {
	switch verb {
	case "list":
		var out []map[string]any
		if err := c.do("GET", "/api/v1/changes", nil, &out); err != nil {
			return err
		}
		if asJSON {
			printJSON(out)
			return nil
		}
		rows := make([][]string, 0, len(out))
		for _, cr := range out {
			rows = append(rows, []string{str(cr["id"]), str(cr["status"]),
				str(cr["author"]), str(cr["title"])})
		}
		table([]string{"ID", "STATUS", "AUTHOR", "TITLE"}, rows)
		return nil
	case "open":
		if len(rest) < 2 {
			return usagef("changes open ID TITLE")
		}
		return c.do("POST", "/api/v1/changes",
			map[string]any{"id": rest[0], "title": rest[1]}, nil)
	case "edit":
		if len(rest) != 4 {
			return usagef("changes edit ID SCOPE KEY VALUE")
		}
		return c.do("POST", "/api/v1/changes/"+rest[0]+"/edits",
			map[string]any{"scope": rest[1], "key": rest[2], "value": parseValue(rest[3])}, nil)
	case "diff":
		if len(rest) != 1 {
			return usagef("changes diff ID")
		}
		var out string
		if err := c.do("GET", "/api/v1/changes/"+rest[0]+"/diff", nil, &out); err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	case "submit", "merge", "abandon":
		if len(rest) != 1 {
			return usagef("changes %s ID", verb)
		}
		var out map[string]any
		if err := c.do("POST", "/api/v1/changes/"+rest[0]+"/"+verb, nil, &out); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", rest[0], str(out["status"]))
		return nil
	}
	return usagef("changes: unknown verb %q", verb)
}

func rolloutCmd(c *client, verb string, rest []string) error {
	switch verb {
	case "status", "":
		var out any
		if err := c.do("GET", "/api/v1/rollout", nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	case "start":
		if len(rest) != 1 {
			return usagef("rollout start TARGET")
		}
		return c.do("POST", "/api/v1/rollout", map[string]any{"target": rest[0]}, nil)
	case "tick":
		var out any
		if err := c.do("POST", "/api/v1/rollout/tick", nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	case "cancel":
		return c.do("DELETE", "/api/v1/rollout", nil, nil)
	}
	return usagef("rollout: unknown verb %q (use status|start TARGET|tick|cancel)", verb)
}

func statusCmd(c *client, asJSON bool, rest []string) error {
	if len(rest) == 1 {
		var out any
		if err := c.do("GET", "/api/v1/status/"+rest[0], nil, &out); err != nil {
			return err
		}
		printJSON(out)
		return nil
	}
	var out []map[string]any
	if err := c.do("GET", "/api/v1/status", nil, &out); err != nil {
		return err
	}
	if asJSON {
		printJSON(out)
		return nil
	}
	rows := make([][]string, 0, len(out))
	for _, s := range out {
		online := "offline"
		if b, _ := s["online"].(bool); b {
			online = "online"
		}
		rows = append(rows, []string{str(s["tag"]), online, str(s["revision"]), str(s["phase"])})
	}
	table([]string{"TAG", "STATE", "REVISION", "PHASE"}, rows)
	return nil
}

func accessCmd(c *client, asJSON bool, verb string, rest []string) error {
	switch verb {
	case "list", "":
		var out []map[string]any
		if err := c.do("GET", "/api/v1/access", nil, &out); err != nil {
			return err
		}
		if asJSON {
			printJSON(out)
			return nil
		}
		rows := make([][]string, 0, len(out))
		for _, b := range out {
			rows = append(rows, []string{str(b["group"]), str(b["role"]), str(b["scope"])})
		}
		table([]string{"IDP GROUP", "ROLE", "SCOPE"}, rows)
		return nil
	case "grant":
		if len(rest) != 3 {
			return usagef("access grant GROUP ROLE SCOPE")
		}
		return c.do("POST", "/api/v1/access",
			map[string]any{"group": rest[0], "role": rest[1], "scope": rest[2]}, nil)
	case "revoke":
		if len(rest) != 2 {
			return usagef("access revoke GROUP SCOPE")
		}
		return c.do("DELETE", "/api/v1/access",
			map[string]any{"group": rest[0], "scope": rest[1]}, nil)
	}
	return usagef("access: unknown verb %q", verb)
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
