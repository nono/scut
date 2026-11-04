package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/resolve"
)

// memberOut is a member in compact output. MentionName is null when the
// member is not in the cache.
type memberOut struct {
	ID          string  `json:"id"`
	MentionName *string `json:"mention_name"`
}

// TaskOut is a checklist item.
type TaskOut struct {
	ID          int64  `json:"id"`
	Description string `json:"description"`
	Complete    bool   `json:"complete"`
}

// LinkOut is a story link. Subject and Object are story IDs.
type LinkOut struct {
	ID      int64  `json:"id"`
	Verb    string `json:"verb"`
	Subject int64  `json:"subject"`
	Object  int64  `json:"object"`
}

// CommentOut is a comment. A deleted comment has only ID and Deleted.
type CommentOut struct {
	ID        int64      `json:"id"`
	Deleted   bool       `json:"deleted,omitempty"`
	Author    *memberOut `json:"author,omitempty"`
	CreatedAt string     `json:"created_at,omitempty"`
	Text      string     `json:"text,omitempty"`
}

// StoryOut is the compact story shape. A key is absent when the API did not
// send the matching field, so --fields narrows the output. A nested list that
// the API cut short has a <key>_total count.
type StoryOut struct {
	ID            int64               `json:"id"`
	Name          string              `json:"name,omitempty"`
	Type          string              `json:"type,omitempty"`
	State         *idName             `json:"state,omitempty"`
	Owners        []memberOut         `json:"owners,omitzero"`
	OwnersTotal   int                 `json:"owners_total,omitempty"`
	Requester     *memberOut          `json:"requester,omitempty"`
	Epic          *idName             `json:"epic,omitempty"`
	Labels        []idName            `json:"labels,omitzero"`
	LabelsTotal   int                 `json:"labels_total,omitempty"`
	Fields        map[string]any      `json:"fields,omitzero"`
	FieldIDs      map[string]fieldIDs `json:"field_ids,omitzero"`
	FieldsTotal   int                 `json:"fields_total,omitempty"`
	Description   *string             `json:"description,omitempty"`
	Tasks         []TaskOut           `json:"tasks,omitzero"`
	TasksTotal    int                 `json:"tasks_total,omitempty"`
	Links         []LinkOut           `json:"links,omitzero"`
	LinksTotal    int                 `json:"links_total,omitempty"`
	Branches      []idName            `json:"branches,omitzero"`
	BranchesTotal int                 `json:"branches_total,omitempty"`
	PullRequests  []idName            `json:"pull_requests,omitzero"`
	PRsTotal      int                 `json:"pull_requests_total,omitempty"`
	Comments      []CommentOut        `json:"comments,omitzero"`
	CommentsTotal int                 `json:"comments_total,omitempty"`
	AppURL        string              `json:"app_url,omitempty"`
}

// fieldIDs holds the IDs of a custom field value, as the API takes them.
type fieldIDs struct {
	FieldID string `json:"field_id"`
	ValueID string `json:"value_id"`
}

// shapeOpts selects the heavy parts of a story.
type shapeOpts struct{ Description, Comments bool }

type nested[T any] struct {
	CurrentItems int `json:"current_items"`
	TotalItems   int `json:"total_items"`
	Entities     []T `json:"entities"`
}

// total returns the count to report, or 0 when the list is complete.
func (n *nested[T]) total() int {
	if n != nil && n.TotalItems > n.CurrentItems {
		return n.TotalItems
	}
	return 0
}

// items returns the entities, never nil for a list that is present.
func (n *nested[T]) items() []T {
	if n == nil {
		return nil
	}
	if n.Entities == nil {
		return []T{}
	}
	return n.Entities
}

type memberIn struct {
	ID string `json:"id"`
}

type commentIn struct {
	ID        int64     `json:"id"`
	Deleted   bool      `json:"deleted"`
	Author    *memberIn `json:"author"`
	CreatedAt string    `json:"created_at"`
	Text      *string   `json:"text"`
}

type storyIn struct {
	ID            int64              `json:"id"`
	Name          string             `json:"name"`
	StoryType     string             `json:"story_type"`
	WorkflowState *idName            `json:"workflow_state"`
	Owners        *nested[memberIn]  `json:"owners"`
	Requester     *memberIn          `json:"requester"`
	Epic          *idName            `json:"epic"`
	Labels        *nested[idName]    `json:"labels"`
	CustomFields  *nested[customIn]  `json:"custom_field_values"`
	Description   *string            `json:"description"`
	Tasks         *nested[taskIn]    `json:"checklist_items"`
	Links         *nested[linkIn]    `json:"story_links"`
	Branches      *nested[idName]    `json:"branches"`
	PullRequests  *nested[idName]    `json:"pull_requests"`
	Comments      *nested[commentIn] `json:"comments"`
	AppURL        string             `json:"app_url"`
}

type customIn struct {
	ID    string `json:"id"`
	Field struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"field"`
	Value any `json:"value"`
}

type taskIn struct {
	ID          int64  `json:"id"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

type linkIn struct {
	ID      int64  `json:"id"`
	Verb    string `json:"verb"`
	Subject struct {
		ID int64 `json:"id"`
	} `json:"subject"`
	Object struct {
		ID int64 `json:"id"`
	} `json:"object"`
}

// names maps member IDs to mention names.
type names map[string]string

func (n names) member(m *memberIn) *memberOut {
	if m == nil {
		return nil
	}
	out := &memberOut{ID: m.ID}
	if s, ok := n[m.ID]; ok {
		out.MentionName = &s
	}
	return out
}

func memberNames(r *resolve.R, ids []string) (names, error) {
	if len(ids) == 0 {
		return names{}, nil
	}
	m, err := r.Names("members", ids...)
	return names(m), err
}

func (n names) comment(c commentIn) CommentOut {
	if c.Deleted {
		return CommentOut{ID: c.ID, Deleted: true}
	}
	out := CommentOut{ID: c.ID, Author: n.member(c.Author), CreatedAt: c.CreatedAt}
	if c.Text != nil {
		out.Text = *c.Text
	}
	return out
}

func authorIDs(cs []commentIn) []string {
	var ids []string
	for _, c := range cs {
		if c.Author != nil && !c.Deleted {
			ids = append(ids, c.Author.ID)
		}
	}
	return ids
}

// shapeComments converts full Comment entities to the compact shape.
func shapeComments(raw []json.RawMessage, r *resolve.R) ([]CommentOut, error) {
	cs, err := decodeAll[commentIn](raw, "comment")
	if err != nil {
		return nil, err
	}
	nm, err := memberNames(r, authorIDs(cs))
	if err != nil {
		return nil, err
	}
	out := make([]CommentOut, 0, len(cs))
	for _, c := range cs {
		out = append(out, nm.comment(c))
	}
	return out, nil
}

// shapeStory converts a Story entity to the compact shape.
func shapeStory(raw json.RawMessage, r *resolve.R, opts shapeOpts) (StoryOut, error) {
	var in storyIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return StoryOut{}, &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("bad story data: %v", err)}
	}
	var ids []string
	if in.Owners != nil {
		for _, o := range in.Owners.Entities {
			ids = append(ids, o.ID)
		}
	}
	if in.Requester != nil {
		ids = append(ids, in.Requester.ID)
	}
	if opts.Comments && in.Comments != nil {
		ids = append(ids, authorIDs(in.Comments.Entities)...)
	}
	nm, err := memberNames(r, ids)
	if err != nil {
		return StoryOut{}, err
	}

	out := StoryOut{
		ID: in.ID, Name: in.Name, Type: in.StoryType, State: in.WorkflowState,
		Requester: nm.member(in.Requester), Epic: in.Epic, AppURL: in.AppURL,
		OwnersTotal: in.Owners.total(), LabelsTotal: in.Labels.total(), FieldsTotal: in.CustomFields.total(),
		TasksTotal: in.Tasks.total(), LinksTotal: in.Links.total(), BranchesTotal: in.Branches.total(),
		PRsTotal: in.PullRequests.total(),
	}
	if in.Owners != nil {
		out.Owners = []memberOut{}
		for _, o := range in.Owners.Entities {
			out.Owners = append(out.Owners, *nm.member(&o))
		}
	}
	if in.Labels != nil {
		out.Labels = in.Labels.items()
	}
	if in.CustomFields != nil {
		out.Fields = map[string]any{}
		out.FieldIDs = map[string]fieldIDs{}
		for _, c := range in.CustomFields.Entities {
			out.Fields[c.Field.Name] = c.Value
			out.FieldIDs[c.Field.Name] = fieldIDs{FieldID: c.Field.ID, ValueID: c.ID}
		}
	}
	if opts.Description {
		out.Description = in.Description
	}
	if in.Tasks != nil {
		out.Tasks = []TaskOut{}
		for _, t := range in.Tasks.Entities {
			out.Tasks = append(out.Tasks, TaskOut{t.ID, t.Description, t.Completed})
		}
	}
	if in.Links != nil {
		out.Links = []LinkOut{}
		for _, l := range in.Links.Entities {
			out.Links = append(out.Links, LinkOut{l.ID, l.Verb, l.Subject.ID, l.Object.ID})
		}
	}
	out.Branches = in.Branches.items()
	out.PullRequests = in.PullRequests.items()
	if opts.Comments && in.Comments != nil {
		out.CommentsTotal = in.Comments.total()
		out.Comments = []CommentOut{}
		for _, c := range in.Comments.Entities {
			out.Comments = append(out.Comments, nm.comment(c))
		}
	}
	return out, nil
}

// Human is the text form: name, state, owners, URL, description, comments.
func (s StoryOut) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (sc-%d)\n", s.Name, s.ID)
	if s.State != nil {
		fmt.Fprintf(&b, "State: %s\n", s.State.Name)
	}
	if len(s.Owners) > 0 {
		var o []string
		for _, m := range s.Owners {
			o = append(o, m.label())
		}
		fmt.Fprintf(&b, "Owners: %s%s\n", strings.Join(o, ", "), ofTotal(len(o), s.OwnersTotal))
	}
	if s.AppURL != "" {
		fmt.Fprintf(&b, "%s\n", s.AppURL)
	}
	if s.Description != nil && *s.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", *s.Description)
	}
	if s.CommentsTotal > 0 {
		fmt.Fprintf(&b, "\nComments%s\n", ofTotal(len(s.Comments), s.CommentsTotal))
	}
	for _, c := range s.Comments {
		switch {
		case c.Deleted:
			fmt.Fprintf(&b, "\n[comment %d deleted]\n", c.ID)
		case c.Author != nil:
			fmt.Fprintf(&b, "\n%s %s\n%s\n", c.Author.label(), c.CreatedAt, c.Text)
		default:
			fmt.Fprintf(&b, "\n%s\n%s\n", c.CreatedAt, c.Text)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ofTotal returns " (n of total)" for a list that the API cut short, else "".
func ofTotal(n, total int) string {
	if total <= n {
		return ""
	}
	return fmt.Sprintf(" (%d of %d)", n, total)
}

func (m memberOut) label() string {
	if m.MentionName != nil {
		return "@" + *m.MentionName
	}
	return m.ID
}

// storyList is several stories; its text form separates them by a blank line.
type storyList []StoryOut

// Human joins the text forms of the stories.
func (l storyList) Human() string {
	parts := make([]string, len(l))
	for i, s := range l {
		parts[i] = s.Human()
	}
	return strings.Join(parts, "\n\n")
}
