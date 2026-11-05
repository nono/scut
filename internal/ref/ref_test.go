package ref_test

import (
	"errors"
	"testing"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
	"github.com/nono/scut/internal/testgit"
)

func TestStoryRef(t *testing.T) {
	for in, want := range map[string]int64{
		"sc-5409": 5409,
		"5409":    5409,
		"https://app.shortcut.com/example-ws/story/5409/some-slug": 5409,
		"https://app.shortcut.com/acme/story/12":                   12,
		"bug/sc-5409/fix-wave":                                     5409,
		"user42/sc-17-x":                                           17,
		"fix/misc-12/sc-5":                                         5,
	} {
		got, err := ref.Story(in)
		if err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", in, got, err, want)
		}
	}
}

func TestStoryRefBad(t *testing.T) {
	for _, in := range []string{"abc", "sc-", "54a", "", "fix/misc-12-typo", "https://app.shortcut.com/acme/epic/5", "99999999999999999999"} {
		_, err := ref.Story(in)
		var e *exit.Error
		if !errors.As(err, &e) || e.Code != exit.Usage {
			t.Errorf("%q: got %v, want usage error", in, err)
		}
	}
}

const docID = "1b4e28ba-2fa1-41d2-883f-0016d3cca427"

func TestDocRef(t *testing.T) {
	for in, want := range map[string]string{
		docID:                                   docID,
		" 1B4E28BA-2FA1-41D2-883F-0016D3CCA427": docID, //nolint:gocritic // the space is part of the test
		"https://app.shortcut.com/acme/write/IkRvYyI6I3V1aWQgIjFiNGUyOGJhLTJmYTEtNDFkMi04ODNmLTAwMTZkM2NjYTQyNyI=":       docID,
		"https://app.shortcut.com/acme/write/IkRvYyI6I3V1aWQgIjFiNGUyOGJhLTJmYTEtNDFkMi04ODNmLTAwMTZkM2NjYTQyNyI":        docID,
		"https://app.shortcut.com/acme/write/IkRvYyI6I3V1aWQgIjFiNGUyOGJhLTJmYTEtNDFkMi04ODNmLTAwMTZkM2NjYTQyNyI=?x=1#y": docID,
		"https://api.app.shortcut.com/api/v4/acme/documents/" + docID:                                                    docID,
		"https://api.app.shortcut.com/api/v4/acme/documents/1B4E28BA-2FA1-41D2-883F-0016D3CCA427":                        docID,
		"HTTPS://app.shortcut.com/acme/write/IkRvYyI6I3V1aWQgIjFiNGUyOGJhLTJmYTEtNDFkMi04ODNmLTAwMTZkM2NjYTQyNyI=":       docID,
	} {
		id, title, err := ref.Doc(in)
		if err != nil || title != "" || id != want {
			t.Errorf("%q: got %q, %q, %v; want %q", in, id, title, err, want)
		}
	}
}

func TestDocRefTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Picking spec":    "Picking spec",
		" Picking spec\n": "Picking spec", // trimmed, as an ID or a URL is
		"1b4e28ba":        "1b4e28ba",
		"write/x":         "write/x",
	} {
		id, title, err := ref.Doc(in)
		if err != nil || id != "" || title != want {
			t.Errorf("%q: got %q, %q, %v; want title %q", in, id, title, err, want)
		}
	}
}

func TestDocRefBad(t *testing.T) {
	for _, in := range []string{
		"https://app.shortcut.com/acme/write/bm90IGEgZG9j",                                                         // base64 of "not a doc"
		"https://app.shortcut.com/acme/write/IkVwaWMiOiN1dWlkICIxYjRlMjhiYS0yZmExLTQxZDItODgzZi0wMDE2ZDNjY2E0Mjci", // "Epic":#uuid …
		"https://app.shortcut.com/acme/story/12",
		"https://api.app.shortcut.com/api/v4/acme/members/" + docID, // a UUID, but not of a doc
		"https://app.shortcut.com/acme/write/" + docID,
		"https://app.shortcut.com/",
		"", " \t\n", // blank
	} {
		_, _, err := ref.Doc(in)
		var e *exit.Error
		if !errors.As(err, &e) || e.Code != exit.Usage {
			t.Errorf("%q: got %v, want usage error", in, err)
		}
	}
}

func TestFromGitOutsideRepo(t *testing.T) {
	_, err := ref.FromGit(t.TempDir())
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Usage || e.Msg != "give a story ID" {
		t.Errorf("got %v", err)
	}
}

func TestFromGitBranch(t *testing.T) {
	id, err := ref.FromGit(testgit.Repo(t, "bug/sc-5409/x"))
	if err != nil || id != 5409 {
		t.Errorf("got %d, %v; want 5409", id, err)
	}
}

func TestFromGitDetached(t *testing.T) {
	dir := testgit.Repo(t, "bug/sc-5409/x")
	testgit.Git(t, dir, "checkout", "-q", "--detach")
	_, err := ref.FromGit(dir)
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Usage || e.Msg != "give a story ID" {
		t.Errorf("got %v", err)
	}
}

// misc-1234 is not story 1234: sc- must not follow a letter or a digit.
func TestFromGitNoLeftBoundary(t *testing.T) {
	_, err := ref.FromGit(testgit.Repo(t, "fix/misc-1234-typo"))
	var e *exit.Error
	if !errors.As(err, &e) || e.Code != exit.Usage || e.Msg != "give a story ID" {
		t.Errorf("got %v", err)
	}
}

func TestHasID(t *testing.T) {
	for _, c := range []struct {
		b    string
		want bool
	}{{"bug/sc-12/x", true}, {"bug/sc-123/x", false}, {"sc-12", true}, {"main", false},
		{"fix/misc-12-typo", false}, {"disc-12", false}, {"a-sc-12", true}, {"misc-12/sc-12", true}} {
		if got := ref.HasID(c.b, 12); got != c.want {
			t.Errorf("HasID(%q, 12) = %v", c.b, got)
		}
	}
}
