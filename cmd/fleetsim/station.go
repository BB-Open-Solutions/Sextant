package main

// station.go simulates an imaging station: the machine on the workbench that
// watches a PXE network, claims the image jobs an operator dispatched, and
// walks each one through the install and the firmware ceremonies.
//
// It exists because the imaging path is the part of Sextant a demo cannot
// show without hardware, and it is precisely the part worth showing: a laptop
// out of its box appears on the network by itself, somebody clicks enrol, and
// it provisions. Everything else in a fleet console demos fine from a
// database. This does not.
//
// It speaks the same public station API a real station does
// (POST report, claim, status) with the same bridge token, so it exercises the
// real endpoints rather than a mock of them. What it cannot prove is the half
// that touches hardware: nixos-anywhere really installing, and a person really
// toggling Secure Boot in the firmware.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// vendors are the machines a Dutch municipality actually has on a workbench,
// so the demo does not read as lorem ipsum.
var stationHardware = []struct {
	vendor, model, cpu, hardware string
	cores, memGB, diskGB         int
}{
	{"Lenovo", "ThinkPad T495s", "AMD Ryzen 5 PRO 3500U", "lenovo-t495s", 8, 16, 512},
	{"Lenovo", "ThinkPad T14 Gen 4", "AMD Ryzen 7 PRO 7840U", "lenovo-t14", 16, 32, 1024},
	{"Dell", "Latitude 5540", "Intel Core i5-1345U", "dell-latitude-5540", 12, 16, 512},
	{"HP", "EliteBook 840 G10", "Intel Core i7-1355U", "hp-elitebook-840", 12, 32, 512},
	{"Intel", "NUC 13 Pro", "Intel Core i5-1340P", "intel-nuc13", 16, 32, 256},
}

// machine is one box on the station's PXE network.
type machine struct {
	MAC      string
	Serial   string
	Vendor   string
	Model    string
	CPU      string
	Cores    int
	MemGB    int
	DiskGB   int
	Firmware string
	Phase    string
	// job is non-nil once an operator dispatched an image job for this MAC and
	// the station claimed it.
	job *jobRun
}

// jobRun is the station's progress through one image job. Real installs move
// in minutes; the simulator moves in beats so a demo is not a coffee break.
type jobRun struct {
	Tag      string
	Status   string
	beatsIn  int
	failAt   int // beat at which this job fails, or -1
	finished bool
}

type stationSim struct {
	url, token, tag string
	creds           *credStore
	client          *http.Client
	rng             *rand.Rand

	mu       sync.Mutex
	machines map[string]*machine
	beat     int

	// pool is how many machines wait on the PXE network. The station adds a
	// fresh one whenever an install finishes, so a demo never runs out of
	// something to enrol and never grows unboundedly either.
	pool    int
	pctFail int
	// secureBoot walks the firmware ceremony (sb-pending, sb-enrolled) before
	// TPM2 sealing. Off shows the shorter path a device without Secure Boot
	// takes, which is also worth being able to demo.
	secureBoot bool
}

func newStationSim(url, token, tag string, pool, pctFail int, secureBoot bool, creds *credStore) *stationSim {
	s := &stationSim{
		url: url, token: token, tag: tag, creds: creds,
		client: &http.Client{Timeout: 10 * time.Second},
		// Seeded from the station tag so a restart invents the same hardware
		// rather than a fresh set of strangers, which matters when a demo is
		// interrupted and picked up again. math/rand is right here and gosec
		// is right in general: nothing this produces is a secret. MACs,
		// serials and a fake host key for machines that do not exist.
		rng:      rand.New(rand.NewSource(int64(hash(tag)))), //nolint:gosec // G404: invented hardware, not key material
		machines: map[string]*machine{},
		pool:     pool, pctFail: pctFail, secureBoot: secureBoot,
	}
	for i := 0; i < pool; i++ {
		m := s.newMachine()
		s.machines[m.MAC] = m
	}
	return s
}

// newMachine invents a box. The MAC is locally administered (02:...), which is
// the correct range for something that does not exist.
func (s *stationSim) newMachine() *machine {
	h := stationHardware[s.rng.Intn(len(stationHardware))]
	mac := fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x",
		s.rng.Intn(256), s.rng.Intn(256), s.rng.Intn(256), s.rng.Intn(256), s.rng.Intn(256))
	return &machine{
		MAC: mac, Serial: fmt.Sprintf("PF%07d", s.rng.Intn(10000000)),
		Vendor: h.vendor, Model: h.model, CPU: h.cpu,
		Cores: h.cores, MemGB: h.memGB, DiskGB: h.diskGB,
		Firmware: "UEFI, Secure Boot off (setup mode)",
		Phase:    "discovered",
	}
}

// tick is one beat: report what is on the network, claim what an operator
// dispatched, and move every claimed job one step.
func (s *stationSim) tick(ctx context.Context) {
	s.mu.Lock()
	s.beat++
	s.mu.Unlock()
	s.report(ctx)
	s.claim(ctx)
	s.advance(ctx)
}

func (s *stationSim) report(ctx context.Context) {
	s.mu.Lock()
	devices := make([]map[string]any, 0, len(s.machines))
	for _, m := range s.machines {
		devices = append(devices, map[string]any{
			"mac": m.MAC, "serial": m.Serial, "vendor": m.Vendor, "model": m.Model,
			"cpu": m.CPU, "cores": m.Cores, "memGB": m.MemGB, "diskGB": m.DiskGB,
			"firmware": m.Firmware, "phase": m.Phase, "lastSeen": time.Now().UTC(),
		})
	}
	s.mu.Unlock()
	// A report REPLACES the station's whole set, so an empty one is a real
	// statement ("nothing on the network") and not a no-op worth skipping.
	s.post(ctx, "/report", map[string]any{"devices": devices}, nil)
}

// claim takes whatever the operator dispatched. The console answers with the
// job plus a one-time device credential, which a real station bakes into the
// image.
//
// The simulator used to discard that credential, on the reasoning that there
// is no image to bake it into. That was wrong in a way that only showed up
// later: issuing a credential is what makes the console stop accepting the
// shared bridge token for that device. A simulated device that had been
// imaged could therefore never check in again, and the demo carried three of
// them as never seen. Keeping it is also the more truthful simulation, since
// it is the posture a real fleet has.
func (s *stationSim) claim(ctx context.Context) {
	var jobs []struct {
		MAC, Tag, Hardware, Status, Credential string
	}
	if !s.post(ctx, "/jobs/claim", map[string]any{}, &jobs) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range jobs {
		m := s.machines[j.MAC]
		if m == nil || m.job != nil {
			continue
		}
		failAt := -1
		if s.pctFail > 0 && s.rng.Intn(100) < s.pctFail {
			failAt = 2 + s.rng.Intn(2)
		}
		m.job = &jobRun{Tag: j.Tag, Status: "imaging", failAt: failAt}
		m.Phase = "installing"
		// Kept, not baked: from here the device speaks for itself.
		s.creds.put(j.Tag, j.Credential)
		log.Printf("station %s: claimed %s for %s", s.tag, j.MAC, j.Tag)
	}
}

// next is the state machine the station drives. It mirrors
// imaging.Status.CanTransition; a step the console would refuse is a bug here,
// not a thing to work around.
func (s *stationSim) next(cur string) string {
	switch cur {
	case "imaging":
		return "installed"
	case "installed":
		if s.secureBoot {
			return "sb-pending"
		}
		return "tpm2-enrolled"
	case "sb-pending":
		return "sb-enrolled"
	case "sb-enrolled":
		return "tpm2-enrolled"
	case "tpm2-enrolled":
		return "done"
	}
	return ""
}

func (s *stationSim) advance(ctx context.Context) {
	type step struct {
		mac, status, message, hostKey string
		progress                      int
	}
	var steps []step
	s.mu.Lock()
	for mac, m := range s.machines {
		j := m.job
		if j == nil || j.finished {
			continue
		}
		j.beatsIn++
		// Installing takes longer than the ceremonies, which is true of the
		// real thing: copying a closure is minutes, enrolling keys is seconds.
		hold := 1
		if j.Status == "imaging" {
			hold = 3
		}
		if j.beatsIn < hold {
			continue
		}
		j.beatsIn = 0
		if j.failAt >= 0 {
			j.failAt--
			if j.failAt < 0 {
				j.Status, j.finished = "failed", true
				m.Phase = "discovered"
				steps = append(steps, step{mac: mac, status: "failed",
					message: "simulated: disk /dev/nvme0n1 reports a bad sector during partitioning"})
				continue
			}
		}
		nx := s.next(j.Status)
		if nx == "" {
			continue
		}
		st := step{mac: mac, status: nx, progress: 100}
		if nx == "installed" {
			// A real station reports the host key it pre-seeded, and the
			// console records it against the asset. Shape matters more than
			// the bytes: this is what makes a secret encryptable for a device
			// that did not exist five minutes ago.
			st.hostKey = fmt.Sprintf("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI%s simulated@%s", randB64(s.rng, 27), m.Serial)
		}
		j.Status = nx
		switch nx {
		case "installed":
			m.Phase = "installed"
		case "done":
			j.finished = true
		}
		steps = append(steps, st)
	}
	s.mu.Unlock()

	for _, st := range steps {
		body := map[string]any{"status": st.status, "progress": st.progress}
		if st.message != "" {
			body["message"] = st.message
		}
		if st.hostKey != "" {
			body["hostKey"] = st.hostKey
		}
		if st.status == "imaging" || st.status == "installed" {
			body["step"] = "nixos-anywhere"
		}
		s.post(ctx, "/jobs/"+st.mac+"/status", body, nil)
		log.Printf("station %s: %s -> %s", s.tag, st.mac, st.status)
	}

	// A finished machine leaves the workbench and a fresh box takes its place,
	// so the demo always has something to enrol and the pool stays put.
	s.mu.Lock()
	for mac, m := range s.machines {
		if m.job != nil && m.job.finished && m.job.Status == "done" {
			delete(s.machines, mac)
			n := s.newMachine()
			s.machines[n.MAC] = n
		}
	}
	s.mu.Unlock()
}

// post calls one station endpoint. out, when non-nil, receives the decoded
// response body.
func (s *stationSim) post(ctx context.Context, path string, body any, out any) bool {
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.url+"/api/station/"+s.tag+path, bytes.NewReader(buf))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("station %s%s: %v", s.tag, path, err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		log.Printf("station %s%s: %d", s.tag, path, resp.StatusCode)
		return false
	}
	if out == nil {
		return true
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		log.Printf("station %s%s: bad response: %v", s.tag, path, err)
		return false
	}
	return true
}

const b64alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func randB64(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = b64alphabet[r.Intn(len(b64alphabet))]
	}
	return string(b)
}
