package main

import (
	"flag"
	"fmt"
)

// commands_shipping.go: sxctl subcommands for the shipping + governance side
// (changes, rollout, status, access, tokens).

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

func tokensCmd(c *client, asJSON bool, verb string, rest []string) error {
	switch verb {
	case "list", "":
		var out []map[string]any
		if err := c.do("GET", "/api/v1/tokens", nil, &out); err != nil {
			return err
		}
		if asJSON {
			printJSON(out)
			return nil
		}
		rows := make([][]string, 0, len(out))
		for _, t := range out {
			rows = append(rows, []string{str(t["id"]), str(t["name"]), str(t["kind"]),
				str(t["ceiling"]), str(t["expires"])})
		}
		table([]string{"ID", "NAME", "KIND", "CEILING", "EXPIRES"}, rows)
		return nil
	case "mint":
		fs := flag.NewFlagSet("mint", flag.ContinueOnError)
		ceiling := fs.String("ceiling", "", "viewer|editor|owner")
		ttl := fs.Int("ttl-days", 0, "expiry in days")
		if len(rest) < 1 {
			return usagef("tokens mint NAME [-ceiling R] [-ttl-days N]")
		}
		if err := fs.Parse(rest[1:]); err != nil {
			return usagef("mint flags: %v", err)
		}
		id := slugify(rest[0])
		in := map[string]any{"id": id, "name": rest[0], "ceiling": *ceiling}
		if *ttl > 0 {
			in["ttlDays"] = *ttl
		}
		var out map[string]any
		if err := c.do("POST", "/api/v1/tokens", in, &out); err != nil {
			return err
		}
		fmt.Println(str(out["secret"]))
		return nil
	case "revoke":
		if len(rest) != 1 {
			return usagef("tokens revoke ID")
		}
		return c.do("DELETE", "/api/v1/tokens/"+rest[0], nil, nil)
	}
	return usagef("tokens: unknown verb %q", verb)
}

// slugify makes a token name into a safe id.
