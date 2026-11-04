// Package config loads the settings and the token of scut.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/nono/scut/internal/exit"
)

// Settings are the resolved settings.
type Settings struct {
	Profile, Workspace, Token, TokenSource  string
	Scopes                                  string
	Team, State, Workflow, Requester, Owner string
	Fields                                  map[string]string
	Output                                  string
	CacheDir                                string
}

// Options are the inputs of Load.
type Options struct {
	Flags   map[string]string // keys: team, state, output, token-file, profile
	Getenv  func(string) string
	Dir     string // start dir of the search for .scut.toml
	Keyring Keyring
}

type values struct {
	Team      string            `toml:"team"`
	State     string            `toml:"state"`
	Workflow  string            `toml:"workflow"`
	Requester string            `toml:"requester"`
	Owner     string            `toml:"owner"`
	Output    string            `toml:"output"`
	Token     string            `toml:"token"`
	TokenFile string            `toml:"token_file"`
	Fields    map[string]string `toml:"fields"`
}

type fileData struct {
	values
	Profile map[string]values `toml:"profile"`
}

// layer is one file with the profile applied.
type layer struct {
	values
	path string
}

const defaultProfile = "default"

// Load resolves the settings: flag > env > repo file > user file.
func Load(o Options) (Settings, error) {
	getenv := o.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	profile := first(o.Flags["profile"], getenv("SCUT_PROFILE"), defaultProfile)
	home := getenv("HOME")

	userPath := filepath.Join(first(getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config")), "scut", "config.toml")
	user, err := readLayer(userPath, profile, false)
	if err != nil {
		return Settings{}, err
	}
	var repo layer
	if o.Dir != "" {
		if p := findRepoFile(o.Dir); p != "" {
			if repo, err = readLayer(p, profile, true); err != nil {
				return Settings{}, err
			}
		}
	}

	s := Settings{Profile: profile, Fields: map[string]string{}}
	pick := func(flag, env, rf, uf string) string {
		return first(o.Flags[flag], getenv(env), rf, uf)
	}
	s.Team = pick("team", "SCUT_TEAM", repo.Team, user.Team)
	s.State = pick("state", "SCUT_STATE", repo.State, user.State)
	s.Workflow = first(getenv("SCUT_WORKFLOW"), repo.Workflow, user.Workflow)
	s.Requester = first(repo.Requester, user.Requester)
	s.Owner = first(repo.Owner, user.Owner)
	s.Output = pick("output", "SCUT_OUTPUT", repo.Output, user.Output)
	if s.Output == "" {
		s.Output = "json"
	}
	if s.Output != "json" && s.Output != "human" {
		return Settings{}, exit.Usagef("output %q is not valid, use json or human", s.Output)
	}
	for _, l := range []layer{user, repo} {
		for k, v := range l.Fields {
			s.Fields[k] = v
		}
	}

	if err := resolveToken(&s, o, getenv, user.TokenFile, home); err != nil {
		return Settings{}, err
	}
	cache := first(getenv("XDG_CACHE_HOME"), filepath.Join(home, ".cache"))
	s.CacheDir = filepath.Join(cache, "scut", s.Workspace)
	return s, nil
}

func resolveToken(s *Settings, o Options, getenv func(string) string, tokenFile, home string) error {
	var raw string
	switch {
	case o.Flags["token-file"] != "":
		t, err := readTokenFile(o.Flags["token-file"], home)
		if err != nil {
			return err
		}
		raw, s.TokenSource = t, "flag --token-file"
	case getenv("SCUT_TOKEN") != "":
		raw, s.TokenSource = getenv("SCUT_TOKEN"), "env SCUT_TOKEN"
	case tokenFile != "":
		t, err := readTokenFile(tokenFile, home)
		if err != nil {
			return err
		}
		raw, s.TokenSource = t, "token_file "+tokenFile
	}
	var kerr error
	if raw == "" && o.Keyring != nil {
		raw, kerr = o.Keyring.Get(keyringService, s.Profile)
		if kerr == nil && raw != "" {
			s.TokenSource = "keyring"
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		msg := fmt.Sprintf("no token for profile %q", s.Profile)
		if kerr != nil {
			msg += fmt.Sprintf(" (keyring: %v)", kerr)
		}
		return &exit.Error{Code: exit.Auth, Msg: msg,
			Suggestion: "use --token-file, set SCUT_TOKEN, set token_file in the user file, or store the token in the keyring"}
	}
	var err error
	if s.Scopes, s.Workspace, err = ParseToken(raw); err != nil {
		return fmt.Errorf("token from %s: %w", s.TokenSource, err)
	}
	s.Token = raw
	return nil
}

func readTokenFile(p, home string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[1:])
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", exit.Usagef("cannot read token file: %v", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// readLayer reads one file. A missing file gives an empty layer.
func readLayer(path, profile string, repo bool) (layer, error) {
	l := layer{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, exit.Usagef("cannot read %s: %v", path, err)
	}
	var d fileData
	md, err := toml.Decode(string(b), &d)
	if err != nil {
		return l, exit.Usagef("%s: %v", path, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		keys := make([]string, len(un))
		for i, k := range un {
			keys[i] = k.String()
		}
		sort.Strings(keys)
		return l, exit.Usagef("%s: unknown key %s", path, strings.Join(keys, ", "))
	}
	l.values = d.values
	if p, ok := d.Profile[profile]; ok {
		l.values = overlay(l.values, p)
	}
	all := []values{d.values}
	for _, p := range d.Profile {
		all = append(all, p)
	}
	for _, v := range all {
		if v.Token != "" {
			return l, exit.Usagef("%s: key token is not allowed, use token_file in the user file", path)
		}
		if repo && v.TokenFile != "" {
			return l, exit.Usagef("%s: key token_file is not allowed in a repo file, put it in the user file", path)
		}
	}
	return l, nil
}

func overlay(base, p values) values {
	base.Team = first(p.Team, base.Team)
	base.State = first(p.State, base.State)
	base.Workflow = first(p.Workflow, base.Workflow)
	base.Requester = first(p.Requester, base.Requester)
	base.Owner = first(p.Owner, base.Owner)
	base.Output = first(p.Output, base.Output)
	base.Token = first(p.Token, base.Token)
	base.TokenFile = first(p.TokenFile, base.TokenFile)
	f := map[string]string{}
	for k, v := range base.Fields {
		f[k] = v
	}
	for k, v := range p.Fields {
		f[k] = v
	}
	base.Fields = f
	return base
}

// findRepoFile looks for .scut.toml from dir up to the dir that has .git.
// Without a .git, it looks in dir only.
func findRepoFile(dir string) string {
	dir, _ = filepath.Abs(dir)
	root := dir
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			root = d
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	for d := dir; ; d = filepath.Dir(d) {
		p := filepath.Join(d, ".scut.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if d == root || filepath.Dir(d) == d {
			return ""
		}
	}
}

func first(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
