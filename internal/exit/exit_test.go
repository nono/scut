package exit_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/nono/scut/internal/exit"
)

func TestWriteError(t *testing.T) {
	var b bytes.Buffer
	code := exit.Write(&b, &exit.Error{Code: exit.NotFound, Msg: `no state "Todo"`, Valid: []string{"To Do"}})
	want := `{"error":"no state \"Todo\"","valid":["To Do"]}` + "\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
	if code != exit.NotFound {
		t.Errorf("code %d, want 3", code)
	}
}

func TestWriteAllFields(t *testing.T) {
	var b bytes.Buffer
	exit.Write(&b, &exit.Error{Code: exit.Other, Msg: "m", Status: 500, API: []byte(`{"a":1}`), Suggestion: "s", Valid: []string{"v"}})
	want := `{"error":"m","status":500,"api":{"a":1},"suggestion":"s","valid":["v"]}` + "\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestWriteWorktree(t *testing.T) {
	var b bytes.Buffer
	exit.Write(&b, &exit.Error{Code: exit.Unavailable, Msg: "m", Worktree: map[string]any{"dir": "/x"}})
	want := `{"error":"m","worktree":{"dir":"/x"}}` + "\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestWritePlainError(t *testing.T) {
	var b bytes.Buffer
	if code := exit.Write(&b, errors.New("x")); code != exit.Other {
		t.Errorf("code %d, want 1", code)
	}
	if b.String() != `{"error":"x"}`+"\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestConstructors(t *testing.T) {
	if e := exit.Usagef("bad %d", 1); e.Code != exit.Usage || e.Msg != "bad 1" || e.Error() != "bad 1" {
		t.Errorf("Usagef: %+v", e)
	}
	if e := exit.NotFoundf("no %s", "x"); e.Code != exit.NotFound || e.Msg != "no x" {
		t.Errorf("NotFoundf: %+v", e)
	}
}

func TestWrapped(t *testing.T) {
	var b bytes.Buffer
	err := errors.Join(errors.New("ctx"), exit.Usagef("u"))
	if code := exit.Write(&b, err); code != exit.Usage {
		t.Errorf("code %d, want 2", code)
	}
}
