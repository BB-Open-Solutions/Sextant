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
			c.printJSON(out)
			return nil
		}
		rows := make([][]string, 0, len(out))
		for _, d := range out {
			rows = append(rows, []string{str(d["tag"]), str(d["class"]),
				str(d["hardware"]), fmt.Sprint(d["groups"])})
		}
		c.table([]string{"TAG", "CLASS", "HARDWARE", "GROUPS"}, rows)
		return nil
	case "get":
		if len(rest) != 1 {
			return usagef("devices get TAG")
		}
		var out any
		if err := c.do("GET", "/api/v1/devices/"+rest[0], nil, &out); err != nil {
			return err
		}
		c.printJSON(out)
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
		c.printJSON(out) // includes the fresh credential, shown once
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
