package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The name with the file ID can be taken too: then -2 follows it.
func TestUniqueNameTwice(t *testing.T) {
	used := map[string]bool{}
	for _, tc := range []struct {
		name string
		id   int64
		want string
	}{{"a.txt", 1, "a.txt"}, {"a-2.txt", 3, "a-2.txt"}, {"a.txt", 2, "a-2-2.txt"}, {"a.txt", 2, "a-2-3.txt"}} {
		if got := uniqueName(tc.name, tc.id, used); got != tc.want {
			t.Errorf("%s (id %d): %s, want %s", tc.name, tc.id, got, tc.want)
		}
	}
}

// Without hard links (EPERM), place copies the file to a new name. It does
// not replace a file either.
func TestPlaceWithoutLink(t *testing.T) {
	old := link
	link = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.EPERM} }
	t.Cleanup(func() { link = old })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".scut-1")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := place(tmp, dir, "a.txt", 7)
	if err != nil || p != filepath.Join(dir, "a-7.txt") {
		t.Fatalf("path %q, err %v", p, err)
	}
	for name, want := range map[string]string{"a.txt": "old", "a-7.txt": "new"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q, %v, want %q", name, b, err, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "a-7.txt")); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("mode: %v, %v", fi, err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temp file left: %v", err)
	}
}
