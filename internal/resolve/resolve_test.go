package resolve_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nono/scut/internal/cache"
	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/resolve"
	"github.com/nono/scut/internal/testapi"
	"github.com/nono/scut/internal/transport"
)

const (
	me         = "00000000-0000-4000-8000-0000000000aa"
	softwareID = "0f0f0f0f-8dfb-475d-b664-5068e9d1d778"
	techArea   = "0f0f0f0f-08b2-4eb1-b29f-703a73481c90"
	backEnd    = "0f0f0f0f-08aa-4af3-b3d0-d03dc9e0846c"
)

type api struct {
	routes map[string]http.HandlerFunc
	calls  map[string]*atomic.Int32
}

func (a *api) json(key string, v any) {
	n := &atomic.Int32{}
	a.calls[key] = n
	a.routes[key] = func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (a *api) n(key string) int { return int(a.calls[key].Load()) }

func list(items ...any) map[string]any {
	if items == nil {
		items = []any{} // the API sends [], never null
	}
	return map[string]any{"current_page": 1, "current_items": len(items), "total_items": len(items), "total_pages": 1, "entities": items}
}

func nested(items ...any) map[string]any {
	return map[string]any{"current_items": len(items), "total_items": len(items), "entities": items}
}

type env struct {
	*api
	now time.Time
	c   *cache.Store
	d   *transport.Doer
	r   *resolve.R
}

func newEnv(t *testing.T, setup func(a *api)) *env {
	t.Helper()
	a := &api{routes: map[string]http.HandlerFunc{}, calls: map[string]*atomic.Int32{}}
	a.json("GET /api/v4/whoami", map[string]any{"entity": map[string]any{"member": map[string]any{"id": me}}})
	a.json("GET /api/v4/acme/members", list(
		map[string]any{"id": me, "mention_name": "testuser", "name": "Test User", "email_address": "bruno@example.com"},
		map[string]any{"id": "u2", "mention_name": "alice", "name": "Alice", "email_address": "alice@example.com"},
	))
	a.json("GET /api/v4/acme/teams", list(map[string]any{
		"id": softwareID, "name": "Software Dev", "default_workflow": map[string]any{"id": 500000005, "name": "Standard"},
		"workflows": nested(map[string]any{"id": 500000005, "name": "Standard"}),
	}))
	a.json("GET /api/v4/acme/workflows", list(
		map[string]any{"id": 500000005, "name": "Standard", "workflow_states": nested(
			map[string]any{"id": 500000007, "name": "To Do"}, map[string]any{"id": 500000008, "name": "Done"})},
		map[string]any{"id": 500004015, "name": "Simpler Kanban", "workflow_states": nested(
			map[string]any{"id": 500004021, "name": "To Do"})},
	))
	a.json("GET /api/v4/acme/custom-fields", list(map[string]any{
		"id": techArea, "name": "Technical Area", "field_type": "enum",
		"values": []any{map[string]any{"id": backEnd, "value": "Back end"}, map[string]any{"id": "api-id", "value": "API"}},
	}))
	a.json("GET /api/v4/acme/labels", list(map[string]any{"id": 7, "name": "bug"}))
	if setup != nil {
		setup(a)
	}
	base := testapi.Fake(t, a.routes)
	e := &env{api: a, now: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)}
	e.c = cache.New(t.TempDir(), time.Hour, func() time.Time { return e.now })
	d := transport.New(config.Settings{Token: "sct_rw_acme_s3cret", Workspace: "acme"}, transport.WithBaseURL(base))
	e.d = d
	e.r = resolve.New(d, e.c)
	return e
}

func wantErr(t *testing.T, err error, code exit.Code) *exit.Error {
	t.Helper()
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("got %v, want code %d", err, code)
	}
	return e
}

func TestResolveExactCase(t *testing.T) {
	e := newEnv(t, nil)
	f, v, err := e.r.Field("Technical Area", "Back end")
	if err != nil || f != techArea || v != backEnd {
		t.Fatalf("got %s %s %v", f, v, err)
	}
	_, _, err = e.r.Field("Technical Area", "Back End")
	x := wantErr(t, err, exit.NotFound)
	if !slices.Contains(x.Valid, "Back end") {
		t.Errorf("valid %v", x.Valid)
	}
	_, _, err = e.r.Field("technical area", "API")
	_ = wantErr(t, err, exit.NotFound)
}

func TestResolveRefreshOnMiss(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.r.Label("bug"); err != nil {
		t.Fatal(err)
	}
	if n := e.n("GET /api/v4/acme/labels"); n != 1 {
		t.Fatalf("first lookup: %d fetches", n)
	}
	// Fresh cache, name unknown: one refresh, then exit 3.
	_, err := e.r.Label("new")
	x := wantErr(t, err, exit.NotFound)
	if n := e.n("GET /api/v4/acme/labels"); n != 2 || !slices.Contains(x.Valid, "bug") {
		t.Errorf("fetches %d, valid %v", n, x.Valid)
	}

	// Stale cache, API now has the name: one refresh, found.
	e2 := newEnv(t, nil)
	_ = e2.c.Put("labels", []map[string]any{{"id": 1, "name": "old"}}, e2.now)
	e2.now = e2.now.Add(2 * time.Hour)
	id, err := e2.r.Label("bug")
	if err != nil || id != 7 || e2.n("GET /api/v4/acme/labels") != 1 {
		t.Errorf("id %d, err %v, fetches %d", id, err, e2.n("GET /api/v4/acme/labels"))
	}
}

func TestResolveFreshHitNoCall(t *testing.T) {
	e := newEnv(t, nil)
	_ = e.c.Put("teams", []map[string]any{{"id": softwareID, "name": "Software Dev"}}, e.now)
	id, err := e.r.Team("Software Dev")
	if err != nil || id != softwareID || e.n("GET /api/v4/acme/teams") != 0 {
		t.Errorf("id %s, err %v", id, err)
	}
}

func TestStateAmbiguous(t *testing.T) {
	e := newEnv(t, nil)
	_, err := e.r.State("To Do", 0, "")
	x := wantErr(t, err, exit.Usage)
	slices.Sort(x.Valid)
	if !slices.Equal(x.Valid, []string{"Simpler Kanban", "Standard"}) {
		t.Errorf("valid %v", x.Valid)
	}
	id, err := e.r.State("To Do", 0, softwareID)
	if err != nil || id != 500000007 {
		t.Errorf("team: %d, %v", id, err)
	}
	id, err = e.r.State("To Do", 500004015, "")
	if err != nil || id != 500004021 {
		t.Errorf("workflow: %d, %v", id, err)
	}
	_, err = e.r.State("Nope", 500004015, "")
	x = wantErr(t, err, exit.NotFound)
	if x.Msg != `no state "Nope" in workflow "Simpler Kanban"` || !slices.Equal(x.Valid, []string{"To Do"}) {
		t.Errorf("%q %v", x.Msg, x.Valid)
	}
	id, err = e.r.State("Done", 0, "")
	if err != nil || id != 500000008 {
		t.Errorf("unique state: %d, %v", id, err)
	}
}

// The whoami cache is per token: another token is another member.
func TestMeCachePerToken(t *testing.T) {
	members := map[string]string{"Bearer sct_rw_acme_a": "member-a", "Bearer sct_rw_acme_b": "member-b"}
	base := testapi.Fake(t, map[string]http.HandlerFunc{"GET /api/v4/whoami": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"entity": map[string]any{"member": map[string]any{"id": members[r.Header.Get("Authorization")]}}})
	}})
	dir := t.TempDir()
	c := cache.New(dir, time.Hour, time.Now)
	for _, tc := range []struct{ token, want string }{{"sct_rw_acme_a", "member-a"}, {"sct_rw_acme_b", "member-b"}, {"sct_rw_acme_a", "member-a"}} {
		d := transport.New(config.Settings{Token: tc.token, Workspace: "acme"}, transport.WithBaseURL(base))
		if id, err := resolve.New(d, c).Me(); err != nil || id != tc.want {
			t.Errorf("%s: %s, %v; want %s", tc.token, id, err, tc.want)
		}
	}
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if b, _ := os.ReadFile(filepath.Join(dir, e.Name())); strings.Contains(e.Name()+string(b), "sct_") {
			t.Errorf("token in cache file %s", e.Name())
		}
	}
	if len(es) != 2 {
		t.Errorf("cache files %d, want 2", len(es))
	}
}

func TestMemberMe(t *testing.T) {
	e := newEnv(t, nil)
	sum := sha256.Sum256([]byte("sct_rw_acme_s3cret"))
	_ = e.c.Put("whoami-"+hex.EncodeToString(sum[:])[:16], me, e.now)
	id, err := e.r.Member("@me")
	if err != nil || id != me || e.n("GET /api/v4/whoami") != 0 {
		t.Errorf("id %s, err %v, calls %d", id, err, e.n("GET /api/v4/whoami"))
	}
	// Cold cache: one call, then cached.
	e = newEnv(t, nil)
	for range 2 {
		if id, err := e.r.Me(); err != nil || id != me {
			t.Fatalf("id %s, err %v", id, err)
		}
	}
	if n := e.n("GET /api/v4/whoami"); n != 1 {
		t.Errorf("whoami calls %d", n)
	}
}

func TestMemberForms(t *testing.T) {
	e := newEnv(t, nil)
	for _, in := range []string{"testuser", "@testuser", "bruno@example.com", me} {
		if id, err := e.r.Member(in); err != nil || id != me {
			t.Errorf("%q: %s, %v", in, id, err)
		}
	}
	_, err := e.r.Member("@Alice")
	x := wantErr(t, err, exit.NotFound)
	if !slices.Contains(x.Valid, "alice") {
		t.Errorf("valid %v", x.Valid)
	}
}

func TestNoFieldDrop(t *testing.T) {
	e := newEnv(t, nil)
	got, err := e.r.Members([]string{"@me", "alice"})
	if err != nil || !slices.Equal(got, []string{me, "u2"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	got, err = e.r.Members([]string{"alice", "ghost"})
	_ = wantErr(t, err, exit.NotFound)
	if got != nil {
		t.Errorf("partial result %v", got)
	}
}

func TestShortNestedList(t *testing.T) {
	e := newEnv(t, func(a *api) {
		a.json("GET /api/v4/acme/workflows", list(map[string]any{
			"id": 5, "name": "Big", "workflow_states": map[string]any{
				"current_items": 1, "total_items": 2,
				"entities": []any{map[string]any{"id": 50, "name": "A"}},
				"list_url": "https://api.app.shortcut.com/api/v4/acme/workflows/5/states",
			},
		}))
		a.json("GET /api/v4/acme/workflows/5/states", list(
			map[string]any{"id": 50, "name": "A"}, map[string]any{"id": 51, "name": "B"}))
	})
	id, err := e.r.State("B", 5, "")
	if err != nil || id != 51 {
		t.Errorf("got %d, %v", id, err)
	}
}

func TestEpic(t *testing.T) {
	var query string
	e := newEnv(t, func(a *api) {
		a.json("GET /api/v4/acme/epics/search", list(
			map[string]any{"id": 1, "name": "Alpha launch"},
			map[string]any{"id": 2, "name": "Alpha"},
			map[string]any{"id": 3, "name": "Dup"}, map[string]any{"id": 4, "name": "Dup"}))
		inner := a.routes["GET /api/v4/acme/epics/search"]
		a.routes["GET /api/v4/acme/epics/search"] = func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query().Get("query")
			if r.URL.Query().Get("cursor") == "" && query == `"Late"` {
				w.Header().Set("Content-Type", "application/json")
				p1 := list(map[string]any{"id": 8, "name": "Late one"})
				p1["next_page_url"] = "http://x/api/v4/acme/epics/search?cursor=p2"
				_ = json.NewEncoder(w).Encode(p1)
				return
			}
			if r.URL.Query().Get("cursor") == "p2" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(list(map[string]any{"id": 9, "name": "Late"}))
				return
			}
			inner(w, r)
		}
	})
	if id, err := e.r.Epic("42"); err != nil || id != 42 {
		t.Errorf("id: %d, %v", id, err)
	}
	if id, err := e.r.Epic("Alpha"); err != nil || id != 2 || query != `"Alpha"` {
		t.Errorf("name: %d, %v, query %q", id, err, query)
	}
	if id, err := e.r.Epic("Late"); err != nil || id != 9 {
		t.Errorf("page 2: %d, %v", id, err)
	}
	_, err := e.r.Epic("Dup")
	x := wantErr(t, err, exit.Usage)
	if !slices.Equal(x.Valid, []string{"3", "4"}) {
		t.Errorf("valid %v", x.Valid)
	}
	_, err = e.r.Epic("Missing")
	_ = wantErr(t, err, exit.NotFound)
}

func TestNames(t *testing.T) {
	e := newEnv(t, nil)
	got, err := e.r.Names("workflows", "500004015", "1")
	if err != nil || len(got) != 1 || got["500004015"] != "Simpler Kanban" {
		t.Errorf("got %v, %v", got, err)
	}
	got, err = e.r.Names("members", me)
	if err != nil || got[me] != "testuser" {
		t.Errorf("got %v, %v", got, err)
	}
	_, err = e.r.Names("bogus", "1")
	_ = wantErr(t, err, exit.Usage)
}

func TestUnwritableCache(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	e := newEnv(t, nil)
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	e.r = resolve.New(e.d, cache.New(dir, time.Hour, time.Now))
	if id, err := e.r.Team("Software Dev"); err != nil || id != softwareID {
		t.Errorf("team: %s, %v", id, err)
	}
	if id, err := e.r.Me(); err != nil || id != me {
		t.Errorf("me: %s, %v", id, err)
	}
}

// A cache clear during a fetch makes the fetched list stale: it holds data
// from before the clear.
func TestClearDuringFetch(t *testing.T) {
	var e *env
	e = newEnv(t, func(a *api) {
		a.routes["GET /api/v4/acme/labels"] = func(w http.ResponseWriter, r *http.Request) {
			e.now = e.now.Add(time.Second)
			_ = e.c.Clear()
			e.now = e.now.Add(time.Second)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list(map[string]any{"id": 7, "name": "bug"}))
		}
	})
	if _, err := e.r.Label("bug"); err != nil {
		t.Fatal(err)
	}
	var raw []json.RawMessage
	if fresh, err := e.c.Get("labels", &raw); fresh || err != nil || len(raw) != 1 {
		t.Errorf("fresh %v, %d items, err %v", fresh, len(raw), err)
	}
}

func TestEmptyListIsFresh(t *testing.T) {
	e := newEnv(t, func(a *api) { a.json("GET /api/v4/acme/labels", list()) })
	for range 2 {
		if got, err := e.r.Names("labels", "1"); err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	}
	if n := e.n("GET /api/v4/acme/labels"); n != 1 {
		t.Errorf("fetches %d", n)
	}
}

func TestMemberEmptyMention(t *testing.T) {
	e := newEnv(t, func(a *api) {
		a.json("GET /api/v4/acme/members", list(
			map[string]any{"id": "u1", "mention_name": "", "email_address": "a@x.io"},
			map[string]any{"id": "u2", "mention_name": "bob"}))
	})
	_, err := e.r.Member("")
	x := wantErr(t, err, exit.NotFound)
	if !slices.Equal(x.Valid, []string{"bob"}) {
		t.Errorf("valid %v", x.Valid)
	}
}

func TestResolveLists(t *testing.T) {
	e := newEnv(t, nil)
	for name, f := range map[string]func() ([]json.RawMessage, error){
		"teams": e.r.Teams, "members": e.r.MemberList, "workflows": e.r.Workflows, "custom-fields": e.r.Fields,
	} {
		l, err := f()
		if err != nil || len(l) == 0 {
			t.Errorf("%s: %d items, %v", name, len(l), err)
		}
		if n := e.n("GET /api/v4/acme/" + name); n != 1 {
			t.Errorf("%s: %d fetches", name, n)
		}
		if _, err := f(); err != nil || e.n("GET /api/v4/acme/"+name) != 1 {
			t.Errorf("%s: second call fetched again", name)
		}
	}
}

func TestResolveFieldValues(t *testing.T) {
	e := newEnv(t, nil)
	vs, err := e.r.FieldValues("Technical Area")
	if err != nil || len(vs) != 2 || vs[0] != (resolve.FieldValue{ID: backEnd, Name: "Back end"}) {
		t.Fatalf("got %v, %v", vs, err)
	}
	_, err = e.r.FieldValues("technical area")
	if x := wantErr(t, err, exit.NotFound); !slices.Contains(x.Valid, "Technical Area") {
		t.Errorf("valid %v", x.Valid)
	}
}
