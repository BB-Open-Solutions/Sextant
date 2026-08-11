package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The API's authentication is centralised the way the console's is: Routes
// registers through get/rw, both wrap in a.wrap, and wrap checks the bearer
// token. Individual endpoints have spot checks for this, a handful of them,
// which is the arrangement that lets the fortieth endpoint be the one nobody
// wrote a check for.
//
// So this asks the question once, of every route at the same time. The list
// comes from a.manifest, which Routes fills as it registers, so a new
// endpoint is audited the moment it exists and nobody has to remember a
// table.
//
// The console has the same pair of tests over its own route table; see
// internal/http/web/route_auth_audit_test.go. Two surfaces, one question.

// publicAPIRoutes may answer without a token, each for a stated reason.
// Deliberately empty: today every /api/v1 route needs one, and an empty
// allowlist that is checked is stronger than a comment saying there are no
// exceptions.
var publicAPIRoutes = map[string]string{}

var apiPlaceholder = regexp.MustCompile(`\{[^}]*\}`)

// requestable turns a route pattern into something a client can send.
func requestable(path string) string {
	p := strings.ReplaceAll(path, "{$}", "")
	return apiPlaceholder.ReplaceAllString(p, "x")
}

func TestNoAPIRouteAnswersWithoutABearerToken(t *testing.T) {
	srv := newTestAPI(t, true)
	a := New(Services{}, Authz{}, testToken, true, nil)
	mux := http.NewServeMux()
	a.Routes(mux)

	routes := a.routeManifest()
	if len(routes) < 15 {
		t.Fatalf("only %d routes in the manifest; the audit is not looking at the API", len(routes))
	}

	seen := map[int]int{}
	for _, route := range routes {
		method, path, ok := strings.Cut(route, " ")
		if !ok {
			t.Fatalf("unparsable manifest entry %q", route)
		}
		if _, public := publicAPIRoutes[route]; public {
			continue
		}
		for _, tok := range []string{"", "not-the-token"} {
			code, _ := call(t, srv, method, requestable(path), nil, tok)
			seen[code]++
			if code != http.StatusUnauthorized {
				what := "no token"
				if tok != "" {
					what = "a wrong token"
				}
				t.Errorf("%s answered %d with %s; it must be 401", route, code, what)
			}
		}
	}

	// A route that 404s never reached its handler, so a run made entirely of
	// those would pass while testing nothing. 401 is the only status this
	// test should ever see, and the distribution is logged so a reader can
	// confirm that rather than take it on trust.
	t.Logf("%d routes, two attempts each, status distribution: %v", len(routes), seen)
	if seen[http.StatusUnauthorized] != len(routes)*2 {
		t.Errorf("only %d of %d attempts were 401; the rest never reached the check",
			seen[http.StatusUnauthorized], len(routes)*2)
	}
}

// The manifest only knows what get/rw registered. A handler put straight on
// the mux is invisible to it and to the test above, which is exactly the hole
// worth guarding, so this reads the source.
func TestEveryAPIRouteIsRegisteredThroughTheWrapper(t *testing.T) {
	// name -> why it is allowed to bypass get/rw.
	allowed := map[string]string{
		"/api/v1/": "the catch-all that gives an unknown path the documented error shape; it serves no data",
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "api.go", nil, 0)
	if err != nil {
		t.Fatalf("parse api.go: %v", err)
	}

	found := 0
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Routes" {
			return true
		}
		found++
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "mux" {
				return true
			}
			if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			p, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			// get/rw call mux.Handle too, but from inside their own closures,
			// where the path is a variable rather than a literal. A literal
			// here is a direct registration.
			why, ok := allowed[p]
			if !ok {
				t.Errorf("%q is registered directly on the mux in Routes, so it "+
					"never reaches a.wrap and answers without a token. Register "+
					"it through get/rw, or add it to the allowlist with a reason.", p)
				return true
			}
			t.Logf("allowed: %-12s %s", p, why)
			return true
		})
		return false
	})
	if found != 1 {
		t.Fatalf("found %d functions named Routes in api.go; expected exactly one", found)
	}
}
