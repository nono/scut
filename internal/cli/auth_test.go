package cli_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestAuthLogin(t *testing.T) {
	var gotAuth string
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/whoami": func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			jsonHandler(whoamiBody)(w, r)
		},
	}, token+"\n", nil)
	code, out, errs := h.run("auth", "login")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["workspace"] != "example-ws" || m["scopes"] != "rw" || m["member"] != "testuser" || len(m) != 3 {
		t.Errorf("out %s", out)
	}
	if strings.Contains(out+errs, "s3cret") {
		t.Error("token leaked")
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("auth header %q", gotAuth)
	}
	if got := h.kr.m["scut/default"]; got != token {
		t.Errorf("keyring %v", h.kr.m)
	}
}

func TestAuthLoginProfile(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{"GET /api/v4/whoami": jsonHandler(whoamiBody)}, token, nil)
	if code, _, errs := h.run("--profile", "work", "auth", "login"); code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if h.kr.m["scut/work"] != token {
		t.Errorf("keyring %v", h.kr.m)
	}
}

func TestAuthLoginBadToken(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/whoami": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
		},
	}, token, nil)
	code, out, errs := h.run("auth", "login")
	if code != 4 {
		t.Fatalf("code %d, want 4; stderr %s", code, errs)
	}
	if out != "" || len(h.kr.m) != 0 {
		t.Errorf("out %q, keyring %v", out, h.kr.m)
	}
	if strings.Contains(errs, "s3cret") {
		t.Error("token leaked")
	}
}

func TestAuthLoginBadForm(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{}, "nonsense", nil)
	if code, _, _ := h.run("auth", "login"); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if len(h.kr.m) != 0 {
		t.Errorf("keyring %v", h.kr.m)
	}
}

func TestAuthStatus(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{"GET /api/v4/whoami": jsonHandler(whoamiBody)}, "",
		map[string]string{"SCUT_TOKEN": token})
	code, out, errs := h.run("auth", "status")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["workspace"] != "example-ws" || m["scopes"] != "rw" || m["source"] != "env SCUT_TOKEN" || m["member"] != "testuser" {
		t.Errorf("out %s", out)
	}
	if strings.Contains(out, "s3cret") {
		t.Error("token leaked")
	}
}

func TestAuthStatus401(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{
		"GET /api/v4/whoami": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
	}, "", map[string]string{"SCUT_TOKEN": token})
	if code, _, _ := h.run("auth", "status"); code != 4 {
		t.Fatalf("code %d, want 4", code)
	}
}

func TestAuthStatusNoToken(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{}, "", nil)
	if code, _, _ := h.run("auth", "status"); code != 4 {
		t.Fatalf("code %d, want 4", code)
	}
}

func TestWhoami(t *testing.T) {
	h := newHarness(t, map[string]http.HandlerFunc{"GET /api/v4/whoami": jsonHandler(whoamiBody)}, "",
		map[string]string{"SCUT_TOKEN": token})
	code, out, errs := h.run("whoami")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	m := decode(t, out)
	if m["id"] != "u1" || m["mention_name"] != "testuser" || m["name"] != "Test User" || m["workspace"] != "example-ws" {
		t.Errorf("out %s", out)
	}
	sc, _ := m["scopes"].([]any)
	if len(sc) != 2 || sc[0] != "read" {
		t.Errorf("scopes %v", m["scopes"])
	}
	wf, _ := m["default_workflow"].(map[string]any)
	st, _ := m["default_workflow_state"].(map[string]any)
	if wf["id"] != float64(5) || wf["name"] != "Default" || st["id"] != float64(6) || st["name"] != "Backlog" {
		t.Errorf("out %s", out)
	}
}
