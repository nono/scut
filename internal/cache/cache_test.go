package cache_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nono/scut/internal/cache"
)

func TestCacheTTL(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	s := cache.New(t.TempDir(), time.Hour, func() time.Time { return now })
	if err := s.Put("k", []string{"a"}); err != nil {
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
	if err := s.Put("k", 1); err != nil {
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
	_ = s.Put("k", 1)
	ents, _ := os.ReadDir(dir)
	_ = os.WriteFile(filepath.Join(dir, ents[0].Name()), []byte("{"), 0o600)
	var v int
	if fresh, err := s.Get("k", &v); fresh || err != nil {
		t.Errorf("corrupt file must read as a miss: fresh %v, err %v", fresh, err)
	}
}
