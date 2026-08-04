package app

// verdicts.go: memoised gate verdicts, so an edit costs the configuration
// shapes it actually changes instead of every shape in the fleet.
//
// The gate already samples one device per shape (fleet.Representatives, see
// docs/architecture/scale.md), which makes a change cost shapes x ~35s rather
// than devices x ~35s. What it did not do is remember: editing one group's
// setting re-evaluated every OTHER group's shape too, every time, because
// fleet.json is part of the flake source and nix's own eval cache therefore
// misses on any fleet edit. In a fleet with a dozen shapes that is minutes of
// re-proving configurations nobody touched.
//
// The memo key is (sourceKey, globalsKey, classKey):
//
//   - sourceKey  the committed tree minus fleet.json - flake.lock, the
//     generator, hardware profiles, the catalog. A core bump or a
//     generator change moves it and drops every verdict.
//   - globalsKey the fleet fields outside the resolver's reach, chiefly
//     `rollout`, which decides a device's comin branch.
//   - classKey   the device's configuration shape: resolved settings,
//     enforcement, hardware, class, group ancestry, pins, apps.
//
// SECURITY NOTE: this narrows what the gate evaluates, so the same rule as
// the partitioner applies - it must never let an unproved configuration
// through. Three properties keep that true:
//
//  1. Only PASS is recorded. A rejection is never cached, so a failing change
//     is re-proved every time and can never be "remembered" as fine.
//  2. Every component of the key is derived from the POST-mutation document,
//     so a hit means: this exact shape, against this exact source, already
//     evaluated clean. Nothing about the previous edit is carried forward.
//  3. Both keys are built to fail toward MORE evaluation. classKey errs
//     toward more classes by construction; globalsKey is built by removing
//     the resolver's inputs rather than by listing what to include, so a
//     field added to fleet.json later lands in the key automatically instead
//     of quietly falling out of it.
//
// The cache is per-process and in-memory on purpose. It is an optimisation,
// never a source of truth: a restart, a second replica or an evicted entry
// costs one evaluation and changes no outcome. Persisting verdicts would make
// a stale one outlive the reasoning that produced it, which is the one
// failure this must not have.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// SourceKeyer is the optional ConfigRepo capability the memo needs: a
// fingerprint of the committed tree with one path left out. A repo that does
// not implement it (a test fake, an adapter written before this existed)
// simply gets no memoisation - every shape is evaluated, which is the
// behaviour this replaced.
type SourceKeyer interface {
	SourceKey(ctx context.Context, exclude string) (string, error)
}

// maxVerdicts bounds the memo. Each entry is two short strings; the cap
// exists so a long-lived console that has seen thousands of shapes cannot
// grow without limit, not because the memory matters at realistic sizes.
const maxVerdicts = 4096

// verdictCache remembers which (source, globals, shape) triples the gate has
// already accepted.
type verdictCache struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	order []string // insertion order, for eviction
}

func newVerdictCache() *verdictCache {
	return &verdictCache{seen: map[string]struct{}{}}
}

// verdictKey binds a shape to the exact source and globals it was proved
// against. Fields are length-prefixed by the separator being impossible
// inside a hex fingerprint, so no two different triples can produce one key.
func verdictKey(sourceKey, globalsKey, classKey string) string {
	sum := sha256.Sum256([]byte(sourceKey + "\x00" + globalsKey + "\x00" + classKey))
	return hex.EncodeToString(sum[:16])
}

// known reports whether this triple has already passed the gate.
func (c *verdictCache) known(key string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.seen[key]
	return ok
}

// record marks triples as proved. Called only after the gate accepted them.
func (c *verdictCache) record(keys []string) {
	if c == nil || len(keys) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		if _, dup := c.seen[k]; dup {
			continue
		}
		c.seen[k] = struct{}{}
		c.order = append(c.order, k)
	}
	for len(c.order) > maxVerdicts {
		delete(c.seen, c.order[0])
		c.order = c.order[1:]
	}
}

// partitionHosts splits a sampled host list into the hosts that still need
// evaluating and the memo keys for ALL of them, so a caller records the whole
// set once the gate accepts the remainder.
//
// A host whose class key is empty (unknown or retired device) is never
// memoised and always evaluated: an empty key would collide across every such
// host, which is the wrong direction.
//
// A repo without a SourceKey, or an error reading it, yields no keys at all -
// every host is evaluated and nothing is recorded. The memo degrades to the
// unmemoised path rather than guessing at a fingerprint.
func partitionHosts(ctx context.Context, repo any, c *verdictCache, f *fleet.Fleet, hosts []string) (todo []string, keys []string) {
	sk, ok := repo.(SourceKeyer)
	if c == nil || !ok {
		return hosts, nil
	}
	sourceKey, err := sk.SourceKey(ctx, FleetFile)
	if err != nil || sourceKey == "" {
		return hosts, nil
	}
	globalsKey := f.GeneratorGlobalsKey()
	for _, h := range hosts {
		classKey := f.ClassKeyOf(h)
		if classKey == "" {
			todo = append(todo, h)
			continue
		}
		k := verdictKey(sourceKey, globalsKey, classKey)
		keys = append(keys, k)
		if !c.known(k) {
			todo = append(todo, h)
		}
	}
	return todo, keys
}
