package web

import (
	"net/http"
	"net/url"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// overlays.go: the custom-overlay surface (ADR 0014). An owner authors Nix
// overlay modules (overlays/<name>.nix) in a code editor; each save passes the
// Nix eval gate before it commits, so a module that does not build never
// reaches git. Scopes then select overlays through the apps/overlays picker.
// Authoring is owner-only: an overlay is arbitrary code the generator imports.

// overlayTemplate is a starter module for a device class, so an operator edits
// from a working base rather than a blank file (ADR 0014, slice 3).
type overlayTemplate struct {
	Key, Name, Suggest, Desc, Code string
}

// overlayTemplates are the built-in class starters offered on a new overlay.
var overlayTemplates = []overlayTemplate{
	{
		Key: "iot-gateway", Name: "IoT gateway", Suggest: "iot-gateway",
		Desc: "Headless data-collection gateway, reachable over the mesh.",
		Code: "{ config, lib, pkgs, ... }:\n{\n  # IoT gateway: headless, minimal, reachable over the mesh.\n  environment.systemPackages = with pkgs; [ mosquitto ];\n  # Open the MQTT port to the mesh only.\n  # networking.firewall.allowedTCPPorts = [ 1883 ];\n}\n",
	},
	{
		Key: "k8s-node", Name: "Kubernetes node", Suggest: "k8s-node",
		Desc: "Container runtime and node prerequisites for a k8s worker.",
		Code: "{ config, lib, pkgs, ... }:\n{\n  # Kubernetes worker node: container runtime + tooling.\n  virtualisation.containerd.enable = true;\n  environment.systemPackages = with pkgs; [ kubectl ];\n  # boot.kernel.sysctl.\"net.bridge.bridge-nf-call-iptables\" = 1;\n}\n",
	},
	{
		Key: "pos-terminal", Name: "POS terminal", Suggest: "pos-terminal",
		Desc: "Locked-down single-purpose kiosk for a point-of-sale app.",
		Code: "{ config, lib, pkgs, ... }:\n{\n  # POS terminal: locked-down single-purpose kiosk.\n  environment.systemPackages = with pkgs; [ chromium ];\n  # Run the POS app in a kiosk session; no general-purpose desktop.\n}\n",
	},
	{
		Key: "blank", Name: "Blank module", Suggest: "",
		Desc: "An empty module to start from scratch.",
		Code: "{ config, lib, pkgs, ... }:\n{\n  # Custom overlay for this device class.\n}\n",
	},
}

// overlaysPage lists the overlays and shows one in the editor (?name=), or a
// class-template starter for a new overlay (?template=).
func (s *Server) overlaysPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	names, err := s.svc.Config.ListOverlays()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Title": "Overlays", "Nav": "overlays",
		"Overlays": names, "Templates": overlayTemplates,
	}

	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		code, err := s.svc.Config.ReadOverlay(name)
		if err != nil {
			data["Error"] = "overlay not found: " + name
		} else {
			data["Selected"], data["Code"] = name, code
		}
	} else if tk := strings.TrimSpace(r.URL.Query().Get("template")); tk != "" {
		// New overlay pre-filled from a class starter template.
		for _, t := range overlayTemplates {
			if t.Key == tk {
				data["Code"], data["SuggestName"] = t.Code, t.Suggest
				break
			}
		}
	}
	s.render(w, "overlays", data, v)
}

// postOverlayWrite creates or replaces an overlay. A gate rejection (the module
// does not evaluate) surfaces to the operator; nothing is committed.
func (s *Server) postOverlayWrite(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	code := r.FormValue("code")
	if err := s.svc.Config.WriteOverlay(r.Context(), name, code, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/overlays?name="+url.QueryEscape(name), http.StatusSeeOther)
	return nil
}

// postOverlayRemove deletes an overlay. The gate then confirms no scope still
// selects it (a dangling reference would fail the build).
func (s *Server) postOverlayRemove(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if err := s.svc.Config.DeleteOverlay(r.Context(), r.PathValue("name"), webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/overlays", http.StatusSeeOther)
	return nil
}
