package config_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
)

type testEnv struct {
	root, userFile, repoFile, repoDir string
	env                               map[string]string
	kr                                fakeKeyring
}

// newEnv builds an isolated tree: home, XDG dirs, and a git repo dir.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	e := &testEnv{
		root:     root,
		userFile: filepath.Join(root, "config", "scut", "config.toml"),
		repoDir:  filepath.Join(root, "repo"),
		env: map[string]string{
			"HOME":            filepath.Join(root, "home"),
			"XDG_CONFIG_HOME": filepath.Join(root, "config"),
			"XDG_CACHE_HOME":  filepath.Join(root, "cache"),
		},
		kr: fakeKeyring{m: map[string]string{}},
	}
	e.repoFile = filepath.Join(e.repoDir, ".scut.toml")
	write(t, filepath.Join(e.repoDir, ".git", "HEAD"), "")
	write(t, filepath.Join(e.repoDir, "sub", "x"), "")
	e.env["SCUT_TOKEN"] = "sct_rw_ws_secret"
	return e
}

func (e *testEnv) opts(flags map[string]string) config.Options {
	return config.Options{
		Flags:   flags,
		Getenv:  func(k string) string { return e.env[k] },
		Dir:     filepath.Join(e.repoDir, "sub"),
		Keyring: e.kr,
	}
}

func TestPrecedence(t *testing.T) {
	rows := []struct {
		name, env string
		get       func(config.Settings) string
	}{
		{"team", "SCUT_TEAM", func(s config.Settings) string { return s.Team }},
		{"state", "SCUT_STATE", func(s config.Settings) string { return s.State }},
		{"output", "SCUT_OUTPUT", func(s config.Settings) string { return s.Output }},
		{"workflow", "SCUT_WORKFLOW", func(s config.Settings) string { return s.Workflow }},
		{"requester", "SCUT_REQUESTER", func(s config.Settings) string { return s.Requester }},
		{"owner", "SCUT_OWNER", func(s config.Settings) string { return s.Owner }},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			e := newEnv(t)
			val := func(src string) string {
				if r.name == "output" {
					return map[string]string{"flag": "human", "env": "json", "repo": "human", "user": "json"}[src]
				}
				return src + "-value"
			}
			write(t, e.userFile, r.name+` = "`+val("user")+`"`)
			write(t, e.repoFile, r.name+` = "`+val("repo")+`"`)
			e.env[r.env] = val("env")
			flags := map[string]string{r.name: val("flag")}

			check := func(want string) {
				t.Helper()
				s, err := config.Load(e.opts(flags))
				if err != nil {
					t.Fatal(err)
				}
				if got := r.get(s); got != want {
					t.Errorf("got %q, want %q", got, want)
				}
			}
			check(val("flag"))
			flags = nil
			check(val("env"))
			delete(e.env, r.env)
			check(val("repo"))
			write(t, e.repoFile, "")
			check(val("user"))
			write(t, e.userFile, "")
			if r.name == "output" {
				check("json")
			} else {
				check("")
			}
		})
	}
}

func TestFieldsAndKeys(t *testing.T) {
	e := newEnv(t)
	write(t, e.userFile, "team = \"Software Dev\"\nowner = \"@me\"\nrequester = \"@me\"\nworkflow = \"W\"\n[fields]\n\"Technical Area\" = \"Back end\"\nA = \"1\"\n")
	write(t, e.repoFile, "[fields]\nA = \"2\"\n")
	s, err := config.Load(e.opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["Technical Area"] != "Back end" || s.Fields["A"] != "2" || s.Owner != "@me" || s.Requester != "@me" || s.Workflow != "W" {
		t.Errorf("got %+v", s)
	}
	e.env["SCUT_WORKFLOW"] = "E"
	if s, _ = config.Load(e.opts(nil)); s.Workflow != "E" {
		t.Errorf("workflow %q", s.Workflow)
	}
}

// SCUT_FIELDS overrides the [fields] of the files, one key at a time.
func TestFieldsEnv(t *testing.T) {
	e := newEnv(t)
	write(t, e.userFile, "[fields]\nA = \"1\"\nB = \"2\"\n")
	write(t, e.repoFile, "[fields]\nB = \"3\"\n")
	e.env["SCUT_FIELDS"] = "A=env; Technical Area = Back end ;"
	s, err := config.Load(e.opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Fields) != 3 || s.Fields["A"] != "env" || s.Fields["B"] != "3" || s.Fields["Technical Area"] != "Back end" {
		t.Errorf("fields %v", s.Fields)
	}
	for _, bad := range []string{"A", "=x", "A=1;B"} {
		e.env["SCUT_FIELDS"] = bad
		_, err := config.Load(e.opts(nil))
		var xe *exit.Error
		if !errors.As(err, &xe) || xe.Code != exit.Usage || !strings.HasPrefix(xe.Msg, "SCUT_FIELDS: ") {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func TestRepoFileWalksUpToGitRoot(t *testing.T) {
	e := newEnv(t)
	write(t, e.repoFile, `team = "T"`)
	s, err := config.Load(e.opts(nil)) // Dir is repo/sub
	if err != nil || s.Team != "T" {
		t.Fatalf("got %q, %v", s.Team, err)
	}
	// A file above the git root is not read.
	e2 := newEnv(t)
	write(t, filepath.Join(e2.root, ".scut.toml"), `team = "above"`)
	if s, err = config.Load(e2.opts(nil)); err != nil || s.Team != "" {
		t.Fatalf("got %q, %v", s.Team, err)
	}
}

func TestRepoFileTokenRefused(t *testing.T) {
	for _, key := range []string{"token", "token_file"} {
		e := newEnv(t)
		write(t, e.repoFile, key+` = "x"`)
		_, err := config.Load(e.opts(nil))
		var xe *exit.Error
		if !errors.As(err, &xe) || xe.Code != exit.Usage ||
			!strings.Contains(xe.Msg, ".scut.toml") || !strings.Contains(xe.Msg, key) {
			t.Errorf("%s: got %v", key, err)
		}
	}
}

func TestUnknownKey(t *testing.T) {
	e := newEnv(t)
	write(t, e.userFile, `colour = "x"`)
	_, err := config.Load(e.opts(nil))
	var xe *exit.Error
	if !errors.As(err, &xe) || xe.Code != exit.Usage ||
		!strings.Contains(xe.Msg, "config.toml") || !strings.Contains(xe.Msg, "colour") {
		t.Fatalf("got %v", err)
	}
}

func TestBadOutput(t *testing.T) {
	e := newEnv(t)
	write(t, e.userFile, `output = "xml"`)
	_, err := config.Load(e.opts(nil))
	var xe *exit.Error
	if want := e.userFile + `: key output: "xml" is not valid, use json or human`; !errors.As(err, &xe) || xe.Code != exit.Usage || xe.Msg != want {
		t.Errorf("got %v, want %s", err, want)
	}
	e.env["SCUT_OUTPUT"] = "yaml"
	_, err = config.Load(e.opts(nil))
	if want := `SCUT_OUTPUT: "yaml" is not valid, use json or human`; !errors.As(err, &xe) || xe.Msg != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

// Explain names the source of a value in a lookup error.
func TestExplain(t *testing.T) {
	e := newEnv(t)
	write(t, e.userFile, "state = \"S\"\n[fields]\nA = \"1\"\nB = \"2\"\n")
	write(t, e.repoFile, "team = \"T\"\n")
	e.env["SCUT_FIELDS"] = "B=3"
	s, err := config.Load(e.opts(map[string]string{"workflow": "W"}))
	if err != nil {
		t.Fatal(err)
	}
	miss := exit.NotFoundf(`no x "y"`)
	for key, want := range map[string]string{
		"team":        e.repoFile + `: key team: no x "y"`,
		"state":       e.userFile + `: key state: no x "y"`,
		"fields.A":    e.userFile + `: key fields."A": no x "y"`,
		"fields.B":    `SCUT_FIELDS: no x "y"`,
		"workflow":    `no x "y"`, // a flag
		"owner":       `no x "y"`, // not set
		"fields.none": `no x "y"`,
	} {
		if got := s.Explain(key, miss).Error(); got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
	other := &exit.Error{Code: exit.Unavailable, Msg: "down"}
	if got := s.Explain("team", other); got != other {
		t.Errorf("a failure that is not a lookup miss changed: %v", got)
	}
}

func TestProfile(t *testing.T) {
	e := newEnv(t)
	other := filepath.Join(e.root, "other-token")
	write(t, other, "sct_ro_other-ws_zzz\n")
	write(t, e.userFile, "team = \"base\"\n[profile.other]\ntoken_file = \""+other+"\"\nteam = \"o\"\n")
	delete(e.env, "SCUT_TOKEN")
	e.env["SCUT_PROFILE"] = "other"
	s, err := config.Load(e.opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Profile != "other" || s.Workspace != "other-ws" || s.Scopes != "ro" || s.Team != "o" {
		t.Errorf("got %+v", s)
	}
	// Keyring user is the profile name.
	delete(e.env, "SCUT_PROFILE")
	e.kr.m["scut/default"] = "sct_rw_kr-ws_k"
	if s, err = config.Load(e.opts(nil)); err != nil || s.Workspace != "kr-ws" || s.Team != "base" {
		t.Fatalf("got %+v, %v", s, err)
	}
	// Flag beats env for the profile.
	e.env["SCUT_PROFILE"] = "nope"
	if s, err = config.Load(e.opts(map[string]string{"profile": "other"})); err != nil || s.Profile != "other" {
		t.Fatalf("got %+v, %v", s, err)
	}
}
