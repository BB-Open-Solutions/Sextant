// Package web is the human console: server-rendered pages over the same app
// services the JSON API uses. Handlers are thin - parse, one service call,
// render - and every mutation carries a CSRF token and per-scope RBAC.
package web

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Sessions provides the authenticated user for a request (the oidc
// adapter, or a dev stub on loopback).
type Sessions interface {
	SessionUser(r *http.Request) (identity.User, string, bool)
}

// Services are the app services the console renders.
type Services struct {
	Config    *app.ConfigService
	Changes   *app.ChangeService
	Rollouts  *app.RolloutService
	Inventory *app.InventoryService
	Tokens    *app.TokenService
	Prefs     ports.PrefsStore
	DevCreds  *app.DeviceCredentials
	Directory ports.Directory
	Evidence  *app.EvidenceService
	Discovery *app.DiscoveryService
	Imaging   *app.ImagingService
	// DeviceSecrets seals/reveals per-device secrets (LUKS, break-glass admin).
	DeviceSecrets *app.DeviceSecretsService
	StationCreds  *app.StationCredentials
	Notify        *app.NotifyService
	Mail          *app.MailService
	Users         ports.UserDirectory
	Compliance    *app.ComplianceService
}

// Server renders the console.
type Server struct {
	svc      Services
	sessions Sessions
	tmpl     map[string]*template.Template
	log      *slog.Logger
	write    bool

	baseViewer, baseEditor, baseOwner []string

	// Organisation presentation defaults; user preferences override.
	defaultLocale, defaultTZ string
	// orgName is the organisation's display name: the scope tree's root is
	// the organisation, and showing its actual name (instead of a generic
	// "root") keeps that legible everywhere scopes appear.
	orgName string
}

// SetDefaults configures the organisation's presentation defaults
// (locale and IANA timezone) applied when a user has no preference.
func (s *Server) SetDefaults(locale, tz string) {
	if locale != "" {
		s.defaultLocale = locale
	}
	if tz != "" {
		s.defaultTZ = tz
	}
}

// SetOrgName sets the organisation's display name (see Server.orgName).
func (s *Server) SetOrgName(name string) {
	if name != "" {
		s.orgName = name
	}
}

// New builds the console server. Baselines mirror the API's org-wide role
// groups.
func New(svc Services, sessions Sessions, write bool,
	baseViewer, baseEditor, baseOwner []string, log *slog.Logger) (*Server, error) {
	funcs := templateFuncs()
	pages := []string{"overview", "devices", "device", "groups", "settings", "policies", "compliance", "changes", "diff", "rollout", "access", "audit", "profile", "station", "secrets", "updates", "service_accounts", "enroll", "wizard", "secret_reveal", "integrations", "overlays", "notifications", "mail", "org", "error"}
	tmpl := make(map[string]*template.Template, len(pages)+1)
	for _, p := range pages {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, err
		}
		tmpl[p] = t
	}
	login, err := template.ParseFS(assets, "templates/login.html")
	if err != nil {
		return nil, err
	}
	tmpl["login"] = login
	return &Server{svc: svc, sessions: sessions, tmpl: tmpl, log: log, write: write,
		baseViewer: baseViewer, baseEditor: baseEditor, baseOwner: baseOwner,
		defaultLocale: "en", defaultTZ: "UTC", orgName: "Organisation"}, nil
}

// Routes registers the console.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /login", s.handleLoginPage)

	get := func(p string, h func(http.ResponseWriter, *http.Request, view)) {
		mux.Handle("GET "+p, s.page(h))
	}
	post := func(p string, h func(http.ResponseWriter, *http.Request, view) error) {
		mux.Handle("POST "+p, s.action(h))
	}

	get("/{$}", s.overview)
	get("/devices", s.devices)
	get("/devices/{tag}", s.device)
	get("/groups", s.groupsPage)
	get("/settings", s.settingsPage)
	get("/policies", s.policies)
	get("/compliance", s.compliancePage)
	get("/changes", s.changesPage)
	// A change's home is the Updates board; old notification links and
	// bookmarks to /changes/<id> land there instead of a 404.
	get("/changes/{id}", func(w http.ResponseWriter, r *http.Request, _ view) {
		http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	})
	get("/changes/{id}/diff", s.diffPage)
	get("/updates", s.updatesPage)
	get("/updates/rollout", s.rolloutMonitorPage)
	// The old names live on as redirects; the WORD pipeline left the UI.
	get("/pipeline", func(w http.ResponseWriter, r *http.Request, _ view) {
		http.Redirect(w, r, "/updates", http.StatusMovedPermanently)
	})
	get("/rollout", func(w http.ResponseWriter, r *http.Request, _ view) {
		http.Redirect(w, r, "/updates/rollout", http.StatusMovedPermanently)
	})
	get("/access", s.accessPage)
	get("/audit", s.auditPage)
	get("/audit/evidence", s.auditEvidence)
	get("/downloads/sxctl", s.downloadCLI)
	get("/downloads/sxctl.sha256", s.cliChecksum)
	get("/station", s.stationPage)
	get("/enroll", s.enrollPage)
	get("/enroll/{station}/wizard", s.enrollWizard)
	get("/integrations", s.integrationsPage)
	get("/overlays", s.overlaysPage)
	get("/secrets", s.secretsPage)
	get("/service-accounts", s.serviceAccountsPage)
	get("/profile", s.profilePage)
	get("/notifications", s.notificationsPage)
	get("/org", s.orgPage)
	get("/mail", s.mailPage)

	post("/notifications/read-all", s.postNotificationsReadAll)
	post("/notifications/{id}/read", s.postNotificationRead)
	post("/mail", s.postMailSave)
	post("/mail/test", s.postMailTest)
	post("/mail/delete", s.postMailDelete)
	post("/devices", s.postDeviceEnroll)
	post("/devices/{tag}/settings", s.postDeviceSetting)
	post("/devices/{tag}/posture", s.postDevicePosture)
	post("/devices/{tag}/intent", s.postDeviceIntent)
	post("/devices/{tag}/intent/clear", s.postDeviceIntentClear)
	post("/devices/{tag}/secret/{kind}/reveal", s.postSecretReveal)
	post("/devices/{tag}/retire", s.postDeviceRetire)
	post("/devices/{tag}/reactivate", s.postDeviceReactivate)
	post("/devices/{tag}/remove", s.postDeviceRemove)
	post("/devices/{tag}/credential", s.postDeviceCredential)
	post("/devices/{tag}/update", s.postDeviceUpdate)
	post("/devices/group", s.postDevicesGroupCreate)
	post("/settings", s.postSetting)
	post("/policies", s.postPolicyPut)
	post("/policies/{id}/delete", s.postPolicyDelete)
	post("/assignments", s.postAssignmentAdd)
	post("/assignments/delete", s.postAssignmentDelete)
	post("/filters", s.postFilterPut)
	post("/filters/{id}/delete", s.postFilterDelete)
	post("/apps", s.postScopeApps)
	post("/groups", s.postGroupAdd)
	post("/groups/{name}/update", s.postGroupUpdate)
	post("/groups/{name}/unpin", s.postGroupUnpin)
	post("/groups/{name}/remove", s.postGroupRemove)
	post("/changes", s.postChange)
	post("/changes/{id}/edits", s.postChangeEdit)
	post("/changes/{id}/submit", s.postChangeSubmit)
	post("/changes/{id}/merge", s.postChangeMerge)
	post("/changes/{id}/abandon", s.postChangeAbandon)
	post("/rollout", s.postRolloutStart)
	post("/rollout/plan", s.postRolloutPlan)
	post("/rollout/tick", s.postRolloutTick)
	post("/rollout/approve", s.postRolloutApprove)
	post("/rollout/cancel", s.postRolloutCancel)
	post("/rollout/pause", s.postRolloutPause)
	post("/rollout/resume", s.postRolloutResume)
	post("/access/grant", s.postAccessGrant)
	post("/access/revoke", s.postAccessRevoke)
	post("/assurance", s.postAssurance)
	post("/profile/prefs", s.postProfilePrefs)
	post("/profile/tokens", s.postProfileTokenMint)
	post("/profile/tokens/{id}/revoke", s.postProfileTokenRevoke)
	post("/enroll/{station}/batch", s.postEnrollBatch)
	post("/enroll/{station}/discovered/{mac}/remove", s.postDiscoveredRemove)
	post("/enroll/{station}/jobs/{mac}/cancel", s.postEnrollJobCancel)
	post("/stations", s.postStationRegister)
	post("/station/{tag}/credential", s.postStationCredential)
	post("/station/{tag}/remove", s.postStationRemove)
	post("/secrets", s.postSecretRegister)
	post("/secrets/{name}/remove", s.postSecretRemove)
	post("/overlays", s.postOverlayWrite)
	post("/overlays/{name}/remove", s.postOverlayRemove)
	post("/service-accounts", s.postServiceAccountMint)
	post("/service-accounts/{id}/revoke", s.postServiceAccountRevoke)
	post("/acceptances", s.postAcceptance)
	post("/acceptances/clear", s.postAcceptanceClear)
}
