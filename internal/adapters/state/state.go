// Package state is the Tier-0 durable store for control-plane state that
// does not belong in the config repo: change requests and the rollout run.
// One JSON file per record under a state directory, all access serialized
// by a mutex - correct for a single process. The Postgres adapter replaces
// this for HA deployments; both sit behind the same ports.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

// Store is a file-backed state directory.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open ensures the state directory exists and returns the store.
func Open(dir string) (*Store, error) {
	// Owner-only: this is server-private control-plane state (change requests,
	// rollout runs), not world-readable content.
	if err := os.MkdirAll(filepath.Join(dir, "changes"), 0o700); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) readJSON(name string, v any) (bool, error) {
	// #nosec G304 - name is a fixed literal or a change ID validated by change.ValidID before this call; it stays inside the private state dir.
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

// writeJSON writes atomically (tmp + rename) so a crash mid-write never
// leaves a torn record.
func (s *Store) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- ports.ChangeStore ---

// Changes exposes the change-request store.
func (s *Store) Changes() *ChangeStore { return &ChangeStore{s} }

// ChangeStore implements ports.ChangeStore on the state directory.
type ChangeStore struct{ s *Store }

func changeFile(id string) (string, error) {
	if err := change.ValidID(id); err != nil {
		return "", err
	}
	return filepath.Join("changes", id+".json"), nil
}

// Put implements ports.ChangeStore.
func (c *ChangeStore) Put(_ context.Context, cr change.CR) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	name, err := changeFile(cr.ID)
	if err != nil {
		return err
	}
	return c.s.writeJSON(name, cr)
}

// Get implements ports.ChangeStore.
func (c *ChangeStore) Get(_ context.Context, id string) (change.CR, bool, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	name, err := changeFile(id)
	if err != nil {
		return change.CR{}, false, err
	}
	var cr change.CR
	ok, err := c.s.readJSON(name, &cr)
	return cr, ok, err
}

// List implements ports.ChangeStore (newest first).
func (c *ChangeStore) List(_ context.Context) ([]change.CR, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(c.s.dir, "changes"))
	if err != nil {
		return nil, err
	}
	var out []change.CR
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var cr change.CR
		if _, err := c.s.readJSON(filepath.Join("changes", e.Name()), &cr); err != nil {
			return nil, fmt.Errorf("corrupt change record %s: %w", e.Name(), err)
		}
		out = append(out, cr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// --- ports.RolloutStore ---

// Rollouts exposes the rollout-state store.
func (s *Store) Rollouts() *RolloutStore { return &RolloutStore{s} }

// RolloutStore implements ports.RolloutStore on the state directory.
type RolloutStore struct{ s *Store }

// Get implements ports.RolloutStore; nil state means no run.
func (r *RolloutStore) Get(_ context.Context) (*rollout.State, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var st rollout.State
	ok, err := r.s.readJSON("rollout.json", &st)
	if err != nil || !ok {
		return nil, err
	}
	return &st, nil
}

// Put implements ports.RolloutStore.
func (r *RolloutStore) Put(_ context.Context, st *rollout.State) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.s.writeJSON("rollout.json", st)
}
