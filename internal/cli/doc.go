package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
)

// docFields are the Doc fields of the compact shape, with no content.
const docFields = "id,title,archived,creator,created_at,updated_at"

// DocOut is the compact doc shape. Only doc view sets a content key, the one of
// the format it asked for. With --out, ContentFile replaces the content.
type DocOut struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Archived        bool       `json:"archived"`
	Creator         *memberOut `json:"creator,omitempty"`
	CreatedAt       string     `json:"created_at,omitempty"`
	UpdatedAt       string     `json:"updated_at,omitempty"`
	ContentMarkdown *string    `json:"content_markdown,omitempty"`
	ContentHTML     *string    `json:"content_html,omitempty"`
	ContentFile     string     `json:"content_file,omitempty"`
}

type docIn struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Archived        bool      `json:"archived"`
	Creator         *memberIn `json:"creator"`
	CreatedAt       string    `json:"created_at"`
	UpdatedAt       string    `json:"updated_at"`
	ContentMarkdown *string   `json:"content_markdown"`
	ContentHTML     *string   `json:"content_html"`
}

// Human is the text form: title, creator, update time, content file or content.
func (o DocOut) Human() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (doc %s)\n", o.Title, o.ID)
	if o.Archived {
		b.WriteString("Archived\n")
	}
	if o.Creator != nil {
		fmt.Fprintf(&b, "Creator: %s\n", o.Creator.label())
	}
	if o.UpdatedAt != "" {
		fmt.Fprintf(&b, "Updated: %s\n", o.UpdatedAt)
	}
	if o.ContentFile != "" {
		fmt.Fprintf(&b, "Content: %s\n", o.ContentFile)
	}
	for _, c := range []*string{o.ContentMarkdown, o.ContentHTML} {
		if c != nil && *c != "" {
			fmt.Fprintf(&b, "\n%s\n", *c)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type docList []DocOut

// Human joins the text forms of the docs.
func (l docList) Human() string {
	parts := make([]string, len(l))
	for i, o := range l {
		parts[i] = o.Human()
	}
	return strings.Join(parts, "\n\n")
}

// shapeDocs converts Doc entities to the compact shape. The content keys stay
// as the API sent them.
func shapeDocs(d *deps, raw []json.RawMessage) (docList, error) {
	docs, err := decodeAll[docIn](raw, "doc")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, x := range docs {
		if x.Creator != nil {
			ids = append(ids, x.Creator.ID)
		}
	}
	nm, err := memberNames(d.Resolver, ids)
	if err != nil {
		return nil, err
	}
	out := make(docList, 0, len(docs))
	for _, x := range docs {
		out = append(out, DocOut{
			ID: x.ID, Title: x.Title, Archived: x.Archived, Creator: nm.member(x.Creator),
			CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt,
			ContentMarkdown: x.ContentMarkdown, ContentHTML: x.ContentHTML,
		})
	}
	return out, nil
}

func newDocGroup(env Env) *cobra.Command {
	g := Group("doc", "View and search docs")
	g.AddCommand(newDocView(env), newDocSearch(env))
	return g
}

func newDocView(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view <doc>",
		Short: "Show a doc and its content, by URL, ID or exact title",
		Long: "Show a doc and its content. The doc is a doc URL, a doc ID or an exact title.\n\n" +
			"A doc can be large: use --out to write the content to a file and print only the doc fields.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "getDoc,searchDocs,listMembers"},
	}
	format := cmd.Flags().String("format", "markdown", "content format: markdown or html")
	outFlag := cmd.Flags().String("out", "", "write the content to this file, which it replaces, and leave it out of the output")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if *format != "markdown" && *format != "html" {
			return &exit.Error{Code: exit.Usage, Msg: fmt.Sprintf("--format takes markdown or html, not %q", *format), Valid: []string{"html", "markdown"}}
		}
		out := ""
		if *outFlag != "" {
			var err error
			if out, err = checkOutFile(*outFlag); err != nil {
				return err
			}
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			id, err := d.Resolver.Doc(args[0])
			if err != nil {
				return err
			}
			key := "content_" + *format
			// Without content_format the API sends no content.
			q := url.Values{"content_format": {*format}, "fields": {docFields + "," + key}}
			raw, err := d.Doer.Raw(cmd.Context(), "GET", "documents/"+url.PathEscape(id), q, nil)
			if err != nil {
				return err
			}
			// A null content is an empty doc, but a reply with no content
			// key or no doc is not: never show it as an empty doc.
			var keys map[string]json.RawMessage
			if json.Unmarshal(raw, &keys) != nil || keys == nil {
				return &exit.Error{Code: exit.Other, Msg: "no doc in the reply"}
			}
			if _, ok := keys[key]; !ok {
				return &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("no %s in the reply", key)}
			}
			l, err := shapeDocs(d, []json.RawMessage{raw})
			if err != nil {
				return err
			}
			o := l[0]
			content := &o.ContentMarkdown
			if *format == "html" {
				content = &o.ContentHTML
			}
			if *content == nil { // an empty doc: print the key, so the content is not missing
				*content = new(string)
			}
			if out != "" {
				if err := os.WriteFile(out, []byte(**content), 0o644); err != nil {
					return &exit.Error{Code: exit.Other, Msg: fmt.Sprintf("cannot write %s: %v", out, err)}
				}
				*content = nil
				o.ContentFile = out
			}
			return d.Out(o)
		})(cmd, args)
	}
	return cmd
}

// checkOutFile returns the absolute path of the --out file. It fails, before
// any call, when the path is "-", is a directory, or has no parent directory.
func checkOutFile(path string) (string, error) {
	if path == "-" {
		return "", exit.Usagef("--out takes a file name; leave out --out to print the content")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", exit.Usagef("bad --out %q: %v", path, err)
	}
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		return "", exit.Usagef("--out %q is a directory", path)
	}
	if fi, err := os.Stat(filepath.Dir(abs)); err != nil || !fi.IsDir() {
		return "", exit.Usagef("--out %q: the directory %s does not exist", path, filepath.Dir(abs))
	}
	return abs, nil
}

func newDocSearch(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "search <text>",
		Short:       "Search docs by title",
		Long:        "Search docs by title, with fuzzy match. The output has no content: use doc view.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "searchDocs,listMembers"},
	}
	limitFlag := cmd.Flags().String("limit", strconv.Itoa(defaultLimit), "maximum number of docs, or all")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return exit.Usagef("the search text is empty")
		}
		limit, err := parseLimit(*limitFlag)
		if err != nil {
			return err
		}
		return withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			raw, stop := listPages(cmd, d, "documents/search", url.Values{"query": {args[0]}, "fields": {docFields}}, limit)
			if raw == nil && stop != nil {
				return stop
			}
			out, err := shapeDocs(d, raw)
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
