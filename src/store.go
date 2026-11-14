package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store keeps the State in memory behind one mutex and mirrors it to a single
// JSON file. Every change is written atomically (temp file, fsync, rename)
// before the lock is released, so concurrent requests cannot double-book a slot
// and a crash never leaves a half-written file.
type Store struct {
	mu    sync.Mutex
	path  string
	state *State
}

// PersistError means the change could not be written to disk and was rolled back.
type PersistError struct{ Err error }

func (e *PersistError) Error() string { return "persisting state: " + e.Err.Error() }
func (e *PersistError) Unwrap() error { return e.Err }

// OpenStore loads path, or seeds and writes it when the file is missing. A file
// that exists but cannot be parsed is an error: it is never overwritten silently.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, fmt.Errorf("%s is not valid state: %w", path, err)
		}
		st.normalize()
		s.state = &st
	case errors.Is(err, os.ErrNotExist):
		s.state = seedState()
		if err := saveAtomic(path, s.state); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s, nil
}

// View runs fn with read access to the state. Copy what you need before returning.
func (s *Store) View(fn func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.state)
}

// Update runs fn under the lock. fn reports whether it changed anything. On an
// error from fn, or from writing the file, the in-memory state is rolled back.
func (s *Store) Update(fn func(*State) (changed bool, err error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.state.clone()
	changed, err := fn(s.state)
	if err != nil {
		s.state = snapshot
		return err
	}
	if !changed {
		return nil
	}
	if err := saveAtomic(s.path, s.state); err != nil {
		s.state = snapshot
		return &PersistError{err}
	}
	return nil
}

// Reset re-seeds the state and file; it returns the seeded booking ids.
func (s *Store) Reset() ([]string, error) {
	var ids []string
	err := s.Update(func(st *State) (bool, error) {
		fresh := seedState()
		*st = *fresh
		ids = ids[:0]
		for id := range st.Bookings {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return true, nil
	})
	return ids, err
}

func saveAtomic(path string, st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	if d, err := os.Open(dir); err == nil { // make the rename durable; best effort
		_ = d.Sync()
		d.Close()
	}
	return nil
}
