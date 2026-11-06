package runlog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// seed makes a file of size bytes that ends in a newline.
func seed(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if size == 0 {
		return
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteAt([]byte("\n"), size-1); err != nil {
		t.Fatal(err)
	}
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestAppendMakesDirAndModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "scut")
	path := filepath.Join(dir, "scut.log")
	if err := appendLine(path, []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("dir mode = %o", m)
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("file mode = %o", m)
	}
}

func TestAppendFixesModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scut")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scut.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendLine(path, []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if d, f := mode(t, dir), mode(t, path); d != 0o700 || f != 0o600 {
		t.Errorf("modes = %o, %o", d, f)
	}
}

// A short write on a full disk leaves a line with no newline. The next line
// must not join it.
func TestAppendAfterTornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scut.log")
	if err := os.WriteFile(path, []byte("{\"cmd\":\"st"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendLine(path, []byte("{\"cmd\":\"b\"}\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{\"cmd\":\"st\n{\"cmd\":\"b\"}\n" {
		t.Errorf("log = %q", b)
	}
}

// When the rename fails, the log stops at the hard limit.
func TestAppendHardLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scut.log")
	if err := os.Mkdir(path+".1", 0o700); err != nil {
		t.Fatal(err)
	}
	seed(t, path, hardLimit-1)
	if err := appendLine(path, []byte("a\n")); err != nil {
		t.Errorf("below the limit: %v", err)
	}
	if n := size(t, path); n != hardLimit+1 {
		t.Errorf("log has %d bytes", n)
	}
	if err := appendLine(path, []byte("a\n")); err == nil {
		t.Error("at the limit: no error")
	}
	if n := size(t, path); n != hardLimit+1 {
		t.Errorf("log has %d bytes after the limit", n)
	}
}

func TestAppendFIFODoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scut.log")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- appendLine(path, []byte("a\n")) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO: no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("appendLine blocks on a FIFO")
	}
}

func TestAppendRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scut.log")
	seed(t, path, maxFile)
	seed(t, path+".1", 10)
	if err := appendLine(path, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if n := size(t, path+".1"); n != maxFile {
		t.Errorf(".1 has %d bytes", n)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "new\n" {
		t.Errorf("log = %q, %v", b, err)
	}
}

func TestAppendBelowLimitDoesNotRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scut.log")
	seed(t, path, maxFile-1)
	if err := appendLine(path, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf(".1 exists: %v", err)
	}
	if n := size(t, path); n != maxFile-1+4 {
		t.Errorf("log has %d bytes", n)
	}
}

// Flock is per open file, so goroutines contend for the lock as processes do.
func TestAppendConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scut.log")
	seed(t, path, maxFile)
	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			if err := appendLine(path, fmt.Appendf(nil, "{\"n\":%d}\n", i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if s := size(t, path+".1"); s < maxFile {
		t.Errorf(".1 has %d bytes: a second rename replaced it", s)
	}
	var all []byte
	for _, p := range []string{path + ".1", path} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, bytes.TrimLeft(b, "\x00")...)
	}
	for i := range n {
		if want := fmt.Appendf(nil, "{\"n\":%d}\n", i); !bytes.Contains(all, want) {
			t.Errorf("line %d is missing", i)
		}
	}
}

// A run that does not get the lock does not wait and does not rename, and it
// writes its line.
func TestAppendLockHeld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scut.log")
	seed(t, path, maxFile)
	d, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := syscall.Flock(int(d.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- appendLine(path, []byte("new\n")) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = syscall.Flock(int(d.Fd()), syscall.LOCK_UN)
		t.Fatal("appendLine waits for the lock")
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf(".1 exists: %v", err)
	}
	if n := size(t, path); n != maxFile+4 {
		t.Errorf("log has %d bytes", n)
	}
}

// Another run can rotate between the first look at the size and the lock.
func TestRotateLooksAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scut.log")
	seed(t, path, 10)
	rotate(path)
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("a small log was renamed: %v", err)
	}
}

func TestAppendErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	seed(t, file, 0)
	if err := appendLine(filepath.Join(file, "scut", "scut.log"), []byte("a\n")); err == nil {
		t.Error("a path below a regular file: no error")
	}
	if err := appendLine(dir, []byte("a\n")); err == nil {
		t.Error("a path that is a directory: no error")
	}
}

func TestFinishWrites(t *testing.T) {
	state := t.TempDir()
	get := envOf(map[string]string{"XDG_STATE_HOME": state})
	Start(get, time.Now).Finish(Result{Cmd: "one"})
	Start(get, time.Now).Finish(Result{Cmd: "two"})
	b, err := os.ReadFile(filepath.Join(state, "scut", "scut.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	if len(lines) != 2 || !bytes.Contains(lines[0], []byte(`"cmd":"one"`)) || !bytes.Contains(lines[1], []byte(`"cmd":"two"`)) {
		t.Errorf("log = %q", b)
	}
}
