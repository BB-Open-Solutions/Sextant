package main

// capabilities.go wires the product's capabilities (ADR 0006): one
// constructor per capability, mounted through the registry. main.go stays
// lifecycle-only. As capabilities grow their own packages (M3), their
// constructors move with them; the registry stays.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	gateadapter "code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/gate"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	ldapdir "code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/ldap"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/nix"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/oidc"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/postgres"
	smtpadapter "code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/smtp"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/api"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/mw"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/capability"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/config"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/health"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// deps carries what the capability constructors share.
type deps struct {
	ctx    context.Context
	cfg    *config.Config
	log    *slog.Logger
	checks *health.Registry

	svc           *app.ConfigService
	changes       *app.ChangeService
	rollouts      *app.RolloutService
	inv           *app.InventoryService
	tokens        *app.TokenService
	devCreds      *app.DeviceCredentials
	discovery     *app.DiscoveryService
	imaging       *app.ImagingService
	staCreds      *app.StationCredentials
	deviceSecrets *app.DeviceSecretsService
	prefs         ports.PrefsStore
	dir           ports.Directory
	evidence      *app.EvidenceService
	notify        *app.NotifyService
	mail          *app.MailService
	users         ports.UserDirectory
	compliance    *app.ComplianceService
	authz         api.Authz
	cleanup       []func()
	// wg tracks the background workers (sync loop, rollout ticker) so shutdown
	// can wait for them to observe cancellation - they end in git commits, and
	// cutting one mid-write is never acceptable.
	wg sync.WaitGroup
}

// background starts a worker under the WaitGroup so shutdown can join it.
func (d *deps) background(fn func()) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		fn()
	}()
}

// buildCapabilities wires the config plane and returns the registry list
// plus cleanup functions (run at shutdown).
func buildCapabilities(ctx context.Context, cfg *config.Config, log *slog.Logger, checks *health.Registry) ([]capability.Capability, []func(), error) {
	if cfg.RepoDir == "" {
		return nil, nil, nil // health/metrics-only deployment
	}
	d := &deps{ctx: ctx, cfg: cfg, log: log, checks: checks}
	if err := d.buildConfigPlane(); err != nil {
		// Return whatever cleanup was already registered (e.g. an opened
		// Postgres pool) so the caller can release it; a later failure in the
		// config plane must not leak resources opened earlier.
		return nil, d.cleanup, err
	}
	caps := []capability.Capability{
		d.observedCapability(),
		d.stationCapability(),
		d.authCapability(), // mounts sessions; console reads them at mount time
		d.consoleCapability(),
		d.apiCapability(),
	}
	return caps, d.cleanup, nil
}

// buildConfigPlane constructs the services every capability shares.
func (d *deps) buildConfigPlane() error {
	cfg, log := d.cfg, d.log

	repo, err := git.Open(cfg.RepoDir, cfg.GitRemote)
	if err != nil {
		return err
	}
	var gate ports.Gate = nix.NewEvalGate()
	var builder ports.Builder = nix.NewBuilder()
	switch cfg.GateMode {
	case "none":
		log.Warn("validation gate disabled (--gate none): edits are not checked against the generator")
		gate = ports.GateFunc(func(context.Context, string, []string) error { return nil })
		builder = builderFunc(func(context.Context, string, []string) error { return nil })
	case "remote":
		log.Info("validation gate delegated to gate-runner", "url", cfg.GateURL)
		gate = gateadapter.NewRemoteGate(cfg.GateURL).WithToken(cfg.GateToken)
		// The console image ships without nix (the reason the gate-runner
		// exists), so the local nix builder cannot run here: calling it would
		// fail every change submit with a misleading "nix build" error. The
		// remote eval gate is the safety property; the heavy realisation build
		// stays a CI concern. Make the in-console build step a no-op until a
		// runner grows a build endpoint, so change requests submit and merge.
		builder = builderFunc(func(context.Context, string, []string) error { return nil })
	}
	svc, err := app.NewConfigService(repo, gate)
	if err != nil {
		return err
	}
	d.svc = svc

	// The remote is the source of truth: keep the snapshot in sync with
	// commits made outside this console (engineers, other tools).
	if repo.HasRemote() {
		d.background(func() { svc.SyncLoop(d.ctx, 30*time.Second, log) })
		log.Info("remote sync loop started", "remote", redactRemote(cfg.GitRemote))
	}

	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(cfg.RepoDir, ".sextant-state")
	}
	st, err := state.Open(stateDir)
	if err != nil {
		return err
	}
	st.SetLogger(log)
	clock := app.SystemClock{}
	// Seal operator-entered secrets at rest (SMTP password). A malformed key
	// fails startup here rather than at first use; an empty key disables the
	// entered-secret path (references still work).
	sealer, err := secretbox.New(cfg.SecretKey)
	if err != nil {
		return err
	}
	openWT := func(dir string) (ports.ConfigRepo, error) { return git.Open(dir, "") }
	d.changes = app.NewChangeService(repo, st.Changes(), gate, builder, clock, openWT, svc)

	d.authz = api.Authz{
		BaselineViewer: cfg.ViewerGroups,
		BaselineEditor: cfg.EditorGroups,
		BaselineOwner:  cfg.OwnerGroups,
	}

	var conv ports.ConvergenceSource = noConvergence{}
	if cfg.PgDSN != "" {
		pg, err := postgres.Open(d.ctx, cfg.PgDSN)
		if err != nil {
			return err
		}
		d.cleanup = append(d.cleanup, pg.Close)
		d.inv = app.NewInventoryService(pg, pg, clock, app.DefaultTenant)
		d.tokens = app.NewTokenService(pg.Tokens(), clock, 0)
		d.devCreds = app.NewDeviceCredentials(pg.Tokens(), clock)
		d.discovery = app.NewDiscoveryService(pg.Discovered(), clock, app.DefaultTenant)
		d.imaging = app.NewImagingService(pg.ImageJobs(), clock, app.DefaultTenant)
		d.staCreds = app.NewStationCredentials(pg.Tokens(), clock)
		d.deviceSecrets = app.NewDeviceSecretsService(pg.DeviceSecrets(), sealer, clock, app.DefaultTenant)
		d.prefs = pg
		// In-app notifications need durable storage, so they light up only with
		// Postgres. The change flow then tells approvers a change is ready and
		// authors when it merges or the gate rejects it.
		d.notify = app.NewNotifyService(pg, clock, app.DefaultTenant)
		d.changes.WithNotifier(d.notify, cfg.OwnerGroups)
		d.inv.WithNotifier(d.notify, cfg.OwnerGroups)
		// Per-tenant SMTP: config lives in Postgres; the password resolves from a
		// mounted secret reference (default) or is decrypted from the sealed
		// value. A reference name maps to a file under SecretDir.
		secretDir := cfg.SecretDir
		readRef := func(name string) (string, error) {
			b, err := os.ReadFile(filepath.Join(secretDir, filepath.Base(name)))
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(b)), nil
		}
		d.mail = app.NewMailService(pg, smtpadapter.New(10*time.Second), sealer, readRef, app.DefaultTenant)
		// Compliance/incidents read the observed plane, so they light up with
		// Postgres alongside the inventory service.
		d.compliance = app.NewComplianceService(d.svc, d.inv, clock)
		// Deliver notifications by e-mail too: the seen-users directory (pg)
		// resolves a recipient or audience to addresses; ConsoleURL makes the
		// mail clickable. Delivery is best-effort and off the emitter's path.
		d.notify.WithMail(d.mail, pg, cfg.ConsoleURL)
		d.users = pg
		d.authz.Tokens = d.tokens // scoped tokens (ADR 0008); break-glass token still works
		conv = pg.NewConvergence(app.DefaultTenant, func(group string) []string {
			// Convergence is scoped to the wave's RELEASED devices: the whole
			// active group for an uncapped wave, or just the marked cohort for
			// a count-capped one (ADR 0013). Retired devices are excluded.
			return svc.Fleet().ReleasedGroupDevices(group)
		})
		d.checks.Register("postgres", pg.Ping)
	}

	// Directory browse: the login IdP (OIDC) and the group source (LDAP)
	// may differ; LDAP only ever lists groups for binding pickers.
	if cfg.LDAPURL != "" {
		dir, err := ldapdir.New(ldapdir.Config{
			URL: cfg.LDAPURL, BindDN: cfg.LDAPBindDN, BindPassword: cfg.LDAPBindPass,
			BaseDN: cfg.LDAPBaseDN, GroupFilter: cfg.LDAPGroupFilter, NameAttr: cfg.LDAPNameAttr,
		})
		if err != nil {
			return err
		}
		// Cache group listings for a minute: the groups/access pages then do
		// not dial LDAP on every load, and an unreachable directory stalls at
		// most one request per minute instead of every one.
		cached := app.NewCachedDirectory(dir, time.Minute, clock)
		d.dir = cached
		// Keep the cache hot in the background so the groups/access pages never
		// pay the LDAP dial on the first load after a TTL - the warmer refreshes
		// ahead of expiry instead of a page request eating the round-trip.
		d.background(func() { cached.WarmLoop(d.ctx) })
		log.Info("directory browse mounted", "ldap", cfg.LDAPURL, "base", cfg.LDAPBaseDN)
	}

	// The update funnel (ADR 0011): the same repo adapter moves the
	// machine-owned rings/<group> branches devices follow.
	d.rollouts = app.NewRolloutService(svc, st.Rollouts(), conv, clock, log).WithRefs(repo)
	// Build-before-promote: with a remote runner, a ring's release is realised
	// into the runner's signed binary cache before its branch moves, so devices
	// substitute the release instead of each compiling it. Opt-in via
	// --release-cache: existing deployments keep local device builds until the
	// cache (signing key + substituter in the overlay) is provisioned.
	if cfg.GateMode == "remote" && cfg.ReleaseCache {
		d.rollouts.WithCacheBuilder(gateadapter.NewRemoteBuilder(cfg.GateURL, cfg.GateToken))
		log.Info("build-before-promote enabled", "runner", cfg.GateURL)
	}
	// Guard on the concrete pointer: passing a typed-nil *NotifyService as the
	// Notifier interface would be a non-nil interface wrapping nil and panic on
	// use, so only attach when Postgres actually gave us a notifier.
	if d.notify != nil {
		d.rollouts.WithNotifier(d.notify, cfg.OwnerGroups)
	}
	d.evidence = app.NewEvidenceService(svc, d.changes, clock)
	d.background(func() { d.rollouts.Run(d.ctx, 30*time.Second) })
	d.checks.Register("config-repo", func(context.Context) error {
		_, err := repo.ReadFile(app.FleetFile)
		return err
	})
	// Join the background workers on shutdown, bounded by the grace period, so
	// an in-flight sync/rollout write finishes (or is abandoned loudly) instead
	// of being cut off. Appended last so it runs BEFORE cleanups added earlier
	// (e.g. the Postgres pool the rollout ticker uses): workers stop first.
	d.cleanup = append(d.cleanup, func() {
		done := make(chan struct{})
		go func() { d.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(cfg.ShutdownGrace):
			log.Warn("background workers did not stop within the shutdown grace")
		}
	})

	log.Info("config plane mounted", "repo", cfg.RepoDir, "write", cfg.Write,
		"gate", cfg.GateMode, "remote", redactRemote(cfg.GitRemote), "state", stateDir)
	return nil
}

// redactRemote masks userinfo embedded in a git remote URL (e.g.
// https://user:token@host/repo) before it is logged. An HTTPS push remote
// commonly carries a credential this way; the gate-runner deliberately keeps
// such credentials off the process surface via netrc, and the console's own
// logs must not be the leak. Malformed or userinfo-free input is returned
// unchanged - logging is best-effort and must never fail the caller.
func redactRemote(remote string) string {
	if remote == "" {
		return remote
	}
	u, err := url.Parse(remote)
	if err != nil || u.User == nil {
		return remote
	}
	// Rebuild the scheme/host/path around a literal "***" mask by hand:
	// url.User("***").String() percent-encodes "*" (-> %2A%2A%2A), which is
	// technically correct but unreadable in a log line - this stays a plain,
	// obviously-redacted marker.
	u.User = nil
	return u.Scheme + "://***@" + u.Host + u.RequestURI()
}

// observedCapability serves the device-facing check-in (rate limited).
func (d *deps) observedCapability() capability.Capability {
	return capability.Func{
		CapName:   "observed",
		EnabledFn: func() bool { return d.inv != nil },
		RoutesFn: func(mux *http.ServeMux) {
			inner := http.NewServeMux()
			api.NewCheckin(d.inv, d.devCreds, d.cfg.CheckinToken).
				WithLifecycle(func(tag string) bool {
					dev, ok := d.svc.Fleet().Devices[tag]
					return ok && dev.Retired()
				}).
				WithIntent(func(tag string) string {
					return d.svc.Fleet().Devices[tag].Intent
				}).Routes(inner)
			mux.Handle("POST /api/checkin", mw.RateLimit(rate.Limit(20), 40, d.cfg.TrustProxy)(inner))
		},
	}
}

// stationCapability serves the imaging-station report endpoint (rate
// limited). A station reports over the mesh, not the public internet, but the
// endpoint is authed and bounded regardless.
func (d *deps) stationCapability() capability.Capability {
	return capability.Func{
		CapName:   "station",
		EnabledFn: func() bool { return d.discovery != nil },
		RoutesFn: func(mux *http.ServeMux) {
			inner := http.NewServeMux()
			api.NewStation(d.discovery, d.imaging, d.devCreds, d.staCreds, d.cfg.CheckinToken, d.log).
				WithSecrets(d.deviceSecrets).Routes(inner)
			// Rate-limit every station route (report is high-frequency, the job
			// claim/status calls are lower but still unauthenticated-reachable),
			// sharing one limiter so a station cannot dodge the report bucket by
			// spamming the job endpoints.
			limited := mw.RateLimit(rate.Limit(5), 10, d.cfg.TrustProxy)(inner)
			mux.Handle("POST /api/station/{tag}/report", limited)
			mux.Handle("GET /api/station/{tag}/jobs", limited)
			mux.Handle("POST /api/station/{tag}/jobs/claim", limited)
			mux.Handle("POST /api/station/{tag}/jobs/{mac}/status", limited)
		},
	}
}

// authCapability mounts session auth: OIDC or the loopback dev stub.
func (d *deps) authCapability() capability.Capability {
	return capability.Func{
		CapName:   "auth",
		EnabledFn: func() bool { return d.cfg.OIDCIssuer != "" || d.cfg.DevAuth },
		RoutesFn: func(mux *http.ServeMux) {
			if d.cfg.DevAuth {
				d.log.Warn("dev auth enabled: synthetic owner session, loopback only")
				d.authz.Sessions = web.DevSessions{}
				mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "/login", http.StatusSeeOther)
				})
				return
			}
			authn, err := oidc.New(d.ctx, oidc.Config{
				Issuer:       d.cfg.OIDCIssuer,
				ClientID:     d.cfg.OIDCClientID,
				ClientSecret: d.cfg.OIDCClientSecret,
				RedirectURL:  d.cfg.OIDCRedirectURL,
				GroupsClaim:  d.cfg.OIDCGroupsClaim,
				Scopes:       d.cfg.OIDCScopes,
				SessionKey:   d.cfg.SessionKey,
				Secure:       d.cfg.SecureCookies,
				Authorize: func(u identity.User) bool {
					return d.svc.Fleet().IdentityResolver(
						d.cfg.ViewerGroups, d.cfg.EditorGroups, d.cfg.OwnerGroups).CanViewAnything(u)
				},
			})
			if err != nil {
				// Fail loudly: readiness carries the failure so the pod
				// never reports healthy without its login flow.
				d.log.Error("oidc discovery failed; session auth NOT mounted", "err", err)
				d.checks.Register("oidc", func(context.Context) error { return err })
				return
			}
			inner := http.NewServeMux()
			authn.Routes(inner)
			limited := mw.RateLimit(rate.Limit(2), 10, d.cfg.TrustProxy)(inner)
			mux.Handle("GET /login/start", limited)
			mux.Handle("GET /callback", limited)
			mux.Handle("POST /logout", limited)
			d.authz.Sessions = authn
			d.log.Info("oidc session auth mounted", "issuer", d.cfg.OIDCIssuer)
		},
	}
}

// consoleCapability mounts the human SSR console when sessions exist.
func (d *deps) consoleCapability() capability.Capability {
	return capability.Func{
		CapName:   "console",
		EnabledFn: func() bool { return d.cfg.OIDCIssuer != "" || d.cfg.DevAuth },
		RoutesFn: func(mux *http.ServeMux) {
			if d.authz.Sessions == nil {
				d.log.Error("console skipped: no session source (oidc failed?)")
				return
			}
			console, err := web.New(
				web.Services{Config: d.svc, Changes: d.changes, Rollouts: d.rollouts,
					Inventory: d.inv, Tokens: d.tokens, Prefs: d.prefs,
					DevCreds: d.devCreds, Directory: d.dir, Evidence: d.evidence,
					Discovery: d.discovery, Imaging: d.imaging, StationCreds: d.staCreds,
					DeviceSecrets: d.deviceSecrets,
					Notify:        d.notify, Mail: d.mail, Users: d.users, Compliance: d.compliance},
				d.authz.Sessions.(web.Sessions), d.cfg.Write,
				d.cfg.ViewerGroups, d.cfg.EditorGroups, d.cfg.OwnerGroups, d.log)
			if err != nil {
				d.log.Error("console templates failed; console NOT mounted", "err", err)
				return
			}
			console.SetDefaults(d.cfg.DefaultLocale, d.cfg.DefaultTimezone)
			console.Routes(mux)
		},
	}
}

// apiCapability mounts /api/v1, the machine contract.
func (d *deps) apiCapability() capability.Capability {
	return capability.Func{
		CapName: "api",
		RoutesFn: func(mux *http.ServeMux) {
			inner := http.NewServeMux()
			api.New(api.Services{Config: d.svc, Changes: d.changes,
				Rollouts: d.rollouts, Inventory: d.inv, Tokens: d.tokens,
				DevCreds: d.devCreds, Prefs: d.prefs, Directory: d.dir,
				Evidence: d.evidence},
				d.authz, d.cfg.APIToken, d.cfg.Write, d.log).Routes(inner)
			// Rate-limit the whole machine surface: a leaked token or a client
			// bug must not be able to hammer the API unbounded.
			mux.Handle("/api/v1/", mw.RateLimit(rate.Limit(20), 40, d.cfg.TrustProxy)(inner))
		},
	}
}

// builderFunc adapts a function to ports.Builder (gate mode none).
type builderFunc func(ctx context.Context, repoDir string, hosts []string) error

func (f builderFunc) Build(ctx context.Context, repoDir string, hosts []string) error {
	return f(ctx, repoDir, hosts)
}

// noConvergence reports the observed plane as not configured, so a rollout
// cannot silently advance without real convergence data.
type noConvergence struct{}

func (noConvergence) RingStatus(context.Context, string, string) (rollout.RingStatus, error) {
	return rollout.RingStatus{}, fmt.Errorf("observed plane not configured, convergence unknown: %w", ports.ErrUnavailable)
}
