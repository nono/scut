package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const linksPath = "/api/v4/acme/story-links"

func linkEntity(id, subject int, verb string, object int) map[string]any {
	return map[string]any{
		"id": id, "verb": verb, "entity_type": "story-link",
		"subject": map[string]any{"id": subject}, "object": map[string]any{"id": object},
	}
}

// linkPosts records the bodies of POST /story-links and answers 201.
func linkPosts(bodies *[]map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		*bodies = append(*bodies, m)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, jsonOf(map[string]any{"entity": linkEntity(100+len(*bodies), 1, "x", 2)}))
	}
}

func decodeLinks(t *testing.T, s string) []map[string]any {
	t.Helper()
	var l []map[string]any
	if err := json.Unmarshal([]byte(s), &l); err != nil {
		t.Fatalf("not a JSON array: %q: %v", s, err)
	}
	return l
}

func TestLinkVerbMap(t *testing.T) {
	var bodies []map[string]any
	h := newHarness(t, map[string]http.HandlerFunc{"POST " + linksPath: linkPosts(&bodies)}, "", searchEnv)
	if code, _, errs := h.run("story", "link", "5818", "blocked-by", "5819"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if b := bodies[0]; b["subject_story_id"] != float64(5819) || b["object_story_id"] != float64(5818) || b["verb"] != "blocks" {
		t.Errorf("blocked-by: %v", b)
	}
	if code, _, _ := h.run("story", "link", "5818", "relates-to", "5819"); code != 0 {
		t.Fatalf("relates-to: code %d", code)
	}
	if b := bodies[1]; b["subject_story_id"] != float64(5818) || b["verb"] != "relates to" {
		t.Errorf("relates-to: %v", b)
	}
	code, _, errs := h.run("story", "link", "5818", "foo", "5819")
	if code != 2 {
		t.Fatalf("foo: code %d", code)
	}
	if v := decode(t, errs)["valid"].([]any); len(v) != 5 {
		t.Errorf("valid %v", v)
	}
	if len(bodies) != 2 {
		t.Errorf("posts %d", len(bodies))
	}
}

func TestLinkArgCount(t *testing.T) {
	h := newHarness(t, nil, "", map[string]string{})
	for _, args := range [][]string{
		{"story", "link", "5818"},
		{"story", "link", "5818", "blocks"},
		{"story", "link", "5818", "blocks", "5819", "relates-to"},
	} {
		if code, _, _ := h.run(args...); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
}

func TestLinkMany(t *testing.T) {
	var bodies []map[string]any
	h := newHarness(t, map[string]http.HandlerFunc{"POST " + linksPath: linkPosts(&bodies)}, "", searchEnv)
	code, out, errs := h.run("story", "link", "5818", "blocks", "5819", "relates-to", "sc-5820")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if len(bodies) != 2 {
		t.Fatalf("posts %d", len(bodies))
	}
	l := decodeLinks(t, out)
	if len(l) != 2 || l[0]["verb"] != "blocks" || l[1]["verb"] != "relates-to" ||
		l[0]["subject"] != float64(5818) || l[1]["object"] != float64(5820) ||
		l[0]["id"] != float64(101) || l[0]["existed"] != false {
		t.Errorf("stdout %s", out)
	}
}

func TestLinkDuplicateExisted(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"POST " + linksPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"message":"Cannot create a duplicate Story Link."}`)
		},
	}, "", searchEnv)
	code, out, errs := h.run("story", "link", "5818", "blocks", "5819")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if l := decodeLinks(t, out); len(l) != 1 || l[0]["existed"] != true || l[0]["error"] != nil {
		t.Errorf("stdout %s", out)
	}
}

func TestLinkOther422IsError(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"POST " + linksPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"message":"Something else."}`)
		},
	}, "", searchEnv)
	code, out, _ := h.run("story", "link", "5818", "blocks", "5819")
	if code == 0 {
		t.Fatalf("code 0, stdout %s", out)
	}
	if l := decodeLinks(t, out); l[0]["error"] == nil {
		t.Errorf("stdout %s", out)
	}
}

func TestLinkPartialFail(t *testing.T) {
	n := 0
	h := newHarness(t, map[string]http.HandlerFunc{
		"POST " + linksPath: func(w http.ResponseWriter, r *http.Request) {
			n++
			if n == 2 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `{"message":"Story not found."}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = io.WriteString(w, jsonOf(map[string]any{"entity": linkEntity(7, 5818, "blocks", 5819)}))
		},
	}, "", searchEnv)
	code, out, _ := h.run("story", "link", "5818", "blocks", "5819", "relates-to", "5820")
	if code != 3 {
		t.Fatalf("code %d", code)
	}
	l := decodeLinks(t, out)
	if len(l) != 2 || l[0]["id"] != float64(7) || l[0]["error"] != nil {
		t.Fatalf("stdout %s", out)
	}
	e, _ := l[1]["error"].(map[string]any)
	if e == nil || e["status"] != float64(404) {
		t.Errorf("second: %s", out)
	}
}

// A 2xx body that does not decode is the error of that link only.
func TestLinkBadBody(t *testing.T) {
	n := 0
	h := newHarness(t, map[string]http.HandlerFunc{
		"POST " + linksPath: func(w http.ResponseWriter, r *http.Request) {
			n++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			if n == 1 {
				_, _ = io.WriteString(w, `{"entity":[1]}`)
				return
			}
			_, _ = io.WriteString(w, jsonOf(map[string]any{"entity": linkEntity(7, 5818, "blocks", 5819)}))
		},
	}, "", searchEnv)
	code, out, errs := h.run("story", "link", "5818", "blocks", "5819", "relates-to", "5820")
	if code != 1 || n != 2 {
		t.Fatalf("code %d, posts %d, stderr %s", code, n, errs)
	}
	l := decodeLinks(t, out)
	if len(l) != 2 || l[0]["error"] == nil || l[1]["id"] != float64(7) || l[1]["error"] != nil {
		t.Errorf("stdout %s", out)
	}
}

func listRoute(links ...any) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v4/acme/stories/5818/story-links": jsonHandler(listBody(links...)),
	}
}

func TestUnlink(t *testing.T) {
	routes := listRoute(linkEntity(11, 5820, "relates to", 5818), linkEntity(12, 5818, "blocks", 5819))
	deleted := ""
	routes["DELETE "+linksPath+"/12"] = func(w http.ResponseWriter, r *http.Request) {
		deleted = r.URL.Path
		w.WriteHeader(204)
	}
	h := newHarness(t, routes, "", searchEnv)
	code, out, errs := h.run("story", "unlink", "5818", "blocks", "5819")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if deleted == "" || decode(t, out)["deleted"] != float64(12) {
		t.Errorf("deleted %q, stdout %s", deleted, out)
	}
	code, _, errs = h.run("story", "unlink", "5818", "duplicates", "5819")
	if code != 3 {
		t.Fatalf("no match: code %d", code)
	}
	v, _ := decode(t, errs)["valid"].([]any)
	if len(v) != 2 || v[1] != "5818 blocks 5819" {
		t.Errorf("valid %v", v)
	}
}

func TestUnlinkSwap(t *testing.T) {
	routes := listRoute(linkEntity(12, 5819, "blocks", 5818))
	routes["DELETE "+linksPath+"/12"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }
	h := newHarness(t, routes, "", searchEnv)
	if code, out, errs := h.run("story", "unlink", "5818", "blocked-by", "5819"); code != 0 || !strings.Contains(out, "12") {
		t.Errorf("code %d, stdout %s, stderr %s", code, out, errs)
	}
}

// "relates to" has no direction: a link made as B relates to A matches A relates-to B.
func TestRelatesToBothOrders(t *testing.T) {
	routes := listRoute(linkEntity(11, 5820, "relates to", 5818), linkEntity(13, 5820, "blocks", 5818))
	deleted := 0
	routes["DELETE "+linksPath+"/11"] = func(w http.ResponseWriter, _ *http.Request) { deleted++; w.WriteHeader(204) }
	patched := 0
	routes["PATCH "+linksPath+"/11"] = func(w http.ResponseWriter, _ *http.Request) {
		patched++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jsonOf(map[string]any{"entity": linkEntity(11, 5818, "blocks", 5820)}))
	}
	h := newHarness(t, routes, "", searchEnv)
	if code, _, errs := h.run("story", "unlink", "5818", "relates-to", "5820"); code != 0 || deleted != 1 {
		t.Errorf("unlink: code %d, deleted %d, stderr %s", code, deleted, errs)
	}
	if code, _, errs := h.run("story", "relink", "5818", "relates-to", "5820", "blocks"); code != 0 || patched != 1 {
		t.Errorf("relink: code %d, patched %d, stderr %s", code, patched, errs)
	}
	if code, _, _ := h.run("story", "unlink", "5818", "blocks", "5820"); code != 3 {
		t.Errorf("blocks has a direction: code %d", code)
	}
}

func TestRelinkSwap(t *testing.T) {
	routes := listRoute(linkEntity(12, 5818, "blocks", 5819))
	var bodies []map[string]any
	routes["PATCH "+linksPath+"/12"] = func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		bodies = append(bodies, m)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jsonOf(map[string]any{"entity": linkEntity(12, 5819, "blocks", 5818)}))
	}
	h := newHarness(t, routes, "", searchEnv)
	code, out, errs := h.run("story", "relink", "5818", "blocks", "5819", "blocked-by")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if len(bodies) != 1 || bodies[0]["verb"] != "blocks" || bodies[0]["subject_story_id"] != float64(5819) ||
		bodies[0]["object_story_id"] != float64(5818) {
		t.Errorf("bodies %v", bodies)
	}
	m := decode(t, out)
	if m["id"] != float64(12) || m["subject"] != float64(5819) || m["object"] != float64(5818) || m["verb"] != "blocks" {
		t.Errorf("stdout %s", out)
	}
	if code, _, _ := h.run("story", "relink", "5818", "relates-to", "5819", "blocks"); code != 3 {
		t.Errorf("no match: code %d", code)
	}
	if code, _, _ := h.run("story", "relink", "5818", "blocks", "5819", "foo"); code != 2 {
		t.Errorf("bad verb: code %d", code)
	}
}
