package output_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nono/scut/internal/output"
)

type human struct{ A int }

func (human) Human() string { return "text" }

func TestPrintJSON(t *testing.T) {
	var b bytes.Buffer
	if err := output.Print(&b, map[string]any{"a": 1, "u": "<&>"}, false); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": 1,\n  \"u\": \"<&>\"\n}\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestPrintRaw(t *testing.T) {
	var b bytes.Buffer
	if err := output.Print(&b, json.RawMessage(`{"a":[1]}`), false); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": [\n    1\n  ]\n}\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestPrintHuman(t *testing.T) {
	var b bytes.Buffer
	if err := output.Print(&b, human{1}, true); err != nil {
		t.Fatal(err)
	}
	if b.String() != "text\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestPrintHumanFallsBackToJSON(t *testing.T) {
	var b bytes.Buffer
	if err := output.Print(&b, map[string]int{"a": 1}, true); err != nil {
		t.Fatal(err)
	}
	if b.String() != "{\n  \"a\": 1\n}\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestPrintJSONIgnoresHuman(t *testing.T) {
	var b bytes.Buffer
	if err := output.Print(&b, human{1}, false); err != nil {
		t.Fatal(err)
	}
	if b.String() != "{\n  \"A\": 1\n}\n" {
		t.Errorf("got %q", b.String())
	}
}
