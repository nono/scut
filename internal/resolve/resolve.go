// Package resolve turns names into API IDs.
// A name match is exact and case-sensitive. On a miss in cached data, the
// cache is refreshed once before the lookup fails.
package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nono/scut/internal/cache"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
	"github.com/nono/scut/internal/transport"
)

const pageSize = "100"

var (
	digitRe = regexp.MustCompile(`^\d+$`)
)

// nestedFields lists, for each cached kind, the nested lists to complete.
var nestedFields = map[string][]string{
	"teams":     {"workflows"},
	"workflows": {"workflow_states"},
}

// R resolves names with the cache, and the API on a miss.
type R struct {
	d   *transport.Doer
	c   *cache.Store
	ctx context.Context
}

// New returns a resolver.
func New(d *transport.Doer, c *cache.Store) *R {
	return &R{d: d, c: c, ctx: context.Background()}
}

// WithContext returns a copy of r whose API calls use ctx.
func (r *R) WithContext(ctx context.Context) *R {
	cp := *r
	cp.ctx = ctx
	return &cp
}

// entity holds the fields that lookups read. The cache keeps the raw JSON.
type entity struct {
	ID              json.RawMessage `json:"id"`
	Name            string          `json:"name"`
	MentionName     string          `json:"mention_name"`
	Email           string          `json:"email_address"`
	DefaultWorkflow struct {
		ID json.RawMessage `json:"id"`
	} `json:"default_workflow"`
	Workflows      nestedList `json:"workflows"`
	WorkflowStates nestedList `json:"workflow_states"`
	Values         []struct {
		ID    json.RawMessage `json:"id"`
		Value string          `json:"value"`
	} `json:"values"`
}

type nestedList struct {
	CurrentItems int               `json:"current_items"`
	TotalItems   int               `json:"total_items"`
	Entities     []json.RawMessage `json:"entities"`
	ListURL      string            `json:"list_url"`
}

func idString(raw json.RawMessage) string { return strings.Trim(string(raw), `"`) }

func idInt(raw json.RawMessage) (int64, error) {
	return strconv.ParseInt(idString(raw), 10, 64)
}

// pageAll reads every page of a list endpoint.
func (r *R) pageAll(path string, q url.Values) ([]json.RawMessage, error) {
	return transport.PageQuery(r.ctx, r.d, path, q, -1)
}

// fetch gets the whole list of kind and stores it in the cache.
func (r *R) fetch(kind string) ([]json.RawMessage, error) {
	at := r.c.Now() // before the call: a cache clear during it makes the data stale
	items, err := r.pageAll("/"+kind, url.Values{"limit": {pageSize}})
	if err != nil {
		return nil, err
	}
	for i, it := range items {
		if items[i], err = r.complete(it, nestedFields[kind]); err != nil {
			return nil, err
		}
	}
	if items == nil {
		items = []json.RawMessage{} // store [] (a fresh empty list), not null
	}
	_ = r.c.Put(kind, items, at) // a cache that cannot be written must not fail the lookup
	return items, nil
}

// complete follows list_url of each nested list that is cut short.
func (r *R) complete(item json.RawMessage, fields []string) (json.RawMessage, error) {
	if len(fields) == 0 {
		return item, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(item, &m); err != nil {
		return item, nil //nolint:nilerr // not an object: no nested list to complete
	}
	changed := false
	for _, f := range fields {
		var n nestedList
		if json.Unmarshal(m[f], &n) != nil || n.CurrentItems >= n.TotalItems || n.ListURL == "" {
			continue
		}
		u, err := url.Parse(n.ListURL)
		if err != nil {
			return nil, &exit.Error{Code: exit.Other, Msg: "bad list_url"}
		}
		all, err := r.pageAll(u.Path, u.Query())
		if err != nil {
			return nil, err
		}
		var mm map[string]json.RawMessage
		if json.Unmarshal(m[f], &mm) != nil {
			continue
		}
		mm["entities"], _ = json.Marshal(all)
		m[f], _ = json.Marshal(mm)
		changed = true
	}
	if !changed {
		return item, nil
	}
	return json.Marshal(m)
}

// load returns the entities of kind. fetched is true when they came from the API.
func (r *R) load(kind string, force bool) (es []entity, fetched bool, err error) {
	var raw []json.RawMessage
	if !force {
		if fresh, err := r.c.Get(kind, &raw); err != nil {
			return nil, false, err
		} else if !fresh {
			raw = nil
		}
	}
	if raw == nil {
		if raw, err = r.fetch(kind); err != nil {
			return nil, false, err
		}
		fetched = true
	}
	es = make([]entity, 0, len(raw))
	for _, b := range raw {
		var e entity
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, false, &exit.Error{Code: exit.Other, Msg: "bad " + kind + " data"}
		}
		es = append(es, e)
	}
	return es, fetched, nil
}

// lookup runs try on the entities of kind. A not-found error from try on
// cached data makes one refresh and one more try. Other errors end it.
func lookup[T any](r *R, kind string, try func([]entity) (T, error)) (T, error) {
	var zero T
	es, fetched, err := r.load(kind, false)
	if err != nil {
		return zero, err
	}
	v, err := try(es)
	var e *exit.Error
	if errors.As(err, &e) && e.Code == exit.NotFound && !fetched {
		if es, _, err = r.load(kind, true); err != nil {
			return zero, err
		}
		return try(es)
	}
	return v, err
}

func miss(valid []string, format string, args ...any) *exit.Error {
	slices.Sort(valid)
	return &exit.Error{Code: exit.NotFound, Msg: fmt.Sprintf(format, args...), Valid: slices.Compact(valid)}
}

// Me returns the ID of the member that owns the token. The cache key is per
// token, so another token in the same workspace gets its own member.
func (r *R) Me() (string, error) {
	key := "whoami-" + r.d.TokenID()
	var id string
	if fresh, err := r.c.Get(key, &id); err != nil {
		return "", err
	} else if fresh && id != "" {
		return id, nil
	}
	at := r.c.Now()
	b, err := r.d.Raw(r.ctx, "GET", "/api/v4/whoami", nil, nil)
	if err != nil {
		return "", err
	}
	var w struct {
		Member struct {
			ID string `json:"id"`
		} `json:"member"`
	}
	if json.Unmarshal(b, &w) != nil || w.Member.ID == "" {
		return "", &exit.Error{Code: exit.Other, Msg: "whoami has no member"}
	}
	_ = r.c.Put(key, w.Member.ID, at) // see fetch
	return w.Member.ID, nil
}

// Member resolves @me, a mention name (with or without @), an email or a UUID.
func (r *R) Member(name string) (string, error) {
	if name == "@me" {
		return r.Me()
	}
	if ref.IsUUID(name) {
		return name, nil
	}
	mention := strings.TrimPrefix(name, "@")
	return lookup(r, "members", func(es []entity) (string, error) {
		var valid []string
		for _, e := range es {
			if e.MentionName == "" {
				continue
			}
			if e.MentionName == mention || (e.Email != "" && e.Email == name) {
				return idString(e.ID), nil
			}
			valid = append(valid, e.MentionName)
		}
		return "", miss(valid, "no member %q", name)
	})
}

// Members resolves each name. It fails on the first name that does not
// resolve. Each ID appears once, in order of first use.
func (r *R) Members(names []string) ([]string, error) { return each(names, r.Member) }

// each resolves each name with one. It fails on the first name that does not
// resolve. Each ID appears once, in order of first use.
func each[T comparable](names []string, one func(string) (T, error)) ([]T, error) {
	out := make([]T, 0, len(names))
	for _, n := range names {
		id, err := one(n)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Team resolves a team name to its UUID.
func (r *R) Team(name string) (string, error) {
	return lookup(r, "teams", func(es []entity) (string, error) {
		var valid []string
		for _, e := range es {
			if e.Name == name {
				return idString(e.ID), nil
			}
			valid = append(valid, e.Name)
		}
		return "", miss(valid, "no team %q", name)
	})
}

// Workflow resolves a workflow name to its ID.
func (r *R) Workflow(name string) (int64, error) {
	return lookup(r, "workflows", func(es []entity) (int64, error) {
		var valid []string
		for _, e := range es {
			if e.Name == name {
				return idInt(e.ID)
			}
			valid = append(valid, e.Name)
		}
		return 0, miss(valid, "no workflow %q", name)
	})
}

// State resolves a state name. With workflowID, it looks in that workflow.
// Else with teamID, it looks in the default workflow of that team. Else it
// looks in all workflows, and a name in more than one is a usage error.
func (r *R) State(name string, workflowID int64, teamID string) (int64, error) {
	if workflowID == 0 && teamID != "" {
		w, err := lookup(r, "teams", func(es []entity) (int64, error) {
			for _, e := range es {
				if idString(e.ID) == teamID {
					return idInt(e.DefaultWorkflow.ID)
				}
			}
			return 0, exit.NotFoundf("no team %q", teamID)
		})
		if err != nil {
			return 0, err
		}
		workflowID = w
	}
	return lookup(r, "workflows", func(es []entity) (int64, error) {
		return findState(es, name, workflowID)
	})
}

func stateEntities(e entity) []entity {
	out := make([]entity, 0, len(e.WorkflowStates.Entities))
	for _, b := range e.WorkflowStates.Entities {
		var s entity
		if json.Unmarshal(b, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func findState(es []entity, name string, workflowID int64) (int64, error) {
	if workflowID != 0 {
		for _, w := range es {
			if id, err := idInt(w.ID); err != nil || id != workflowID {
				continue
			}
			var valid []string
			for _, s := range stateEntities(w) {
				if s.Name == name {
					return idInt(s.ID)
				}
				valid = append(valid, s.Name)
			}
			return 0, miss(valid, "no state %q in workflow %q", name, w.Name)
		}
		return 0, exit.NotFoundf("no workflow %d", workflowID)
	}
	var ids, inWorkflows, valid []string
	for _, w := range es {
		for _, s := range stateEntities(w) {
			valid = append(valid, s.Name)
			if s.Name == name {
				ids = append(ids, idString(s.ID))
				inWorkflows = append(inWorkflows, w.Name)
			}
		}
	}
	switch len(ids) {
	case 0:
		return 0, miss(valid, "no state %q", name)
	case 1:
		return strconv.ParseInt(ids[0], 10, 64)
	}
	slices.Sort(inWorkflows)
	return 0, &exit.Error{
		Code:       exit.Usage,
		Msg:        fmt.Sprintf("state %q is in more than one workflow", name),
		Suggestion: "give a workflow or a team",
		Valid:      inWorkflows,
	}
}

// Field resolves a custom field name and one of its values.
func (r *R) Field(name, value string) (fieldID, valueID string, err error) {
	type pair struct{ f, v string }
	p, err := lookup(r, "custom-fields", func(es []entity) (pair, error) {
		var fields []string
		for _, f := range es {
			if f.Name != name {
				fields = append(fields, f.Name)
				continue
			}
			var vals []string
			for _, v := range f.Values {
				if v.Value == value {
					return pair{idString(f.ID), idString(v.ID)}, nil
				}
				vals = append(vals, v.Value)
			}
			return pair{}, miss(vals, "no value %q for field %q", value, name)
		}
		return pair{}, miss(fields, "no field %q", name)
	})
	return p.f, p.v, err
}

// Label resolves a label name to its ID.
func (r *R) Label(name string) (int64, error) {
	return lookup(r, "labels", func(es []entity) (int64, error) {
		var valid []string
		for _, e := range es {
			if e.Name == name {
				return idInt(e.ID)
			}
			valid = append(valid, e.Name)
		}
		return 0, miss(valid, "no label %q", name)
	})
}

// Labels resolves each label name, as Members does.
func (r *R) Labels(names []string) ([]int64, error) { return each(names, r.Label) }

// epicQuery quotes a name for the search, so that it matches as a phrase.
// A name with a double quote is sent as is.
func epicQuery(s string) string {
	if strings.Contains(s, `"`) {
		return s
	}
	return `"` + s + `"`
}

// Epic resolves an epic ID or an exact epic name, trimmed. A blank one is a
// usage error. Epics are not cached.
func (r *R) Epic(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, exit.Usagef("give an epic ID or name")
	}
	if digitRe.MatchString(s) {
		return strconv.ParseInt(s, 10, 64)
	}
	id, err := r.byName("/epics/search", url.Values{"query": {epicQuery(s)}}, "name", s, "epic", "give the epic ID")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(id, 10, 64)
}

// DocByTitle resolves an exact doc title (see ref.Doc) to a doc ID. Docs are
// not cached.
func (r *R) DocByTitle(s string) (string, error) {
	return r.byName("/documents/search", url.Values{"query": {s}, "fields": {"id,title"}}, "title", s, "doc", "give the doc URL or ID")
}

// byName reads every page of the search at path with q, and returns the ID of
// the one item whose key field is s. No match exits 3 with the names found;
// more than one exits 2 with their IDs and hint. noun names the item.
func (r *R) byName(path string, q url.Values, key, s, noun, hint string) (string, error) {
	q.Set("limit", pageSize)
	items, err := r.pageAll(path, q)
	if err != nil {
		return "", err
	}
	var ids, names []string
	for _, b := range items {
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		var name string
		_ = json.Unmarshal(m[key], &name) // no name: ""
		names = append(names, name)
		if name == s {
			ids = append(ids, idString(m["id"]))
		}
	}
	switch len(ids) {
	case 0:
		return "", miss(names, "no %s %q", noun, s)
	case 1:
		return ids[0], nil
	}
	return "", &exit.Error{
		Code:       exit.Usage,
		Msg:        fmt.Sprintf("%s %s %q matches more than one %s", noun, key, s, noun),
		Suggestion: hint,
		Valid:      ids,
	}
}

// Names maps IDs to names, for output. kind is members, teams, workflows or
// labels. A member maps to its mention name. An unknown ID is left out.
func (r *R) Names(kind string, ids ...string) (map[string]string, error) {
	switch kind {
	case "members", "teams", "workflows", "labels":
	default:
		return nil, exit.Usagef("no names for %q", kind)
	}
	es, _, err := r.load(kind, false)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range es {
		id := idString(e.ID)
		if !slices.Contains(ids, id) {
			continue
		}
		out[id] = e.Name
		if kind == "members" {
			out[id] = e.MentionName
		}
	}
	return out, nil
}

// all returns the raw entities of kind, from the cache or the API.
func (r *R) all(kind string) ([]json.RawMessage, error) {
	var raw []json.RawMessage
	if fresh, err := r.c.Get(kind, &raw); err != nil {
		return nil, err
	} else if fresh && raw != nil {
		return raw, nil
	}
	return r.fetch(kind)
}

// Teams returns all teams, as the API sent them.
func (r *R) Teams() ([]json.RawMessage, error) { return r.all("teams") }

// MemberList returns all members, as the API sent them.
func (r *R) MemberList() ([]json.RawMessage, error) { return r.all("members") }

// Workflows returns all workflows, with their states, as the API sent them.
func (r *R) Workflows() ([]json.RawMessage, error) { return r.all("workflows") }

// Fields returns all custom fields, with their values, as the API sent them.
func (r *R) Fields() ([]json.RawMessage, error) { return r.all("custom-fields") }

// FieldValue is one value of a custom field.
type FieldValue struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FieldValues returns the values of the custom field called name.
func (r *R) FieldValues(name string) ([]FieldValue, error) {
	return lookup(r, "custom-fields", func(es []entity) ([]FieldValue, error) {
		var valid []string
		for _, f := range es {
			if f.Name != name {
				valid = append(valid, f.Name)
				continue
			}
			out := make([]FieldValue, 0, len(f.Values))
			for _, v := range f.Values {
				out = append(out, FieldValue{idString(v.ID), v.Value})
			}
			return out, nil
		}
		return nil, miss(valid, "no field %q", name)
	})
}
