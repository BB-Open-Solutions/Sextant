// Command fleetsim simulates a fleet of devices against a Sextant console:
// N fake agents check in on a beat, follow their group's rings/<group>
// branch like comin would, and exhibit a configurable mix of behaviours
// (fast, slow, offline, erroring). It exists to exercise the updates flow -
// waves, thresholds, soak, stragglers - with realistic numbers long before
// the real fleet has them. Test tooling only: it speaks the public check-in
// API and needs nothing but the shared check-in token.
//
// With -gen N it instead writes a demo fleet.json (groups + devices) to
// stdout, so a demo environment seeds and simulates from one source.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// demoGroups shapes the generated fleet: a small IT test group plus
// production groups of plausible sizes (150 devices total by default).
var demoGroups = []struct {
	Name string
	N    int
}{
	{"ict-test", 3}, {"kantoor-a", 25}, {"kantoor-b", 40},
	{"balie", 30}, {"depot", 22}, {"zaanstad", 30},
}

type fleetDoc struct {
	Version  int                     `json:"version"`
	Org      map[string]any          `json:"org"`
	Groups   map[string]struct{}     `json:"groups"`
	Devices  map[string]fleetDevice  `json:"devices"`
	Rollout  *fleetRollout           `json:"rollout,omitempty"`
	Stations map[string]fleetStation `json:"stations,omitempty"`
}

// fleetStation registers the imaging station the simulator plays. Without it
// the console answers "unknown station" and shows nothing of what the station
// reported - the reports are accepted and stored, they just have no page.
// Generating it here means the demo has a working imaging line from the first
// second rather than after a manual registration nobody knew to do.
type fleetStation struct {
	Description string `json:"description,omitempty"`
	Site        string `json:"site,omitempty"`
}

// fleetRollout is the generated wave plan. Without one the demo has devices
// and no ladder, and the ladder is the part that is hard to explain in words:
// a wave promotes on a MEASURED share of its devices being healthy on the
// target, not on a timer.
type fleetRollout struct {
	Rings []fleetRing `json:"rings"`
}

type fleetRing struct {
	Group             string `json:"group"`
	Name              string `json:"name,omitempty"`
	SoakMinutes       int    `json:"soakMinutes,omitempty"`
	MinHealthyPercent int    `json:"minHealthyPercent,omitempty"`
	RequireApproval   bool   `json:"requireApproval,omitempty"`
}

type fleetDevice struct {
	Groups   []string `json:"groups,omitempty"`
	Hardware string   `json:"hardware,omitempty"`
}

func main() {
	var (
		gen      = flag.Int("gen", 0, "generate a demo fleet.json with this many devices (0 = simulate)")
		url      = flag.String("url", "http://127.0.0.1:8080", "console base URL")
		token    = flag.String("token", "", "shared check-in token (SEXTANT_CHECKIN_TOKEN)")
		repo     = flag.String("repo", "", "overlay git repo (path or URL) whose rings/<group> branches devices follow")
		fleet    = flag.String("fleet", "fleet.json", "fleet document naming the devices to simulate")
		interval = flag.Duration("interval", 10*time.Second, "beat interval per device")
		pctSlow  = flag.Int("slow", 15, "percent of devices that converge slowly (3-6 beats late)")
		pctOff   = flag.Int("offline", 5, "percent of devices that go silent mid-run")
		pctErr   = flag.Int("error", 3, "percent of devices that report a deploy error")
		station  = flag.String("station", "", "also simulate this imaging station (tag); empty disables it")
		staPool  = flag.Int("station-pool", 3, "machines waiting on the station's PXE network")
		staFail  = flag.Int("station-fail", 10, "percent of image jobs that fail once, so a retry can be shown")
		staSB    = flag.Bool("station-secureboot", true, "walk the Secure Boot ceremony before TPM2 sealing")
	)
	flag.Parse()

	if *gen > 0 {
		if err := writeDemoFleet(os.Stdout, *gen); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *token == "" || *repo == "" {
		log.Fatal("simulate needs -token and -repo (or use -gen N to emit a demo fleet.json)")
	}
	devices, err := loadDevices(*fleet)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("simulating %d devices against %s (beat %s)", len(devices), *url, *interval)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sim := &simulator{
		url: *url, token: *token, repo: *repo,
		pctSlow: *pctSlow, pctOff: *pctOff, pctErr: *pctErr,
		client: &http.Client{Timeout: 10 * time.Second},
		state:  map[string]*devState{},
	}
	// Spread the beats: real devices are not phase-locked, and 150 posts in
	// one burst from one IP trips the console's rate limiter (429s that a
	// real fleet, one IP per device, would never see). Each device gets a
	// deterministic offset within the interval.
	sim.spread = *interval
	// The imaging station beats on its own clock and its own goroutine: it
	// talks to different endpoints, and a station that stalled while 150
	// devices checked in would be the one thing on screen not moving.
	if *station != "" {
		sta := newStationSim(*url, *token, *station, *staPool, *staFail, *staSB)
		log.Printf("simulating imaging station %s with %d machines on its network", *station, *staPool)
		go func() {
			t := time.NewTicker(*interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					sta.tick(ctx)
				}
			}
		}()
	}
	t := time.NewTicker(*interval / 10)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			sim.slice(ctx, devices, now, *interval)
		}
	}
}

// device is one simulated agent: a tag following one group's ring branch.
type device struct {
	Tag   string
	Group string
}

type devState struct {
	// pendingRev is the branch revision the device has seen but not yet
	// adopted; adoptAt is the beat number it switches (download+build time).
	running    string
	pendingRev string
	adoptAt    int
}

type simulator struct {
	url, token, repo        string
	pctSlow, pctOff, pctErr int
	client                  *http.Client

	beatN    int
	sliceN   int
	spread   time.Duration
	branches map[string]string
	mu       sync.Mutex
	state    map[string]*devState
}

// slice runs one tenth of the beat interval: the devices whose offset falls
// in this slice check in now, so load spreads evenly instead of bursting.
func (s *simulator) slice(ctx context.Context, devices []device, now time.Time, interval time.Duration) {
	s.sliceN++
	phase := s.sliceN % 10
	if phase == 0 {
		s.beatN++
		var err error
		if s.branches, err = s.ringRevs(ctx); err != nil {
			log.Printf("ls-remote: %v", err)
			return
		}
		if s.beatN > 1 {
			log.Printf("beat %d done", s.beatN-1)
		}
	}
	branches := s.branches
	if branches == nil {
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, d := range devices {
		if int(hash(d.Tag+"o")%10) != phase {
			continue // not this device's slice
		}
		if behaviour(d.Tag, s.pctOff) && s.beatN > 3 {
			continue // gone silent mid-run
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(d device) {
			defer wg.Done()
			defer func() { <-sem }()
			s.beatOne(ctx, d, branches)
		}(d)
	}
	wg.Wait()
	_ = now
	_ = interval
}

func (s *simulator) beatOne(ctx context.Context, d device, branches map[string]string) {
	target := branches["rings/"+d.Group]
	if target == "" {
		target = branches["main"]
	}
	s.mu.Lock()
	st := s.state[d.Tag]
	if st == nil {
		st = &devState{running: target} // enrolled on whatever its branch holds
		s.state[d.Tag] = st
	}
	if target != "" && target != st.running && target != st.pendingRev {
		lag := 1 + int(hash(d.Tag)%3) // ordinary: 1-3 beats of download+switch
		if behaviour(d.Tag, s.pctSlow) {
			lag = 3 + int(hash(d.Tag)%4) // slow link / big closure
		}
		st.pendingRev, st.adoptAt = target, s.beatN+lag
	}
	if st.pendingRev != "" && s.beatN >= st.adoptAt {
		st.running, st.pendingRev = st.pendingRev, ""
	}
	running := st.running
	s.mu.Unlock()

	body := map[string]any{"tag": d.Tag, "revision": running, "phase": "running"}
	if behaviour(d.Tag, s.pctErr) && s.beatN%7 == 0 {
		body["error"] = "simulated: nix-store --verify failed on /nix/store/…"
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url+"/api/checkin", bytes.NewReader(buf))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("%s: %v", d.Tag, err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("%s: check-in %d", d.Tag, resp.StatusCode)
	}
}

// ringRevs lists every branch head of the overlay repo in one call.
func (s *simulator) ringRevs(ctx context.Context) (map[string]string, error) {
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "--heads", s.repo).Output()
	if err != nil {
		return nil, err
	}
	revs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			revs[strings.TrimPrefix(parts[1], "refs/heads/")] = parts[0]
		}
	}
	return revs, nil
}

// behaviour deterministically assigns a trait to pct% of devices: the same
// tag always behaves the same way across runs, which makes a demo scriptable.
func behaviour(tag string, pct int) bool {
	return int(hash(tag+"b")%100) < pct
}

func hash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func loadDevices(path string) ([]device, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Devices map[string]fleetDevice `json:"devices"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make([]device, 0, len(doc.Devices))
	for tag, d := range doc.Devices {
		g := ""
		if len(d.Groups) > 0 {
			g = d.Groups[0]
		}
		out = append(out, device{Tag: tag, Group: g})
	}
	return out, nil
}

// writeDemoFleet emits a fleet.json with n devices spread over the demo
// groups proportionally.
func writeDemoFleet(w *os.File, n int) error {
	doc := fleetDoc{
		Version: 3,
		Org:     map[string]any{"settings": map[string]any{}},
		Groups:  map[string]struct{}{},
		Devices: map[string]fleetDevice{},
		// Three waves, deliberately not identical: a strict test group that
		// must be whole, a first office that may leave stragglers behind, and
		// the rest of the fleet behind a manual sign-off. Between them they
		// show every knob the plan has.
		Stations: map[string]fleetStation{
			"st-1": {Description: "Simulated imaging line", Site: "demo"},
		},
		Rollout: &fleetRollout{Rings: []fleetRing{
			{Group: "ict-test", Name: "Test", SoakMinutes: 10, MinHealthyPercent: 100},
			{Group: "kantoor-a", Name: "First office", SoakMinutes: 30},
			{Group: "kantoor-b", Name: "The rest", SoakMinutes: 30, RequireApproval: true},
		}},
	}
	base := 0
	for _, g := range demoGroups {
		base += g.N
	}
	// #nosec G404 - demo data generation, deliberately deterministic; no security use.
	r := rand.New(rand.NewSource(42))
	hw := []string{"lenovo-t495s", "hp-elitebook-840", "dell-latitude-5440"}
	for _, g := range demoGroups {
		doc.Groups[g.Name] = struct{}{}
		count := g.N * n / base
		if count == 0 {
			count = 1
		}
		for i := 1; i <= count; i++ {
			tag := fmt.Sprintf("%s-%03d", g.Name, i)
			doc.Devices[tag] = fleetDevice{
				Groups:   []string{g.Name},
				Hardware: hw[r.Intn(len(hw))],
			}
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
