package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
)

type fakeKeyring struct {
	m   map[string]string
	err error
}

func (f fakeKeyring) Get(service, user string) (string, error) {
	return f.m[service+"/"+user], f.err
}

func (f fakeKeyring) Set(service, user, secret string) error {
	f.m[service+"/"+user] = secret
	return nil
}

func TestParseTokenBad(t *testing.T) {
	for _, s := range []string{"abc", "sct_rw_", "sct_rw_ws", "sct__ws_x", "xxx_rw_ws_x"} {
		if _, _, err := config.ParseToken(s); err == nil {
			t.Errorf("%q: want error", s)
		}
	}
	// The slug names the cache dir: it must not escape it.
	for _, slug := range []string{"..", ".", "a/b", "A", "-a", "a b", `a\b`} {
		_, _, err := config.ParseToken("sct_rw_" + slug + "_x")
		var xe *exit.Error
		if !errors.As(err, &xe) || xe.Code != exit.Auth || !strings.Contains(xe.Msg, "malformed token") {
			t.Errorf("slug %q: got %v", slug, err)
		}
	}
	sc, ws, err := config.ParseToken("sct_rw_my-ws_a_b")
	if err != nil || sc != "rw" || ws != "my-ws" {
		t.Errorf("got %q %q %v", sc, ws, err)
	}
}

func TestTokenTrim(t *testing.T) {
	e := newEnv(t)
	f := filepath.Join(e.root, "tok")
	write(t, f, "sct_rw_example-ws_abc\n")
	s, err := config.Load(e.opts(map[string]string{"token-file": f}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Token != "sct_rw_example-ws_abc" || s.Workspace != "example-ws" || s.Scopes != "rw" {
		t.Errorf("got %+v", s)
	}
	want := filepath.Join(e.root, "cache", "scut", "example-ws")
	if s.CacheDir != want {
		t.Errorf("CacheDir %q, want %q", s.CacheDir, want)
	}
}

func TestTokenSourceOrder(t *testing.T) {
	e := newEnv(t)
	flagF := filepath.Join(e.root, "flag")
	fileF := filepath.Join(e.root, "file")
	write(t, flagF, "sct_rw_ws_flag")
	write(t, fileF, "sct_rw_ws_file")
	write(t, e.userFile, `token_file = "`+fileF+`"`)
	e.kr.m["scut/default"] = "sct_rw_ws_keyring"
	e.env["SCUT_TOKEN"] = "sct_rw_ws_env"

	steps := []struct{ src, secret string }{
		{"flag --token-file", "flag"},
		{"env SCUT_TOKEN", "env"},
		{"token_file " + fileF, "file"},
		{"keyring", "keyring"},
	}
	flags := map[string]string{"token-file": flagF}
	for _, st := range steps {
		s, err := config.Load(e.opts(flags))
		if err != nil {
			t.Fatal(err)
		}
		if s.Token != "sct_rw_ws_"+st.secret || s.TokenSource != st.src {
			t.Errorf("got token %q source %q, want %q %q", s.Token, s.TokenSource, st.secret, st.src)
		}
		if strings.Contains(s.TokenSource, "sct_") {
			t.Errorf("source leaks token: %q", s.TokenSource)
		}
		switch st.secret {
		case "flag":
			flags = nil
		case "env":
			delete(e.env, "SCUT_TOKEN")
		case "file":
			write(t, e.userFile, "")
		}
	}
}

func TestTokenFileTilde(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.root, "home", "tok"), "sct_r_ws_x\n")
	write(t, e.userFile, `token_file = "~/tok"`)
	delete(e.env, "SCUT_TOKEN")
	s, err := config.Load(e.opts(nil))
	if err != nil || s.Token != "sct_r_ws_x" {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestNoTokenIsAuthError(t *testing.T) {
	e := newEnv(t)
	delete(e.env, "SCUT_TOKEN")
	_, err := config.Load(e.opts(nil))
	var xe *exit.Error
	if !errors.As(err, &xe) || xe.Code != exit.Auth {
		t.Fatalf("got %v, want code 4", err)
	}
	e.kr.err = errors.New("no dbus")
	_, err = config.Load(e.opts(nil))
	if !errors.As(err, &xe) || xe.Code != exit.Auth || !strings.Contains(xe.Msg, "no dbus") {
		t.Fatalf("got %v, want code 4 with keyring error", err)
	}
}

func TestBadTokenInFile(t *testing.T) {
	e := newEnv(t)
	e.env["SCUT_TOKEN"] = "abc"
	_, err := config.Load(e.opts(nil))
	var xe *exit.Error
	if !errors.As(err, &xe) || xe.Code != exit.Auth || strings.Contains(xe.Msg, "abc") {
		t.Fatalf("got %v, want code 4", err)
	}
}

// A token source that is given but empty must not fall through to the
// keyring: that token can be for another workspace.
func TestEmptyTokenSource(t *testing.T) {
	e := newEnv(t)
	e.kr.m["scut/default"] = "sct_rw_other_secret"
	f := filepath.Join(e.root, "empty")
	write(t, f, " \n")
	var xe *exit.Error
	_, err := config.Load(e.opts(map[string]string{"token-file": f}))
	if !errors.As(err, &xe) || xe.Code != exit.Auth || xe.Msg != "token file "+f+" is empty" {
		t.Errorf("--token-file: got %v", err)
	}
	e.env["SCUT_TOKEN"] = "  "
	_, err = config.Load(e.opts(nil))
	if !errors.As(err, &xe) || xe.Code != exit.Auth {
		t.Errorf("blank SCUT_TOKEN: got %v", err)
	}
	delete(e.env, "SCUT_TOKEN")
	write(t, e.userFile, `token_file = "`+f+`"`)
	_, err = config.Load(e.opts(nil))
	if !errors.As(err, &xe) || xe.Code != exit.Auth || xe.Msg != "token file "+f+" is empty" {
		t.Errorf("token_file: got %v", err)
	}
}

func TestMissingTokenFile(t *testing.T) {
	e := newEnv(t)
	_, err := config.Load(e.opts(map[string]string{"token-file": filepath.Join(e.root, "nope")}))
	if err == nil {
		t.Fatal("want error")
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}
