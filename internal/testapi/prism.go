package testapi

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const prismWait = 60 * time.Second

// Prism starts a Prism mock of the v4 spec and returns its base URL.
// It skips the test if npx is missing, unless CI is set: then it fails.
// Prism stops in t.Cleanup.
func Prism(t *testing.T) string {
	t.Helper()
	npx, err := exec.LookPath("npx")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("npx not found, the contract tests need it in CI")
		}
		t.Skip("npx not found")
	}
	root := repoRoot(t)
	port := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, npx, "--yes", "@stoplight/prism-cli@5", "mock",
		"-h", "127.0.0.1", "-p", fmt.Sprint(port), filepath.Join(root, "api", "shortcut.openapi.json"))
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start prism: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { cancel(); <-exited })

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(prismWait)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			t.Fatal("prism exited before it was ready")
		default:
		}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
			_ = c.Close()
			return base
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("prism not ready after %s", prismWait)
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// repoRoot walks up from the working directory to the dir that holds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}
