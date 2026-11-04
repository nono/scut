package cli

import "github.com/nono/scut/internal/exit"

// linkVerbs maps a CLI link verb to the API verb. With swap, the story that
// the command acts on is the object of the link, not the subject.
var linkVerbs = map[string]struct {
	api  string
	swap bool
}{
	"blocks":        {"blocks", false},
	"blocked-by":    {"blocks", true},
	"relates-to":    {"relates to", false},
	"duplicates":    {"duplicates", false},
	"duplicated-by": {"duplicates", true},
}

// linkVerb translates a CLI link verb.
func linkVerb(cli string) (api string, swap bool, err error) {
	v, ok := linkVerbs[cli]
	if !ok {
		return "", false, &exit.Error{
			Code:  exit.Usage,
			Msg:   "unknown link verb " + `"` + cli + `"`,
			Valid: []string{"blocks", "blocked-by", "relates-to", "duplicates", "duplicated-by"},
		}
	}
	return v.api, v.swap, nil
}
