package cli

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
	"github.com/nono/scut/internal/transport"
)

const defaultComments = 10

// storyCmd returns the story group with its subcommands.
func storyCmd(env Env) *cobra.Command {
	g := Group("story", "View, search and change stories")
	g.AddCommand(newStoryView(env), newStorySearch(env), newStoryComments(env), newStoryHistory(env), newStoryComment(env), newStoryUpdate(env), newStoryCreate(env), newStoryLink(env), newStoryUnlink(env), newStoryRelink(env), newStoryBranch(env), newStoryFiles(env), newStorySubtasks(env))
	return g
}

// apiFieldNames maps --fields names to API field names.
var apiFieldNames = map[string]string{
	"state":     "workflow_state",
	"type":      "story_type",
	"tasks":     "checklist_items",
	"fields":    "custom_field_values",
	"field_ids": "custom_field_values",
	"links":     "story_links",
}

// storyFieldNames are the --fields names that StoryOut can show. fields and
// field_ids both ask for custom_field_values, so each gives both keys.
var storyFieldNames = []string{"app_url", "branches", "comments", "description", "epic", "field_ids", "fields", "id", "labels",
	"links", "name", "owners", "pull_requests", "requester", "state", "tasks", "type"}

const fieldsUsage = "comma-separated fields to show: output names, API Story field names (estimate, deadline, …), " +
	"or the ID names of the updateStory body (epic_id, owner_ids, …)"

// apiName returns the API field name of a --fields name.
func apiName(f string) string {
	if a, ok := apiFieldNames[f]; ok {
		return a
	}
	return f
}

// validFields returns the names that --fields takes: the CLI names, the
// properties of the Story schema and the ID names.
func validFields() []string {
	v := append(slices.Clone(storyFieldNames), apiStoryFields...)
	v = slices.AppendSeq(v, maps.Keys(idFieldNames))
	slices.Sort(v)
	return slices.Compact(v)
}

// extraFields returns the API fields that StoryOut does not model. The
// output prints them as the API sent them.
func extraFields(fields []string) []string {
	var out []string
	for _, f := range fields {
		if !slices.ContainsFunc(storyFieldNames, func(n string) bool { return apiName(n) == f }) {
			out = append(out, f)
		}
	}
	return out
}

// apiFields maps a --fields list to the API names and keeps id. It takes the
// CLI names, the API names and the ID names. An unknown name is a usage error.
func apiFields(list string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(list, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if a, ok := idFieldNames[f]; ok {
			f = a
		}
		if !slices.Contains(storyFieldNames, f) && !slices.Contains(apiStoryFields, f) {
			return nil, withValues(exit.Usagef("unknown field %q", f), "fields", validFields())
		}
		f = apiName(f)
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	if !slices.Contains(out, "id") {
		out = append([]string{"id"}, out...)
	}
	return out, nil
}

// commentValues are the values of --comments.
var commentValues = []string{"<number>", "all", "none"}

// manyComments gives the fix of a --comments error with several stories:
// with no --comments, each story has its comments.
func manyComments(e *exit.Error) *exit.Error {
	e.Valid = []string{"none"}
	e.Suggestion = "leave out --comments to get the comments of each story"
	return e
}

// parseCommentCount returns the number of comments to fetch, -1 for all.
func parseCommentCount(s string) (int, error) {
	switch s {
	case "all":
		return -1, nil
	case "none":
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, withValues(exit.Usagef("--comments takes a number, \"all\" or \"none\", not %q", s), "comments", commentValues)
	}
	return n, nil
}

func newStoryView(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "view [<story>…]",
		Short:       "Show stories; default story from the git branch",
		Annotations: map[string]string{OpsAnnotation: "getStory,listStoryComments,listStoryChecklistItems,listStories,listMembers", IDArgsAnnotation: "sc-"},
	}
	fieldsFlag := valuesFlag(cmd.Flags(), "fields", "", "", fieldsUsage, validFields())
	commentsFlag := valuesFlag(cmd.Flags(), "comments", "", strconv.Itoa(defaultComments), "number of newest comments, all, or none", commentValues)
	// pflag stops at the flag with no value: the stories before it are known,
	// the stories after it are not.
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		ferr := c.Parent().FlagErrorFunc()(c, err)
		var ee *exit.Error
		if f := missingValue(err); f != nil && f.Name == "comments" && len(c.Flags().Args()) > 1 && errors.As(ferr, &ee) {
			return manyComments(ee)
		}
		return ferr
	})
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		nComments, err := parseCommentCount(*commentsFlag)
		if len(args) > 1 && (err != nil || nComments != 0) && cmd.Flags().Changed("comments") {
			return manyComments(exit.Usagef("--comments needs a single story"))
		}
		if err != nil {
			return err
		}
		ids, err := storyIDs(env, args)
		if err != nil {
			return err
		}
		uniq := uniqueIDs(ids)
		if len(uniq) > maxPageSize {
			return exit.Usagef("story view takes at most %d stories, not %d", maxPageSize, len(uniq))
		}
		var fields []string
		if *fieldsFlag != "" {
			if fields, err = apiFields(*fieldsFlag); err != nil {
				return err
			}
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			ctx := cmd.Context()
			if len(ids) > 1 {
				return viewMany(ctx, d, uniq, fields, nComments != 0)
			}
			return viewOne(ctx, d, ids[0], fields, nComments)
		})(cmd, args)
	}
	return cmd
}

// storyIDs parses the story arguments, or reads the ID from the git branch.
func storyIDs(env Env, args []string) ([]int64, error) {
	if len(args) == 0 {
		id, err := ref.FromGit(env.Dir)
		if err != nil {
			return nil, err
		}
		return []int64{id}, nil
	}
	ids := make([]int64, 0, len(args))
	for _, a := range args {
		id, err := ref.Story(a)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func fieldsQuery(q url.Values, fields []string) {
	if fields != nil {
		q.Set("fields", strings.Join(fields, ","))
	}
}

func viewOne(ctx context.Context, d *deps, id int64, fields []string, nComments int) error {
	q := url.Values{}
	fieldsQuery(q, fields)
	raw, err := d.Doer.Raw(ctx, "GET", "stories/"+strconv.FormatInt(id, 10), q, nil)
	if err != nil {
		return err
	}
	s, err := shapeStory(raw, d.Resolver, shapeOpts{Description: true, Extra: extraFields(fields)})
	if err != nil {
		return err
	}
	// The nested items have no completed: read the full list.
	if (fields == nil || slices.Contains(fields, "checklist_items")) && (len(s.Tasks) > 0 || s.TasksTotal > 0) {
		path := "stories/" + strconv.FormatInt(id, 10) + "/checklist-items"
		items, err := transport.PageQuery(ctx, d.Doer, path, url.Values{"limit": {strconv.Itoa(maxPageSize)}}, -1)
		if err != nil {
			return err
		}
		if s.Tasks, err = shapeTasks(items); err != nil {
			return err
		}
		s.TasksTotal = 0
	}
	if nComments != 0 && (fields == nil || slices.Contains(fields, "comments")) {
		cs, err := fetchComments(ctx, d, id, nComments)
		if err != nil {
			return err
		}
		s.Comments = cs
		var c struct {
			Comments *nested[json.RawMessage] `json:"comments"`
		}
		if json.Unmarshal(raw, &c) == nil && c.Comments != nil && c.Comments.TotalItems > len(cs) {
			s.CommentsTotal = c.Comments.TotalItems
		}
	}
	return d.Out(s)
}

// fetchComments gets the newest n comments, or all with n < 0.
func fetchComments(ctx context.Context, d *deps, id int64, n int) ([]CommentOut, error) {
	path := "stories/" + strconv.FormatInt(id, 10) + "/comments"
	q := url.Values{"order_dir": {"desc"}, "limit": {strconv.Itoa(pageSize(n))}}
	raw, err := transport.PageQuery(ctx, d.Doer, path, q, n)
	if err != nil {
		return nil, err
	}
	return shapeComments(raw, d.Resolver)
}

// uniqueIDs returns ids without repeats, in argument order.
func uniqueIDs(ids []int64) []int64 {
	var uniq []int64
	for _, id := range ids {
		if !slices.Contains(uniq, id) {
			uniq = append(uniq, id)
		}
	}
	return uniq
}

// viewMany gets several stories in one call. It prints the stories found, in
// argument order, then fails with exit 3 if some are missing. With comments
// false, it prints no comments.
func viewMany(ctx context.Context, d *deps, uniq []int64, fields []string, comments bool) error {
	q := storiesQuery(uniq)
	fieldsQuery(q, fields)
	raw, err := transport.PageQuery(ctx, d.Doer, "stories", q, -1)
	if err != nil {
		return err
	}
	byID := map[int64]StoryOut{}
	opts := shapeOpts{Description: true, Comments: comments, Extra: extraFields(fields)}
	for _, r := range raw {
		s, err := shapeStory(r, d.Resolver, opts)
		if err != nil {
			return err
		}
		byID[s.ID] = s
	}
	out := make(storyList, 0, len(uniq))
	for _, id := range uniq {
		if s, ok := byID[id]; ok {
			out = append(out, s)
		}
	}
	if err := d.Out(out); err != nil {
		return err
	}
	return noStory(uniq, byID)
}

// storiesQuery is the query of listStories for the stories ids, archived
// ones too.
func storiesQuery(ids []int64) url.Values {
	return url.Values{"ids": {joinIDs(ids, ",")}, "filter": {"include_archived"}, "limit": {strconv.Itoa(len(ids))}}
}

// noStory fails with exit 3 if some of ids are not keys of found.
func noStory[V any](ids []int64, found map[int64]V) error {
	if missing := idsNotIn(ids, found); len(missing) > 0 {
		return exit.NotFoundf("no story %s", joinIDs(missing, ", "))
	}
	return nil
}
