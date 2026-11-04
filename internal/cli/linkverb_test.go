package cli

import "testing"

func TestLinkVerb(t *testing.T) {
	for _, c := range []struct {
		cli, api string
		swap     bool
	}{
		{"blocks", "blocks", false},
		{"blocked-by", "blocks", true},
		{"relates-to", "relates to", false},
		{"duplicates", "duplicates", false},
		{"duplicated-by", "duplicates", true},
	} {
		api, swap, err := linkVerb(c.cli)
		if err != nil || api != c.api || swap != c.swap {
			t.Errorf("%s: got %q, %v, %v", c.cli, api, swap, err)
		}
	}
	if _, _, err := linkVerb("eats"); err == nil {
		t.Error("unknown verb: no error")
	}
}
