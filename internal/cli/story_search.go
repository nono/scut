package cli

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/transport"
)

const (
	defaultLimit = 25
	maxPageSize  = 100
)

// parseLimit returns the item limit, -1 for "all".
func parseLimit(s string) (int, error) {
	if s == "all" {
		return -1, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, exit.Usagef("--limit takes a number above 0 or \"all\", not %q", s)
	}
	return n, nil
}

// pageSize is the page size to ask for when the caller wants limit items.
func pageSize(limit int) int {
	if limit < 0 {
		return maxPageSize
	}
	return min(defaultLimit, limit)
}

// listPages gets up to limit items (all with limit < 0) from a list endpoint.
func listPages(cmd *cobra.Command, d *deps, path string, q url.Values, limit int) ([]json.RawMessage, error) {
	q.Set("limit", strconv.Itoa(pageSize(limit)))
	return transport.PageQuery(cmd.Context(), d.Doer, path, q, limit)
}

// listStoryFields returns the API field names to ask for in a story list. With
// no --fields it leaves out description and comments, which are heavy.
func listStoryFields(list string) ([]string, error) {
	if list != "" {
		return apiFields(list)
	}
	var out []string
	for _, f := range storyFieldNames {
		if f == "description" || f == "comments" {
			continue
		}
		if a, ok := apiFieldNames[f]; ok {
			f = a
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out, nil
}

// listShapeOpts turns on description and comments when --fields asks for
// them. With no --fields they stay off, so the list stays small.
func listShapeOpts(list string, fields []string) shapeOpts {
	if list == "" {
		return shapeOpts{}
	}
	return shapeOpts{Description: slices.Contains(fields, "description"), Comments: slices.Contains(fields, "comments")}
}

// shapeStories shapes a list of Story entities.
func shapeStories(d *deps, raw []json.RawMessage, opts shapeOpts) (storyList, error) {
	out := make(storyList, 0, len(raw))
	for _, r := range raw {
		s, err := shapeStory(r, d.Resolver, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func newStorySearch(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search stories",
		Long: "Search stories with a Shortcut search query, sent as is.\n" +
			"Example: scut story search '!is:done epic:5387'\n\n" +
			"The search index is eventually consistent: search can lag; use story view for current data.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "searchStories,listMembers"},
	}
	limitFlag := cmd.Flags().String("limit", strconv.Itoa(defaultLimit), "maximum number of stories, or all")
	fieldsFlag := cmd.Flags().String("fields", "", "comma-separated fields to show")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		limit, err := parseLimit(*limitFlag)
		if err != nil {
			return err
		}
		fields, err := listStoryFields(*fieldsFlag)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			q := url.Values{"query": {args[0]}}
			fieldsQuery(q, fields)
			raw, stop := listPages(cmd, d, "stories/search", q, limit)
			if raw == nil && stop != nil {
				return stop
			}
			out, err := shapeStories(d, raw, listShapeOpts(*fieldsFlag, fields))
			if err != nil {
				return err
			}
			if err := d.Out(out); err != nil {
				return err
			}
			return stop // a list cut short: the items read, then the error
		})(cmd, args)
	}
	return cmd
}
