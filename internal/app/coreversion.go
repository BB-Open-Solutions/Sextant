package app

import (
	"encoding/json"
	"fmt"
	"time"
)

// coreversion.go: the DAWO core version an overlay is pinned to. Config
// changes are a status ("all devices current"), but the core image lineage is
// a real version an operator reasons about - it differs per ring and it is the
// thing "which DAWO are we on?" asks about. The overlay pins it as a flake
// input, so flake.lock is the authority.

// FlakeLockFile is the lock file at the overlay root; coreInput is the flake
// input naming the core repo. An overlay without that input is legal (it is
// then not a DAWO overlay in the version sense), so every read is optional.
const (
	FlakeLockFile = "flake.lock"
	coreInput     = "dawo"
)

// CoreVersion is the pinned core revision: what the overlay builds against.
// URL is the input's source (display only, may be empty); Modified is the
// pin's upstream commit time (zero when the lock omits it).
type CoreVersion struct {
	Rev      string
	URL      string
	Modified time.Time
}

// coreEntry memoises one flake.lock read against the snapshot it was read
// with, so a render does not re-read the file. Same idea as relCache, keyed by
// the snapshot pointer rather than a revision: flake.lock is a working-tree
// file, so it can only change when the tree does, and every tree change
// publishes a new snapshot.
type coreEntry struct {
	snap *configSnapshot
	core CoreVersion
	ok   bool
}

// CoreVersion returns the core revision the overlay is pinned to. ok is false
// when the overlay has no readable dawo pin - a missing flake.lock, a missing
// node or a missing rev are all ordinary states, and the caller simply omits
// the version instead of reporting an error.
func (s *ConfigService) CoreVersion() (CoreVersion, bool) {
	snap := s.snap.Load()
	if e := s.coreCache.Load(); e != nil && e.snap == snap {
		return e.core, e.ok
	}
	raw, err := s.repo.ReadFile(FlakeLockFile)
	var core CoreVersion
	ok := false
	if err == nil {
		core, ok = parseFlakeLock(raw)
	}
	s.coreCache.Store(&coreEntry{snap: snap, core: core, ok: ok})
	return core, ok
}

// flakeLock is the subset of the (documented, versioned) lock format this
// reader needs. Inputs values are raw because an input maps either to a node
// name or to a follows path (an array); only the plain name resolves here.
type flakeLock struct {
	Root  string `json:"root"`
	Nodes map[string]struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
		Locked struct {
			Rev          string `json:"rev"`
			LastModified int64  `json:"lastModified"`
			URL          string `json:"url"`
			Type         string `json:"type"`
			Owner        string `json:"owner"`
			Repo         string `json:"repo"`
		} `json:"locked"`
	} `json:"nodes"`
}

// parseFlakeLock extracts the core pin from a flake.lock. It never errors:
// malformed or unrelated lock files simply carry no core version.
func parseFlakeLock(raw []byte) (CoreVersion, bool) {
	var doc flakeLock
	if err := json.Unmarshal(raw, &doc); err != nil {
		return CoreVersion{}, false
	}
	node, ok := doc.Nodes[coreNodeName(doc)]
	if !ok || node.Locked.Rev == "" {
		return CoreVersion{}, false
	}
	cv := CoreVersion{Rev: node.Locked.Rev, URL: node.Locked.URL}
	if cv.URL == "" && node.Locked.Owner != "" && node.Locked.Repo != "" {
		// The flake-ref shorthand nix itself prints for a fetcher input.
		cv.URL = fmt.Sprintf("%s:%s/%s", node.Locked.Type, node.Locked.Owner, node.Locked.Repo)
	}
	if node.Locked.LastModified > 0 {
		cv.Modified = time.Unix(node.Locked.LastModified, 0).UTC()
	}
	return cv, true
}

// coreNodeName resolves which node holds the core input. The root node maps
// input names to node names, which disambiguates a lock where the name is
// taken (dawo_2); without that mapping the input name is the node name.
func coreNodeName(doc flakeLock) string {
	root := doc.Root
	if root == "" {
		root = "root"
	}
	var name string
	if err := json.Unmarshal(doc.Nodes[root].Inputs[coreInput], &name); err == nil && name != "" {
		return name
	}
	return coreInput
}
