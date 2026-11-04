package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/ref"
	"github.com/nono/scut/internal/transport"
)

const duplicateLinkMsg = "Cannot create a duplicate Story Link."

// apiLink is a story link as the API returns it.
type apiLink struct {
	ID      int64  `json:"id"`
	Verb    string `json:"verb"`
	Subject struct {
		ID int64 `json:"id"`
	} `json:"subject"`
	Object struct {
		ID int64 `json:"id"`
	} `json:"object"`
}

func (l apiLink) String() string {
	return fmt.Sprintf("%d %s %d", l.Subject.ID, l.Verb, l.Object.ID)
}

// linkEnds returns the API subject and object of "a <cli verb> b".
func linkEnds(a int64, cliVerb string, b int64) (subject int64, verb string, object int64, err error) {
	verb, swap, err := linkVerb(cliVerb)
	if err != nil {
		return 0, "", 0, err
	}
	if swap {
		return b, verb, a, nil
	}
	return a, verb, b, nil
}

func isDuplicateLink(err error) bool {
	var e *exit.Error
	if !errors.As(err, &e) || e.Status != http.StatusUnprocessableEntity {
		return false
	}
	var body struct {
		Message string `json:"message"`
	}
	return json.Unmarshal(e.API, &body) == nil && body.Message == duplicateLinkMsg
}

// errorJSON returns the JSON that exit.Write prints for err.
func errorJSON(err error) json.RawMessage {
	var b bytes.Buffer
	exit.Write(&b, err)
	return json.RawMessage(bytes.TrimSpace(b.Bytes()))
}

type linkResult struct {
	Subject int64           `json:"subject"`
	Verb    string          `json:"verb"`
	Object  int64           `json:"object"`
	ID      *int64          `json:"id,omitempty"`
	Existed *bool           `json:"existed,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func newStoryLink(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <story> <verb> <story> [<verb> <story>]...",
		Short: "Link stories",
		Long: "Link stories. Verbs: blocks, blocked-by, relates-to, duplicates, duplicated-by.\n" +
			"Prints one result per pair. A link that exists already gives existed: true.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) < 3 || len(args)%2 == 0 {
				return exit.Usagef("need a story, then one or more pairs of verb and story")
			}
			return nil
		},
		Annotations: map[string]string{opsAnnotation: "createStoryLink"},
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		a, err := ref.Story(args[0])
		if err != nil {
			return err
		}
		type pair struct {
			subject, object int64
			cli, api        string
			cliSubject      int64
			cliObject       int64
		}
		var pairs []pair
		for i := 1; i < len(args); i += 2 {
			b, err := ref.Story(args[i+1])
			if err != nil {
				return err
			}
			s, v, o, err := linkEnds(a, args[i], b)
			if err != nil {
				return err
			}
			pairs = append(pairs, pair{s, o, args[i], v, a, b})
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			var first error
			res := make([]linkResult, 0, len(pairs))
			for _, p := range pairs {
				r := linkResult{Subject: p.cliSubject, Verb: p.cli, Object: p.cliObject}
				raw, err := d.Doer.Raw(cmd.Context(), "POST", "story-links", nil, map[string]any{
					"subject_story_id": p.subject, "verb": p.api, "object_story_id": p.object,
				})
				switch {
				case err == nil:
					var l apiLink
					if err := json.Unmarshal(raw, &l); err != nil {
						return fmt.Errorf("decode link: %w", err)
					}
					existed := false
					r.ID, r.Existed = &l.ID, &existed
				case isDuplicateLink(err):
					existed := true
					r.Existed = &existed
				case cmd.Context().Err() != nil:
					return err
				default:
					r.Error = errorJSON(err)
					if first == nil {
						first = err
					}
				}
				res = append(res, r)
			}
			if err := d.Out(res); err != nil {
				return err
			}
			if first != nil {
				return first
			}
			return nil
		})(cmd, args)
	}
	return cmd
}

// storyLinks lists the links of a story.
func storyLinks(cmd *cobra.Command, d *deps, id int64) ([]apiLink, error) {
	items, err := transport.PageQuery(cmd.Context(), d.Doer, "stories/"+strconv.FormatInt(id, 10)+"/story-links", url.Values{}, -1)
	if err != nil {
		return nil, err
	}
	links := make([]apiLink, 0, len(items))
	for _, it := range items {
		var l apiLink
		if err := json.Unmarshal(it, &l); err != nil {
			return nil, fmt.Errorf("decode link: %w", err)
		}
		links = append(links, l)
	}
	return links, nil
}

// findLink returns the link of story a that matches the API subject, verb and
// object. No match gives exit 3 with the existing links as valid.
func findLink(links []apiLink, subject int64, verb string, object int64) (apiLink, error) {
	valid := make([]string, 0, len(links))
	for _, l := range links {
		if l.Subject.ID == subject && l.Verb == verb && l.Object.ID == object {
			return l, nil
		}
		valid = append(valid, l.String())
	}
	return apiLink{}, &exit.Error{
		Code:  exit.NotFound,
		Msg:   fmt.Sprintf("no link %d %s %d", subject, verb, object),
		Valid: valid,
	}
}

func linkPath(id int64) string { return "story-links/" + strconv.FormatInt(id, 10) }

func newStoryUnlink(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "unlink <story> <verb> <story>",
		Short:       "Remove a link between stories",
		Args:        cobra.ExactArgs(3),
		Annotations: map[string]string{opsAnnotation: "listStoryStoryLinks,deleteStoryLink"},
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		a, err := ref.Story(args[0])
		if err != nil {
			return err
		}
		b, err := ref.Story(args[2])
		if err != nil {
			return err
		}
		s, v, o, err := linkEnds(a, args[1], b)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			links, err := storyLinks(cmd, d, a)
			if err != nil {
				return err
			}
			l, err := findLink(links, s, v, o)
			if err != nil {
				return err
			}
			if _, err := d.Doer.Raw(cmd.Context(), "DELETE", linkPath(l.ID), nil, nil); err != nil {
				return err
			}
			return d.Out(map[string]int64{"deleted": l.ID})
		})(cmd, args)
	}
	return cmd
}

func newStoryRelink(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "relink <story> <old-verb> <story> <new-verb>",
		Short:       "Change the verb of a link",
		Args:        cobra.ExactArgs(4),
		Annotations: map[string]string{opsAnnotation: "listStoryStoryLinks,updateStoryLink"},
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		a, err := ref.Story(args[0])
		if err != nil {
			return err
		}
		b, err := ref.Story(args[2])
		if err != nil {
			return err
		}
		s, v, o, err := linkEnds(a, args[1], b)
		if err != nil {
			return err
		}
		ns, nv, no, err := linkEnds(a, args[3], b)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			links, err := storyLinks(cmd, d, a)
			if err != nil {
				return err
			}
			l, err := findLink(links, s, v, o)
			if err != nil {
				return err
			}
			raw, err := d.Doer.Raw(cmd.Context(), "PATCH", linkPath(l.ID), nil, map[string]any{
				"verb": nv, "subject_story_id": ns, "object_story_id": no,
			})
			if err != nil {
				return err
			}
			var u apiLink
			if err := json.Unmarshal(raw, &u); err != nil {
				return fmt.Errorf("decode link: %w", err)
			}
			return d.Out(map[string]any{"id": u.ID, "subject": u.Subject.ID, "verb": u.Verb, "object": u.Object.ID})
		})(cmd, args)
	}
	return cmd
}
