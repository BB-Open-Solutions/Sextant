package web_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Console authorisation is centralised: Routes registers every handler through
// s.page or s.action, and both call authed before the handler runs. That is a
// good design and it has one failure mode - a route registered straight on the
// mux walks past it, and nothing about the code looks wrong.
//
// So this file audits the registration itself rather than any one handler. It
// is the answer to the security plan's question "which handlers never call
// requireWeb/canView": with these two tests, none can.
//
// Two halves, and both are needed. The static half proves no route BYPASSES
// the wrappers. The runtime half proves the wrappers actually REFUSE - a
// static check would happily bless s.page if page had stopped checking.

const routesFile = "web.go"

// placeholder matches a mux path parameter, {tag} or {id} or {$}.
var placeholder = regexp.MustCompile(`\{[^}]*\}`)

// publicRoutes are the only paths allowed to skip the session check, and each
// is here for a reason that has to survive being read out loud.
var publicRoutes = map[string]string{
	"GET /static/": "stylesheets and fonts; no data, and the login page needs them to render",
	"GET /login":   "the page you are sent to when you have no session",
}

// registeredRoutes parses Routes and returns every literal path it registers,
// mapped to the expression registering it.
func registeredRoutes(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, routesFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", routesFile, err)
	}

	out := map[string]string{}
	// Inside Routes, get(p, h) and post(p, h) are local helpers that wrap in
	// s.page / s.action by construction; the mux.* calls are the raw ones.
	record := func(kind string, args []ast.Expr) {
		if len(args) < 2 {
			return
		}
		lit, ok := args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		p, err := strconv.Unquote(lit.Value)
		if err != nil {
			return
		}
		// The value records HOW it was registered, which is the whole point:
		// "raw" means straight onto the mux, past the wrappers.
		how := "raw:" + render(args[1])
		if kind != "" {
			how = "wrapped"
		}
		out[normalise(kind, p)] = how
	}

	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Routes" {
			return true
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr: // mux.Handle / mux.HandleFunc
				id, ok := fn.X.(*ast.Ident)
				if !ok || id.Name != "mux" {
					return true
				}
				if fn.Sel.Name == "Handle" || fn.Sel.Name == "HandleFunc" {
					record("", call.Args)
				}
			case *ast.Ident: // the local get / post helpers
				if fn.Name == "get" || fn.Name == "post" {
					record(strings.ToUpper(fn.Name), call.Args)
				}
			}
			return true
		})
		return false
	})

	if len(out) < 40 {
		// The console has dozens of routes. A handful means the parse found
		// the wrong function or the file moved, and an audit that silently
		// checks nothing is worse than no audit.
		t.Fatalf("only %d routes parsed from %s; the audit is not looking at the route table", len(out), routesFile)
	}
	return out
}

// normalise turns a helper call into the method-prefixed form mux uses, so
// both kinds of registration compare against publicRoutes the same way.
func normalise(kind, p string) string {
	if kind == "" {
		return p // already "GET /x"
	}
	return kind + " " + p
}

func render(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.CallExpr:
		return render(v.Fun) + "(...)"
	case *ast.SelectorExpr:
		return render(v.X) + "." + v.Sel.Name
	case *ast.Ident:
		return v.Name
	case *ast.FuncLit:
		return "func literal"
	}
	return "?"
}

func TestEveryConsoleRouteGoesThroughTheAuthWrappers(t *testing.T) {
	for route, how := range registeredRoutes(t) {
		if how == "wrapped" {
			continue // registered via get()/post(), which wrap in s.page/s.action
		}
		why, public := publicRoutes[route]
		if !public {
			t.Errorf("%s is registered directly on the mux (%s), so it never reaches "+
				"s.page or s.action and is therefore unauthenticated. Register it "+
				"through get()/post(), or add it to publicRoutes with a reason.",
				route, strings.TrimPrefix(how, "raw:"))
			continue
		}
		t.Logf("public: %-14s %s", route, why)
	}

	// The allowlist must not outlive what it lists. An entry for a route that
	// no longer exists is an open door somebody can walk back through.
	routes := registeredRoutes(t)
	for route := range publicRoutes {
		if _, ok := routes[route]; !ok {
			t.Errorf("publicRoutes lists %q, which Routes no longer registers", route)
		}
	}
}

// The static half above says nothing about whether the wrappers still refuse.
// This half drives every registered route over real HTTP with a session that
// says no, and asserts the handler is never reached.
//
// New routes are picked up automatically: the list comes from the parse, not
// from a table somebody has to remember to extend.
func TestNoConsoleRouteAnswersWithoutASession(t *testing.T) {
	ts, _ := newConsoleNoSession(t)
	c := client()

	for route := range registeredRoutes(t) {
		method, path, ok := strings.Cut(route, " ")
		if !ok {
			t.Fatalf("unparsable route %q", route)
		}
		if _, public := publicRoutes[route]; public {
			continue
		}
		// Path parameters get a placeholder value. Auth runs before the
		// handler, so nothing downstream ever sees them - and if something
		// does, that is the finding.
		//
		// Any {name}, not a fixed list: a new parameter name must not quietly
		// turn into a 404 that this test would then read as "nothing served".
		// "{$}" is mux's end-of-path anchor rather than a parameter, so it is
		// dropped rather than filled in. Trimming a substituted "x" off the
		// end instead turns POST /elevation/{id} into POST /elevation, which
		// answers 405 and tests nothing.
		req := strings.ReplaceAll(path, "{$}", "")
		req = placeholder.ReplaceAllString(req, "x")

		var resp *http.Response
		var err error
		switch method {
		case "GET":
			resp, err = c.Get(ts.URL + req)
		case "POST":
			resp, err = c.PostForm(ts.URL+req, url.Values{})
		default:
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", route, err)
			continue
		}
		loc := resp.Header.Get("Location")
		resp.Body.Close()

		// Either sent to /login, or refused outright. Never a rendered page.
		switch {
		case resp.StatusCode == http.StatusFound && strings.HasPrefix(loc, "/login"):
		case resp.StatusCode == http.StatusForbidden:
		case resp.StatusCode == http.StatusUnauthorized:
		default:
			// 404 is deliberately NOT accepted. It would mean the request
			// never reached the route it was meant to test, and a test that
			// silently stops testing is the thing this file exists to prevent.
			t.Errorf("%s answered %d (Location %q) with no session; it must not",
				route, resp.StatusCode, loc)
		}
	}
}

// noSessions is a Sessions that never authenticates, which is what an
// unauthenticated visitor looks like to the console.
type noSessions struct{}

func (noSessions) SessionUser(*http.Request) (identity.User, string, bool) {
	return identity.User{}, "", false
}

// newConsoleNoSession is newConsole with a Sessions that never authenticates.
// Built here rather than by widening newConsole: a dozen tests read that
// helper and none of them are about auth.
func newConsoleNoSession(t *testing.T) (*httptest.Server, *app.ConfigService) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": seedFleet,
		"catalog.json": seedCatalog, "profiles.json": seedProfiles,
		"bundles.json": seedBundles} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.New(web.Services{Config: cfg}, noSessions{}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfg
}
