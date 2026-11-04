package cache_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nono/scut/internal/cache"
)

func TestCacheTTL(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	s := cache.New(t.TempDir(), time.Hour, func() time.Time { return now })
	if err := s.Put("k", []string{"a"}, now); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		after time.Duration
		fresh bool
	}{{59 * time.Minute, true}, {61 * time.Minute, false}} {
		now = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC).Add(c.after)
		var got []string
		fresh, err := s.Get("k", &got)
		if err != nil || fresh != c.fresh || len(got) != 1 {
			t.Errorf("after %v: fresh %v, got %v, err %v", c.after, fresh, got, err)
		}
	}
}

func TestCacheMissing(t *testing.T) {
	s := cache.New(filepath.Join(t.TempDir(), "sub"), time.Hour, time.Now)
	var v int
	if fresh, err := s.Get("none", &v); fresh || err != nil {
		t.Errorf("fresh %v, err %v", fresh, err)
	}
}

func TestCacheClearAndNoTemp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	s := cache.New(dir, time.Hour, time.Now)
	if err := s.Put("k", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("want one file, got %d", len(ents))
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	var v int
	if fresh, _ := s.Get("k", &v); fresh {
		t.Error("fresh after Clear")
	}
	if err := s.Clear(); err != nil {
		t.Errorf("second Clear: %v", err)
	}
}

func TestCacheCorrupt(t *testing.T) {
	dir := t.TempDir()
	s := cache.New(dir, time.Hour, time.Now)
	_ = s.Put("k", 1, time.Now())
	ents, _ := os.ReadDir(dir)
	_ = os.WriteFile(filepath.Join(dir, ents[0].Name()), []byte("{"), 0o600)
	var v int
	if fresh, err := s.Get("k", &v); fresh || err != nil {
		t.Errorf("corrupt file must read as a miss: fresh %v, err %v", fresh, err)
	}
}

// A Put after a Clear, of data fetched before it, must not bring back the
// old data as fresh.
func TestCachePutAfterClear(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	s := cache.New(dir, time.Hour, func() time.Time { return now })
	fetched := now
	now = now.Add(time.Second)
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := s.Put("k", 1, fetched); err != nil {
		t.Fatal(err)
	}
	var v int
	if fresh, err := s.Get("k", &v); fresh || err != nil || v != 1 {
		t.Errorf("data fetched before the clear: fresh %v, v %d, err %v", fresh, v, err)
	}
	if err := s.Put("k", 2, now); err != nil {
		t.Fatal(err)
	}
	if fresh, err := s.Get("k", &v); !fresh || err != nil || v != 2 {
		t.Errorf("data fetched after the clear: fresh %v, v %d, err %v", fresh, v, err)
	}
	// The marker stays: a second clear does not remove it.
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 || ents[0].Name() != "cleared-at" {
		t.Errorf("dir after clear: %v", ents)
	}
}

// A file that a concurrent Put writes during the removal makes RemoveAll
// fail with ENOTEMPTY: Clear tries once more.
func TestCacheClearNotEmpty(t *testing.T) {
	notEmpty := &fs.PathError{Op: "unlinkat", Path: "x", Err: syscall.ENOTEMPTY}
	for fails, ok := range map[int]bool{1: true, 2: false} {
		s := cache.New(t.TempDir(), time.Hour, time.Now)
		n := 0
		cache.SetRemoveAll(s, func(p string) error {
			n++
			if n <= fails {
				return notEmpty
			}
			return os.RemoveAll(p)
		})
		err := s.Clear()
		if ok != (err == nil) || (!ok && !errors.Is(err, syscall.ENOTEMPTY)) {
			t.Errorf("%d failures: err %v", fails, err)
		}
	}
}
