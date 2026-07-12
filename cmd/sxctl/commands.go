package main

import (
	"flag"
	"fmt"
	"strings"
)

// commands.go: the per-resource sxctl subcommands (devices, groups, apps,
// settings, changes, rollout, status, access, tokens). Split out of main.go.

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
	case "update":
		fs := flag.NewFlagSet("update", flag.ContinueOnError)
		hw := fs.String("hardware", "", "hardware profile")
		class := fs.String("class", "", "class")
		user := fs.String("user", "", "assigned user")
		groups := fs.String("groups", "", "comma-separated group list (replaces)")
		if len(rest) < 1 {
			return usagef("devices update TAG [-hardware H] [-class C] [-user U] [-groups a,b]")
		}
		if err := fs.Parse(rest[1:]); err != nil {
			return usagef("update flags: %v", err)
		}
		in := map[string]any{}
		for k, v := range map[string]*string{"hardware": hw, "class": class, "assignedUser": user} {
			if *v != "" {
				in[k] = *v
			}
		}
		if *groups != "" {
			in["groups"] = strings.Split(*groups, ",")
		}
		if len(in) == 0 {
			return usagef("nothing to update")
		}
		return c.do("PATCH", "/api/v1/devices/"+rest[0], in, nil)
	case "retire":
		if len(rest) != 1 {
			return usagef("devices retire TAG")
		}
		return c.do("POST", "/api/v1/devices/"+rest[0]+"/retire", nil, nil)
	case "lock":
		if len(rest) != 1 {
			return usagef("devices lock TAG")
		}
		return c.do("POST", "/api/v1/devices/"+rest[0]+"/intent",
			map[string]any{"intent": "lock"}, nil)
	case "wipe":
		fs := flag.NewFlagSet("wipe", flag.ContinueOnError)
		force := fs.Bool("force", false, "wipe without locking first (lost device)")
		if len(rest) < 1 {
			return usagef("devices wipe TAG [-force]")
		}
		if err := fs.Parse(rest[1:]); err != nil {
			return usagef("wipe flags: %v", err)
		}
		return c.do("POST", "/api/v1/devices/"+rest[0]+"/intent",
			map[string]any{"intent": "wipe", "force": *force}, nil)
	case "unlock", "clear-intent":
		if len(rest) != 1 {
			return usagef("devices unlock TAG")
		}
		return c.do("DELETE", "/api/v1/devices/"+rest[0]+"/intent", nil, nil)
	case "reactivate":
		if len(rest) != 1 {
			return usagef("devices reactivate TAG")
		}
		var out any
		if err := c.do("POST", "/api/v1/devices/"+rest[0]+"/reactivate", nil, &out); err != nil {
			return err
		}
		printJSON(out) // includes the fresh credential, shown once
		return nil
	}
	return usagef("devices: unknown verb %q", verb)
}

// groupsCmd manages the group tree.
func groupsCmd(c *client, verb string, rest []string) error {
	switch verb {
	case "add":
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		parent := fs.String("parent", "", "parent group")
		idp := fs.String("idp", "", "IdP group mapping")
		if len(rest) < 1 {
			return usagef("groups add NAME [-parent P] [-idp G]")
		}
		if err := fs.Parse(rest[1:]); err != nil {
			return usagef("add flags: %v", err)
		}
		return c.do("POST", "/api/v1/groups",
			map[string]any{"name": rest[0], "parent": *parent, "idpGroup": *idp}, nil)
	case "update":
		fs := flag.NewFlagSet("update", flag.ContinueOnError)
		parent := fs.String("parent", "", "new parent (\"-\" detaches to root)")
		idp := fs.String("idp", "", "IdP group mapping")
		if len(rest) < 1 {
			return usagef("groups update NAME [-parent P|-] [-idp G]")
		}
		if err := fs.Parse(rest[1:]); err != nil {
			return usagef("update flags: %v", err)
		}
		in := map[string]any{}
		if *parent != "" {
			p := *parent
			if p == "-" {
				p = ""
			}
			in["parent"] = p
		}
		if *idp != "" {
			in["idpGroup"] = *idp
		}
		if len(in) == 0 {
			return usagef("nothing to update")
		}
		return c.do("PATCH", "/api/v1/groups/"+rest[0], in, nil)
	case "remove":
		if len(rest) != 1 {
			return usagef("groups remove NAME")
		}
		return c.do("DELETE", "/api/v1/groups/"+rest[0], nil, nil)
	}
	return usagef("groups: unknown verb %q (add|update|remove)", verb)
}

// appsCmd replaces an additive app list at a scope.
func appsCmd(c *client, verb string, rest []string) error {
	if verb != "set" || len(rest) < 2 {
		return usagef("apps set SCOPE KIND [NAME ...]  (kind: packages|flatpaks|overlays)")
	}
	return c.do("PUT", "/api/v1/apps",
		map[string]any{"scope": rest[0], "kind": rest[1], "names": rest[2:]}, nil)
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
