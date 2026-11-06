package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/nono/scut/internal/testapi"
)

func TestExecuteCancelledWritesLine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := testapi.Fake(t, map[string]http.HandlerFunc{"/": func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}})
	state := t.TempDir()
	vars := map[string]string{
		"SCUT_TOKEN": "sct_rw_example-ws_s3cret", "XDG_STATE_HOME": state,
		"XDG_CONFIG_HOME": t.TempDir(), "XDG_CACHE_HOME": t.TempDir(),
	}
	env := Env{Getenv: func(k string) string { return vars[k] }, Dir: t.TempDir(), APIBase: base}
	var out, errb bytes.Buffer
	if code := execute(ctx, []string{"team", "list"}, &out, &errb, env); code != 130 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	b, err := os.ReadFile(filepath.Join(state, "scut", "scut.log"))
	if err != nil {
		t.Fatal(err)
	}
	var l struct {
		Exit int `json:"exit"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatalf("%q: %v", b, err)
	}
	if l.Exit != 130 {
		t.Errorf("line = %s", b)
	}
}

type closedPipe struct{}

func (closedPipe) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write /dev/stdout: %w", syscall.EPIPE)
}

// A reader that closes the pipe, as head does, must not lose the line. The run
// keeps the exit code of a process that SIGPIPE stops, and prints no error.
func TestExecuteBrokenPipeWritesLine(t *testing.T) {
	state := t.TempDir()
	vars := map[string]string{"XDG_STATE_HOME": state}
	env := Env{Getenv: func(k string) string { return vars[k] }, Dir: t.TempDir()}
	var errb bytes.Buffer
	if code := execute(context.Background(), []string{"spec", "getStory"}, closedPipe{}, &errb, env); code != 141 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if errb.Len() != 0 {
		t.Errorf("stderr = %q", errb.String())
	}
	b, err := os.ReadFile(filepath.Join(state, "scut", "scut.log"))
	if err != nil {
		t.Fatal(err)
	}
	var l struct {
		Exit  int    `json:"exit"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatalf("%q: %v", b, err)
	}
	if l.Exit != 141 || !strings.Contains(l.Error, "broken pipe") {
		t.Errorf("line = %s", b)
	}
}
