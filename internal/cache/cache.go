// Package cache stores API lists on disk with a time to live.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Store is a directory of cached values.
type Store struct {
	dir string
	ttl time.Duration
	now func() time.Time
}

// New returns a Store in dir. An entry older than ttl is stale.
func New(dir string, ttl time.Duration, now func() time.Time) *Store {
	return &Store{dir: dir, ttl: ttl, now: now}
}

type entry struct {
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

func (s *Store) path(key string) string { return filepath.Join(s.dir, key+".json") }

// Get decodes the entry of key into v. fresh is false when the entry is
// missing, unreadable or older than the TTL. A stale entry still fills v.
func (s *Store) Get(key string, v any) (fresh bool, err error) {
	b, err := os.ReadFile(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read cache: %w", err)
	}
	var e entry
	if json.Unmarshal(b, &e) != nil || json.Unmarshal(e.Data, v) != nil {
		return false, nil // corrupt: treat as a miss
	}
	return s.now().Sub(e.At) < s.ttl, nil
}

// Put stores v under key. It writes a temp file, then renames it.
func (s *Store) Put(key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	b, err := json.Marshal(entry{At: s.now(), Data: data})
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	f, err := os.CreateTemp(s.dir, key+".*.tmp")
	if err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("write cache: %w", err)
	}
	if err := os.Rename(f.Name(), s.path(key)); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("write cache: %w", err)
	}
	return nil
}

// Clear removes all entries.
func (s *Store) Clear() error {
	if err := os.RemoveAll(s.dir); err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}
	return nil
}
