package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/testapi"
)

const token = "sct_rw_example-ws_s3cret"

type fakeKeyring struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *fakeKeyring) Get(service, user string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[service+"/"+user], nil
}

func (k *fakeKeyring) Set(service, user, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[service+"/"+user] = secret
	return nil
}

type harness struct {
	env cli.Env
	kr  *fakeKeyring
}

// newHarness returns an Env with a fake API, an empty keyring and no real
// config or cache dir. vars override the environment.
func newHarness(t *testing.T, routes map[string]http.HandlerFunc, stdin string, vars map[string]string) *harness {
	t.Helper()
	dir := t.TempDir()
	m := map[string]string{"HOME": dir, "XDG_CONFIG_HOME": dir + "/cfg", "XDG_CACHE_HOME": dir + "/cache"}
	for k, v := range vars {
		m[k] = v
	}
	kr := &fakeKeyring{m: map[string]string{}}
	h := &harness{kr: kr}
	h.env = cli.Env{
		Getenv:  func(k string) string { return m[k] },
		Dir:     dir,
		Stdin:   strings.NewReader(stdin),
		Keyring: kr,
	}
	if routes != nil {
		h.env.APIBase = testapi.Fake(t, routes)
	}
	return h
}

func (h *harness) run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = cli.Execute(args, &out, &errb, h.env)
	return code, out.String(), errb.String()
}

func jsonHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

const whoamiBody = `{"entity":{"entity_type":"whoami","authorization":{"scopes":["read","write"]},
"member":{"id":"u1","mention_name":"testuser","name":"Test User"},
"workspace":{"slug":"example-ws","name":"Template Systems"},
"default_workflow":{"id":5,"name":"Default"},
"default_workflow_state":{"id":6,"name":"Backlog"}}}`

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON: %q: %v", s, err)
	}
	return m
}

// storyRoot returns a root with a story group, to test the group helpers.
func storyRoot(env cli.Env) *cobra.Command {
	root := cli.NewRoot(env)
	story := cli.Group("story", "Work with stories")
	create := &cobra.Command{Use: "create", Short: "Create a story", Run: func(*cobra.Command, []string) {}}
	create.Flags().String("name", "", "story name")
	create.Flags().Int("estimate", 0, "story points")
	story.AddCommand(create)
	root.AddCommand(story)
	return root
}
