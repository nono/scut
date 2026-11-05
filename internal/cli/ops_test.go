package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/testgit"
)

// specOp is one operation of the v4 spec, with a regexp for its path.
type specOp struct {
	method, id string
	re         *regexp.Regexp
	params     int // number of {param} segments; fewer wins a tie
}

func readSpecOps(t *testing.T) []specOp {
	t.Helper()
	b, err := os.ReadFile("../../api/shortcut.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var ops []specOp
	for p, item := range doc.Paths {
		segs := strings.Split(p, "/")
		n := 0
		for i, sg := range segs {
			if strings.HasPrefix(sg, "{") {
				segs[i] = "[^/]+"
				n++
			} else {
				segs[i] = regexp.QuoteMeta(sg)
			}
		}
		re := regexp.MustCompile("^" + strings.Join(segs, "/") + "$")
		for m, op := range item {
			if op.OperationID != "" {
				ops = append(ops, specOp{strings.ToUpper(m), op.OperationID, re, n})
			}
		}
	}
	return ops
}

// opFor returns the operationId of a request, or "" when no spec path matches.
func opFor(ops []specOp, method, path string) string {
	best := specOp{params: -1}
	for _, o := range ops {
		if o.method == method && o.re.MatchString(path) && (best.params < 0 || o.params < best.params) {
			best = o
		}
	}
	return best.id
}

// opsRecorder serves a fake API for every command and records each request.
type opsRecorder struct {
	mu   sync.Mutex
	reqs []string // "METHOD path"
}

func (o *opsRecorder) routes(t *testing.T) map[string]http.HandlerFunc {
	story := storyFixture(t)
	epic := epicFixture()
	cmt := jsonOf(map[string]any{"entity": comment(9, "hi", false)})
	created := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(body))
		}
	}
	m := lookupRoutes()
	m["GET /api/v4/acme/workflows"] = jsonHandler(listBody(
		map[string]any{"id": 5, "name": "Standard", "workflow_states": nestedList(map[string]any{"id": 7, "name": "To Do"})},
		map[string]any{"id": 500000005, "name": "Fixture", "workflow_states": nestedList( // the workflow of the story fixture
			map[string]any{"id": 17, "name": "To Do"}, map[string]any{"id": 18, "name": "Done"})}))
	for k, v := range map[string]http.HandlerFunc{
		"GET /api/v4/whoami":                        jsonHandler(whoamiBody),
		"GET /api/v4/acme/labels":                   jsonHandler(listBody(map[string]any{"id": 31, "name": "bug-hunt"})),
		"GET /api/v4/acme/stories":                  jsonHandler(listBody(story)),
		"POST /api/v4/acme/stories":                 created(jsonOf(map[string]any{"entity": story})),
		"GET /api/v4/acme/stories/search":           jsonHandler(listBody(story)),
		"GET /api/v4/acme/stories/{id}":             jsonHandler(jsonOf(map[string]any{"entity": story})),
		"PATCH /api/v4/acme/stories/{id}":           jsonHandler(jsonOf(map[string]any{"entity": story})),
		"GET /api/v4/acme/stories/{id}/comments":    jsonHandler(listBody(comment(1, "hi", false))),
		"POST /api/v4/acme/stories/{id}/comments":   created(cmt),
		"GET /api/v4/acme/stories/{id}/story-links": jsonHandler(listBody(linkEntity(50, 5818, "blocks", 9))),
		"GET /api/v4/acme/stories/{id}/files":       jsonHandler(listBody()),
		"POST /api/v4/acme/story-links":             created(jsonOf(map[string]any{"entity": linkEntity(51, 5818, "blocks", 4000)})),
		"DELETE /api/v4/acme/story-links/{id}":      func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) },
		"PATCH /api/v4/acme/story-links/{id}":       jsonHandler(jsonOf(map[string]any{"entity": linkEntity(50, 5818, "relates to", 9)})),
		"GET /api/v4/acme/workflows/{id}/states": jsonHandler(listBody( // the states of the workflow of the story fixture
			map[string]any{"id": 500000007, "type": "unstarted", "position": 0},
			map[string]any{"id": 500000008, "type": "started", "position": 1})),
		"GET /api/v4/acme/epics/search":         jsonHandler(listBody(epic)),
		"GET /api/v4/acme/epics/{id}":           jsonHandler(jsonOf(map[string]any{"entity": epic})),
		"GET /api/v4/acme/epics/{id}/stories":   jsonHandler(listBody(story)),
		"POST /api/v4/acme/epics/{id}/comments": created(cmt),
	} {
		m[k] = v
	}
	for k, h := range m {
		m[k] = func(w http.ResponseWriter, r *http.Request) {
			o.mu.Lock()
			o.reqs = append(o.reqs, r.Method+" "+r.URL.Path)
			o.mu.Unlock()
			h(w, r)
		}
	}
	return m
}

// opsCases are commands, with arguments that make them call the API on each
// path that can add an operation: names to resolve, @me, and so on.
var opsCases = [][]string{
	{"whoami"},
	{"auth", "status"},
	{"auth", "login"},
	{"team", "list"},
	{"member", "list"},
	{"workflow", "list"},
	{"workflow", "states", "Standard"},
	{"field", "list"},
	{"field", "values", "Technical Area"},
	{"cache", "clear"},
	{"story", "view", "5818"},
	{"story", "view", "5818", "5818"},
	{"story", "search", "x"},
	{"story", "comments", "5818"},
	{"story", "comment", "5818", "--body-file", "-"},
	{"story", "update", "5818", "--state", "Done", "--epic", "Big epic", "--requester", "@me",
		"--add-owner", "bruno", "--field", "Technical Area=Back end"},
	{"story", "create", "--name", "x", "--team", "Software Dev", "--state", "To Do", "--epic", "Big epic",
		"--owner", "@me", "--requester", "bruno", "--label", "bug-hunt", "--field", "Technical Area=Back end"},
	{"story", "create", "--name", "x", "--workflow", "Standard", "--state", "To Do"},
	{"story", "link", "5818", "blocks", "4000"},
	{"story", "unlink", "5818", "blocks", "9"},
	{"story", "relink", "5818", "blocks", "9", "relates-to"},
	{"story", "branch", "5818"},
	{"story", "files", "5818", "--out", "@tmp"},
	{"epic", "view", "5387"},
	{"epic", "view", "Big epic"},
	{"epic", "search", "x"},
	{"epic", "stories", "Big epic"},
	{"epic", "comment", "Big epic", "--body-file", "-"},
	{"worktree", "5818", "--repo", "@repo", "--start"},
}

// Each operation that a command calls is in its scut/ops annotation, so
// docs/coverage.md is right.
func TestOpsAnnotationsComplete(t *testing.T) {
	ops := readSpecOps(t)
	tested := map[*cobra.Command]bool{}
	root := cli.NewRoot(cli.Env{})
	for _, args := range opsCases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var rec opsRecorder
			args = slices.Clone(args)
			for i, a := range args {
				switch a {
				case "@tmp":
					args[i] = t.TempDir()
				case "@repo":
					_, args[i] = testgit.Repos(t)
				}
			}
			h := newHarness(t, rec.routes(t), "sct_rw_acme_s3cret\n", map[string]string{"SCUT_TOKEN": "sct_rw_acme_s3cret"})
			code, _, errs := h.run(args...)
			if code != 0 {
				t.Fatalf("code %d, stderr %s, requests %v", code, errs, rec.reqs)
			}
			c, _, err := root.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			tested[c] = true
			annotated := strings.Split(c.Annotations[cli.OpsAnnotation], ",")
			var missing []string
			for _, r := range rec.reqs {
				method, path, _ := strings.Cut(r, " ")
				id := opFor(ops, method, path)
				if id == "" {
					t.Errorf("%s: no operation in the spec", r)
					continue
				}
				if !slices.Contains(annotated, id) && !slices.Contains(missing, id) {
					missing = append(missing, id)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s calls %v, not in its annotation %q", c.CommandPath(), missing, c.Annotations[cli.OpsAnnotation])
			}
		})
	}
	// Each leaf command that has an annotation must have a case.
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			walk(s)
		}
		if a := c.Annotations[cli.OpsAnnotation]; a != "" && a != "*" && !tested[c] {
			t.Errorf("%s has no case in opsCases", c.CommandPath())
		}
	}
	walk(root)
}
