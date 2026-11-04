package cli

import (
	"path/filepath"
	"testing"
)

// cache clear removes only <cache>/scut/<slug>.
func TestCheckCacheDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "cache")
	if err := checkCacheDir(filepath.Join(base, "scut", "acme"), "acme"); err != nil {
		t.Errorf("good dir: %v", err)
	}
	for _, tc := range []struct{ dir, slug string }{
		{base, ".."},
		{filepath.Join(base, "scut"), "."},
		{filepath.Join(base, "scut", "a", "b"), "a/b"},
		{filepath.Join(base, "scut", "A"), "A"},
		{filepath.Join(base, "other", "acme"), "acme"},
		{filepath.Join(base, "scut", "acme"), "beta"},
		{"scut/acme", "acme"},
	} {
		if err := checkCacheDir(tc.dir, tc.slug); err == nil {
			t.Errorf("%q, slug %q: want error", tc.dir, tc.slug)
		}
	}
}
