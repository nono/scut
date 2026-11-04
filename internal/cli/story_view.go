package cli

import (
	"context"
	"encoding/json"
	"fmt"
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

// storyCmd returns the story group. Later tasks add subcommands to it.
func storyCmd(env Env) *cobra.Command {
	g := Group("story", "View, search and change stories")
	g.AddCommand(newStoryView(env), newStorySearch(env), newStoryComments(env), newStoryComment(env), newStoryUpdate(env), newStoryCreate(env), newStoryLink(env), newStoryUnlink(env), newStoryRelink(env), newStoryBranch(env), newStoryFiles(env))
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

const fieldsUsage = "comma-separated fields to show: output names or API Story field names (estimate, deadline, …)"

// apiName returns the API field name of a --fields name.
func apiName(f string) string {
	if a, ok := apiFieldNames[f]; ok {
		return a
	}
	return f
}

// validFields returns the names that --fields takes: the CLI names and the
// properties of the Story schema.
func validFields() []string {
	v := append(slices.Clone(storyFieldNames), apiStoryFields...)
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
// CLI names and the API names. An unknown name is a usage error.
func apiFields(list string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(list, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !slices.Contains(storyFieldNames, f) && !slices.Contains(apiStoryFields, f) {
			return nil, &exit.Error{Code: exit.Usage, Msg: fmt.Sprintf("unknown field %q", f), Valid: validFields()}
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

// parseCommentCount returns the number of comments to fetch, -1 for all.
func parseCommentCount(s string) (int, error) {
	if s == "all" {
		return -1, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, exit.Usagef("--comments takes a number or \"all\", not %q", s)
	}
	return n, nil
}

func newStoryView(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "view [<story>…]",
		Short:       "Show stories; default story from the git branch",
		Annotations: map[string]string{OpsAnnotation: "getStory,listStoryComments,listStoryChecklistItems,listStories,listMembers"},
	}
	fieldsFlag := cmd.Flags().String("fields", "", fieldsUsage)
	commentsFlag := cmd.Flags().String("comments", strconv.Itoa(defaultComments), "number of newest comments, or all")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		nComments, err := parseCommentCount(*commentsFlag)
		if err != nil {
			return err
		}
		if len(args) > 1 && cmd.Flags().Changed("comments") {
			return exit.Usagef("--comments needs a single story")
		}
		ids, err := storyIDs(env, args)
		if err != nil {
			return err
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
				return viewMany(ctx, d, ids, fields)
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
	if (fields == nil || slices.Contains(fields, "checklist_items")) && hasTasks(raw) {
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

// viewMany gets several stories in one call. It prints the stories found, in
// argument order, then fails with exit 3 if some are missing.
func viewMany(ctx context.Context, d *deps, ids []int64, fields []string) error {
	var uniq []int64
	strs := []string{}
	for _, id := range ids {
		if !slices.Contains(uniq, id) {
			uniq = append(uniq, id)
			strs = append(strs, strconv.FormatInt(id, 10))
		}
	}
	if len(uniq) > maxPageSize {
		return exit.Usagef("story view takes at most %d stories, not %d", maxPageSize, len(uniq))
	}
	q := url.Values{"ids": {strings.Join(strs, ",")}, "filter": {"include_archived"}, "limit": {strconv.Itoa(len(uniq))}}
	fieldsQuery(q, fields)
	raw, err := transport.PageQuery(ctx, d.Doer, "stories", q, -1)
	if err != nil {
		return err
	}
	byID := map[int64]StoryOut{}
	for _, r := range raw {
		s, err := shapeStory(r, d.Resolver, shapeOpts{Description: true, Comments: true, Extra: extraFields(fields)})
		if err != nil {
			return err
		}
		byID[s.ID] = s
	}
	out := make(storyList, 0, len(uniq))
	var missing []string
	for _, id := range uniq {
		if s, ok := byID[id]; ok {
			out = append(out, s)
		} else {
			missing = append(missing, strconv.FormatInt(id, 10))
		}
	}
	if err := d.Out(out); err != nil {
		return err
	}
	if len(missing) > 0 {
		return exit.NotFoundf("no story %s", strings.Join(missing, ", "))
	}
	return nil
}
