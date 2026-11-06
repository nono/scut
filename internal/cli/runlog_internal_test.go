package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
