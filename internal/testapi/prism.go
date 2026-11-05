package testapi

import (
	"bytes"
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
// Prism comes from node_modules (npm ci, or make test). The test skips if
// it is missing, unless CI is set: then it fails.
// Prism stops in t.Cleanup. If Prism fails to start, the error shows its output.
func Prism(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	prism := filepath.Join(root, "node_modules", ".bin", "prism")
	if _, err := os.Stat(prism); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s not found, run npm ci: the contract tests need it in CI", prism)
		}
		t.Skipf("%s not found, run npm ci", prism)
	}
	port := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, prism, "mock",
		"-h", "127.0.0.1", "-p", fmt.Sprint(port), filepath.Join(root, "api", "shortcut.openapi.json"))
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
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
			t.Fatalf("prism exited before it was ready: %v\n%s", cmd.ProcessState, out.String())
		default:
		}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
			_ = c.Close()
			return base
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	<-exited
	t.Fatalf("prism not ready after %s\n%s", prismWait, out.String())
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
