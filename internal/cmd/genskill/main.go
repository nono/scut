// Command genskill writes skills/scut/SKILL.md and docs/coverage.md from the
// scut command tree, and internal/cli/storyfields_gen.go from the v4 spec.
// With -check it writes nothing and exits 1 if a file differs.
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nono/scut/internal/cli"
)

const (
	skillPath    = "skills/scut/SKILL.md"
	coveragePath = "docs/coverage.md"
	fieldsPath   = "internal/cli/storyfields_gen.go"
	specPath     = "api/shortcut.openapi.json"
)

//go:embed skill_head.md
var skillHead string

const frontmatter = "---\nname: scut\n" +
	"description: Use when a task reads or changes Shortcut stories, epics, comments, links or attachments, reads a Shortcut doc, or needs a Shortcut ID or branch name.\n---\n\n"

func main() {
	env := cli.Env{Getenv: func(string) string { return "" }}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, cli.NewRoot(env)))
}

func run(args []string, stdout, stderr io.Writer, root *cobra.Command) int {
	fs := flag.NewFlagSet("genskill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "exit 1 if a generated file differs")
	dir := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec, err := readSpec(filepath.Join(*dir, specPath))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "genskill:", err)
		return 1
	}
	cov, err := coverageText(root, spec)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "genskill:", err)
		return 1
	}
	files := []struct{ path, text string }{
		{skillPath, skillText(root)},
		{coveragePath, cov},
		{fieldsPath, storyFieldsText(spec)},
	}
	var stale []string
	for _, f := range files {
		full := filepath.Join(*dir, f.path)
		if *check {
			if b, err := os.ReadFile(full); err != nil || string(b) != f.text {
				stale = append(stale, f.path)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			_, _ = fmt.Fprintln(stderr, "genskill:", err)
			return 1
		}
		if err := os.WriteFile(full, []byte(f.text), 0o644); err != nil {
			_, _ = fmt.Fprintln(stderr, "genskill:", err)
			return 1
		}
	}
	if len(stale) > 0 {
		_, _ = fmt.Fprintf(stderr, "differs from the generated text, run make skill: %s\n", strings.Join(stale, ", "))
		return 1
	}
	return 0
}

// skipCmd reports commands that are not part of the reference.
func skipCmd(c *cobra.Command) bool {
	switch c.Name() {
	case "usage", "help", "completion":
		return true
	}
	return c.Hidden
}

// leafCommands returns the leaf commands sorted by path.
func leafCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			switch {
			case skipCmd(s):
			case s.HasSubCommands():
				walk(s)
			default:
				out = append(out, s)
			}
		}
	}
	walk(root)
	sort.Slice(out, func(i, j int) bool { return cmdPath(out[i]) < cmdPath(out[j]) })
	return out
}

// cmdPath returns the command path without the root name.
func cmdPath(c *cobra.Command) string {
	return strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
}

func skillText(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString(frontmatter)
	b.WriteString(skillHead)
	for _, c := range leafCommands(root) {
		fmt.Fprintf(&b, "- `%s`", cmdPath(c))
		if _, a, ok := strings.Cut(c.Use, " "); ok {
			fmt.Fprintf(&b, " `%s`", a)
		}
		fmt.Fprintf(&b, " %s.", strings.TrimSuffix(c.Short, "."))
		var flags []string
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden {
				return
			}
			s := "--" + f.Name
			if t := f.Value.Type(); t != "bool" {
				s += " " + t
			}
			flags = append(flags, s)
		})
		sort.Strings(flags)
		if len(flags) > 0 {
			fmt.Fprintf(&b, " Flags: %s.", strings.Join(flags, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// opsByCommand maps each operationId to the sorted command paths that call it.
// The "*" of scut api is left out.
func opsByCommand(root *cobra.Command) map[string][]string {
	m := map[string][]string{}
	for _, c := range leafCommands(root) {
		for _, op := range strings.Split(c.Annotations[cli.OpsAnnotation], ",") {
			if op = strings.TrimSpace(op); op != "" && op != "*" {
				m[op] = append(m[op], cmdPath(c))
			}
		}
	}
	for _, cmds := range m {
		sort.Strings(cmds)
	}
	return m
}

type specInfo struct {
	total       int
	ops         map[string]bool
	storyFields []string // the top-level properties of the Story schema, sorted
}

func readSpec(path string) (specInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return specInfo{}, err
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas struct {
				Story struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"Story"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return specInfo{}, fmt.Errorf("%s: %w", path, err)
	}
	s := specInfo{ops: map[string]bool{}}
	for _, item := range doc.Paths {
		for _, raw := range item {
			var op struct {
				ID string `json:"operationId"`
			}
			if json.Unmarshal(raw, &op) == nil && op.ID != "" {
				s.ops[op.ID] = true
			}
		}
	}
	s.total = len(s.ops)
	for name := range doc.Components.Schemas.Story.Properties {
		s.storyFields = append(s.storyFields, name)
	}
	if len(s.storyFields) == 0 {
		return specInfo{}, fmt.Errorf("%s: no Story schema properties", path)
	}
	sort.Strings(s.storyFields)
	return s, nil
}

// storyFieldsText is the Go file that lists the Story properties, for --fields.
func storyFieldsText(spec specInfo) string {
	var b strings.Builder
	b.WriteString("// Code generated by genskill from " + specPath + "; DO NOT EDIT.\n\n")
	b.WriteString("package cli\n\n")
	b.WriteString("// apiStoryFields are the top-level properties of the Story schema.\n")
	b.WriteString("var apiStoryFields = []string{\n")
	for _, f := range spec.storyFields {
		fmt.Fprintf(&b, "\t%q,\n", f)
	}
	b.WriteString("}\n")
	return b.String()
}

func coverageText(root *cobra.Command, spec specInfo) (string, error) {
	ops := opsByCommand(root)
	ids := make([]string, 0, len(ops))
	for id := range ops {
		if !spec.ops[id] {
			return "", fmt.Errorf("operationId %q is not in %s", id, specPath)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("# Endpoint coverage\n\n")
	b.WriteString("Generated by `make skill`. Do not edit.\n\n")
	fmt.Fprintf(&b, "Commands call %d of %d v4 operations. `scut api` can call any operation.\n\n", len(ids), spec.total)
	b.WriteString("| operationId | Commands |\n|---|---|\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "| %s | %s |\n", id, "`"+strings.Join(ops[id], "`, `")+"`")
	}
	return b.String(), nil
}
