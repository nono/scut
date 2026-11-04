// Package cache stores API lists on disk with a time to live.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// markerName is the file that holds the time of the last Clear. It is not an
// entry: keys get ".json".
const markerName = "cleared-at"

// Store is a directory of cached values.
type Store struct {
	dir       string
	ttl       time.Duration
	now       func() time.Time
	removeAll func(string) error
}

// New returns a Store in dir. An entry older than ttl, or fetched before the
// last Clear, is stale.
func New(dir string, ttl time.Duration, now func() time.Time) *Store {
	return &Store{dir: dir, ttl: ttl, now: now, removeAll: os.RemoveAll}
}

// Now returns the time on the clock of the store. A caller reads it before a
// fetch, to give it to Put.
func (s *Store) Now() time.Time { return s.now() }

type entry struct {
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

func (s *Store) path(key string) string { return filepath.Join(s.dir, key+".json") }

// Get decodes the entry of key into v. fresh is false when the entry is
// missing, unreadable, older than the TTL, or fetched before the last Clear.
// A stale entry still fills v.
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
	if cleared, ok := s.clearedAt(); ok && e.At.Before(cleared) {
		return false, nil
	}
	return s.now().Sub(e.At) < s.ttl, nil
}

// clearedAt returns the time of the last Clear, if any.
func (s *Store) clearedAt() (time.Time, bool) {
	b, err := os.ReadFile(filepath.Join(s.dir, markerName))
	if err != nil {
		return time.Time{}, false
	}
	var t time.Time
	if t.UnmarshalText(b) != nil {
		return time.Time{}, false
	}
	return t, true
}

// Put stores v, fetched at fetchedAt (see Now), under key. It writes a temp
// file, then renames it.
func (s *Store) Put(key string, v any, fetchedAt time.Time) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	b, err := json.Marshal(entry{At: fetchedAt, Data: data})
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	return s.write(key+".json", key+".*.tmp", b)
}

// write writes b to name in dir, through a temp file.
func (s *Store) write(name, pattern string, b []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	f, err := os.CreateTemp(s.dir, pattern)
	if err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("write cache: %w", err)
	}
	if err := os.Rename(f.Name(), filepath.Join(s.dir, name)); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("write cache: %w", err)
	}
	return nil
}

// Clear removes all entries, then writes the time of the clear to a marker
// file. A Put that runs after the clear, of data fetched before it, is
// stale. A Put during the removal can make it fail with ENOTEMPTY: it tries
// once more.
func (s *Store) Clear() error {
	at := s.now()
	err := s.removeAll(s.dir)
	if errors.Is(err, syscall.ENOTEMPTY) {
		err = s.removeAll(s.dir)
	}
	if err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}
	b, err := at.MarshalText()
	if err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}
	return s.write(markerName, markerName+".*.tmp", b)
}
