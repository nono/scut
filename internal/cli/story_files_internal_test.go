package cli

import "testing"

// The name with the file ID can be taken too: then -2 follows it.
func TestUniqueNameTwice(t *testing.T) {
	used := map[string]bool{}
	for _, tc := range []struct {
		name string
		id   int64
		want string
	}{{"a.txt", 1, "a.txt"}, {"a-2.txt", 3, "a-2.txt"}, {"a.txt", 2, "a-2-2.txt"}, {"a.txt", 2, "a-2-3.txt"}} {
		if got := uniqueName(tc.name, tc.id, used); got != tc.want {
			t.Errorf("%s (id %d): %s, want %s", tc.name, tc.id, got, tc.want)
		}
	}
}
