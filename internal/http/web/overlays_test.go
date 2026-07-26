package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestOverlaysPageWriteAndRemove(t *testing.T) {
	ts, cfg := newConsole(t) // dev session is org owner; allow-all gate
	c := client()

	// Empty page renders.
	resp, _ := c.Get(ts.URL + "/overlays")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "No overlays yet") {
		t.Fatalf("overlays page = %d\n%s", resp.StatusCode, body)
	}

	// Write an overlay -> committed.
	code := "{ ... }:\n{\n}\n"
	resp, _ = c.PostForm(ts.URL+"/overlays", url.Values{"csrf": {"dev-csrf"}, "name": {"k8s-node"}, "code": {code}})
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("write overlay = %d, want 303", resp.StatusCode)
	}
	if names, _ := cfg.ListOverlays(); len(names) != 1 || names[0] != "k8s-node" {
		t.Fatalf("overlay not written: %v", names)
	}

	// It appears + its code loads in the editor.
	resp, _ = c.Get(ts.URL + "/overlays?name=k8s-node")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "k8s-node.nix") {
		t.Fatalf("selected overlay not rendered\n%s", body)
	}

	// Remove it.
	resp, _ = c.PostForm(ts.URL+"/overlays/k8s-node/remove", url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if names, _ := cfg.ListOverlays(); len(names) != 0 {
		t.Fatalf("overlay still present after remove: %v", names)
	}
}

func TestOverlaysTemplatePrefill(t *testing.T) {
	ts, _ := newConsole(t)
	c := client()
	resp, _ := c.Get(ts.URL + "/overlays?template=k8s-node")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	// The picker is offered and the k8s starter code is prefilled.
	if !strings.Contains(s, "Start from a class template") {
		t.Fatal("template picker not shown")
	}
	if !strings.Contains(s, "virtualisation.containerd.enable") || !strings.Contains(s, `value="k8s-node"`) {
		t.Fatalf("k8s template not prefilled\n%s", s)
	}
}

func TestOverlayCheckWithoutChecker(t *testing.T) {
	ts, _ := newConsole(t)
	form := url.Values{"csrf": {"dev-csrf"}, "name": {"k8s-node"}, "code": {"{ config, ... }: { }"}}
	resp, err := client().PostForm(ts.URL+"/overlays/check", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("check = %d", resp.StatusCode)
	}
	// No syntax checker wired (gate none in tests): the page says the fast
	// check is unavailable, and still renders the editor with the code.
	if !strings.Contains(string(body), "needs the remote validation gate") {
		t.Error("unavailable note missing")
	}
	if !strings.Contains(string(body), "config") {
		t.Error("editor did not re-render the code")
	}
}
