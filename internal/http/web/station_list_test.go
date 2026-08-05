package web_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// memJobs is an in-memory ports.ImageJobStore. Only ListByStation carries the
// stations list; the rest satisfy the interface.
type memJobs struct {
	byStation map[string][]imaging.Job
	err       error
}

func (m *memJobs) Upsert(context.Context, string, imaging.Job, time.Time) error { return nil }
func (m *memJobs) ListByStation(_ context.Context, tenant, station string) ([]imaging.Job, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.byStation[tenant+"|"+station], nil
}
func (m *memJobs) ListPending(context.Context, string, string) ([]imaging.Job, error) {
	return nil, nil
}
func (m *memJobs) Get(context.Context, string, string, string) (imaging.Job, bool, error) {
	return imaging.Job{}, false, nil
}
func (m *memJobs) GetActiveByTag(context.Context, string, string) (imaging.Job, bool, error) {
	return imaging.Job{}, false, nil
}
func (m *memJobs) UpdateProgress(context.Context, string, string, string, int, string, time.Time) error {
	return nil
}
func (m *memJobs) TransitionStatus(context.Context, string, string, string, imaging.Status, imaging.Status, string, time.Time) (bool, error) {
	return true, nil
}
func (m *memJobs) Delete(context.Context, string, string, string) error { return nil }

// failDisc is a discovery store whose reads fail, for the "we could not look"
// path. It must not render as zero.
type failDisc struct{}

func (failDisc) Report(context.Context, string, string, []discovery.Discovered, time.Time) error {
	return nil
}
func (failDisc) List(context.Context, string, string) ([]discovery.Discovered, error) {
	return nil, fmt.Errorf("discovery store down")
}
func (failDisc) Remove(context.Context, string, string, string) error { return nil }

const seedTwoStations = `{
  "version": 3,
  "org": {"settings": {}},
  "groups": {"pilot": {}},
  "stations": {"nuc-1": {"description": "test bench", "site": "meterkast"},
               "nuc-2": {"description": "spare"}}
}`

// newStationListConsole builds a console over two registered stations, with
// pluggable discovery and imaging planes so a test can seed state or make a
// plane fail.
func newStationListConsole(t *testing.T, disc ports.DiscoveredStore, jobs ports.ImageJobStore) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seedTwoStations), 0o644); err != nil {
		t.Fatal(err)
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
	svc := web.Services{Config: cfg, Discovery: app.NewDiscoveryService(disc, clockNow{}, "")}
	if jobs != nil {
		svc.Imaging = app.NewImagingService(jobs, clockNow{}, "")
	}
	srv, err := web.New(svc, web.DevSessions{}, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := client().Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s = %d", url, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestStationsListCountsWork: the stations page lists every registered station
// with what is waiting, what is moving, and what is stuck - the numbers that
// decide where an operator goes next. Before this the page was a dropdown: it
// named the stations and said nothing about any of them.
func TestStationsListCountsWork(t *testing.T) {
	disc := &memDisc{sets: map[string][]discovery.Discovered{}}
	disc.sets[app.DefaultTenant+"|nuc-1"] = []discovery.Discovered{
		{MAC: "aa:bb:cc:dd:ee:01", Phase: observed.Discovered, LastSeen: time.Unix(2000, 0)},
		{MAC: "aa:bb:cc:dd:ee:02", Phase: observed.Discovered, LastSeen: time.Unix(9000, 0)},
	}
	jobs := &memJobs{byStation: map[string][]imaging.Job{}}
	jobs.byStation[app.DefaultTenant+"|nuc-1"] = []imaging.Job{
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:11", Tag: "a", Status: imaging.Pending},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:12", Tag: "b", Status: imaging.Imaging},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:13", Tag: "c", Status: imaging.SBPending},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:14", Tag: "d", Status: imaging.Failed},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:15", Tag: "e", Status: imaging.Done},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:16", Tag: "f", Status: imaging.Canceled},
	}
	body := getBody(t, newStationListConsole(t, disc, jobs).URL+"/station")

	// Both stations are listed without selecting one first.
	for _, tag := range []string{"nuc-1", "nuc-2"} {
		if !strings.Contains(body, `/station?tag=`+tag) {
			t.Fatalf("station %s not listed\n%s", tag, body)
		}
	}
	// Two discovered, two in flight (pending + imaging), two waiting on a
	// person (sb-pending + failed). Done and canceled count as neither: they
	// are finished, and a finished job is not the operator's problem.
	row := stationRowHTML(t, body, "nuc-1")
	for _, want := range []string{">2<"} {
		if !strings.Contains(row, want) {
			t.Fatalf("nuc-1 row missing %q\n%s", want, row)
		}
	}
	if n := strings.Count(row, ">2<"); n != 3 {
		t.Fatalf("nuc-1 should show 2 discovered, 2 in flight, 2 needing attention; got %d cells reading 2\n%s", n, row)
	}
	// The freshest discovery is the liveness signal, not the oldest.
	if !strings.Contains(row, "1970-01-01 02:30") {
		t.Fatalf("nuc-1 last report is not the newest discovery\n%s", row)
	}
	// A station with nothing on it says so rather than showing zeroes.
	spare := stationRowHTML(t, body, "nuc-2")
	if !strings.Contains(spare, "never reported") {
		t.Fatalf("idle station does not say it never reported\n%s", spare)
	}
	if strings.Contains(spare, ">0<") {
		t.Fatalf("idle station renders literal zeroes\n%s", spare)
	}
}

// TestStationsListUnreadablePlaneIsNotZero: a store that cannot be read must
// not render as "nothing waiting". That reading is the one an operator acts
// on by walking away.
func TestStationsListUnreadablePlaneIsNotZero(t *testing.T) {
	body := getBody(t, newStationListConsole(t, failDisc{}, &memJobs{byStation: map[string][]imaging.Job{}}).URL+"/station")
	if !strings.Contains(body, "counts unavailable") {
		t.Fatalf("unreadable discovery plane not surfaced\n%s", body)
	}
	if strings.Contains(stationRowHTML(t, body, "nuc-1"), ">0<") {
		t.Fatal("unreadable plane rendered as zero")
	}
}

// TestStationsListJobPlaneFailureIsNotZero: same rule for the imaging plane -
// a station whose jobs cannot be listed must not read as idle.
func TestStationsListJobPlaneFailureIsNotZero(t *testing.T) {
	disc := &memDisc{sets: map[string][]discovery.Discovered{}}
	disc.sets[app.DefaultTenant+"|nuc-1"] = []discovery.Discovered{
		{MAC: "aa:bb:cc:dd:ee:01", Phase: observed.Discovered, LastSeen: time.Unix(2000, 0)},
	}
	body := getBody(t, newStationListConsole(t, disc, &memJobs{err: fmt.Errorf("jobs down")}).URL+"/station")
	if !strings.Contains(stationRowHTML(t, body, "nuc-1"), "counts unavailable") {
		t.Fatalf("unreadable imaging plane not surfaced\n%s", body)
	}
}

// stationRowHTML slices out one station's <tr> so an assertion cannot be
// satisfied by a number that belongs to a different station.
func stationRowHTML(t *testing.T, body, tag string) string {
	t.Helper()
	anchor := `/station?tag=` + tag + `"`
	i := strings.Index(body, anchor)
	if i < 0 {
		t.Fatalf("station %s not in page", tag)
	}
	start := strings.LastIndex(body[:i], "<tr")
	end := strings.Index(body[i:], "</tr>")
	if start < 0 || end < 0 {
		t.Fatalf("no row around station %s", tag)
	}
	return body[start : i+end]
}
