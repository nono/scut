package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/cli"
)

const repoRoot = "../../.."

func testRoot() *cobra.Command {
	return cli.NewRoot(cli.Env{Getenv: func(string) string { return "" }})
}

func loadSpec(t *testing.T) specInfo {
	t.Helper()
	s, err := readSpec(filepath.Join(repoRoot, specPath))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSkillHasEachCommand(t *testing.T) {
	root := testRoot()
	text := skillText(root)
	leaves := leafCommands(root)
	if len(leaves) < 20 {
		t.Fatalf("only %d leaf commands", len(leaves))
	}
	for _, c := range leaves {
		if !strings.Contains(text, "`"+cmdPath(c)) {
			t.Errorf("missing %q", cmdPath(c))
		}
	}
}

func TestSkillNoMCP(t *testing.T) {
	if strings.Contains(skillText(testRoot()), "MCP") {
		t.Error("the skill mentions MCP")
	}
}

func TestSkillNotSupported(t *testing.T) {
	text := skillText(testRoot())
	_, sec, ok := strings.Cut(text, "## Not supported")
	if !ok {
		t.Fatal("no Not supported section")
	}
	sec, _, _ = strings.Cut(sec, "\n## ")
	sec = strings.ToLower(sec)
	for _, want := range []string{"pr", "branch", "update a doc", "history", "bulk"} {
		if !strings.Contains(sec, want) {
			t.Errorf("Not supported lacks %q", want)
		}
	}
}

func TestSkillFrontmatterAndHead(t *testing.T) {
	text := skillText(testRoot())
	for _, want := range []string{
		"---\nname: scut\ndescription: Use when a task reads or changes Shortcut stories, epics, comments, links or attachments, reads a Shortcut doc, or needs a Shortcut ID or branch name.\n---\n",
		"scut usage", "--body-file", "--description-file", "130", "suggestion", "story search", "scut api",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestSkillSize(t *testing.T) {
	if n := len(skillText(testRoot())); n >= 6000 {
		t.Errorf("SKILL.md has %d characters, want under 6000", n)
	}
}

func TestSkillStable(t *testing.T) {
	first := skillText(testRoot())
	if first != skillText(testRoot()) {
		t.Error("output differs between runs")
	}
}

func TestCoverageTable(t *testing.T) {
	root := testRoot()
	spec := loadSpec(t)
	table, err := coverageText(root, spec)
	if err != nil {
		t.Fatal(err)
	}
	ops := opsByCommand(root)
	if len(ops) == 0 {
		t.Fatal("no operationId")
	}
	for op, cmds := range ops {
		if !strings.Contains(table, "| "+op+" |") {
			t.Errorf("missing operationId %s", op)
		}
		for _, c := range cmds {
			if !strings.Contains(table, c) {
				t.Errorf("missing command %s", c)
			}
		}
	}
	if !strings.Contains(table, "of "+strconv.Itoa(spec.total)+" ") {
		t.Error("no total count")
	}
}

func TestCoverageUnknownOp(t *testing.T) {
	root := testRoot()
	root.AddCommand(&cobra.Command{Use: "bogus", Annotations: map[string]string{cli.OpsAnnotation: "noSuchOp"}, Run: func(*cobra.Command, []string) {}})
	if _, err := coverageText(root, loadSpec(t)); err == nil {
		t.Error("want an error for an operationId that is not in the spec")
	}
}

// allowNoOps lists the leaf commands that call no v4 operation.
var allowNoOps = map[string]bool{
	"cache clear": true, // removes local files only
	"spec":        true, // reads the embedded spec
}

func TestEveryLeafHasOps(t *testing.T) {
	root := testRoot()
	for _, c := range leafCommands(root) {
		p := cmdPath(c)
		_, has := c.Annotations[cli.OpsAnnotation]
		if allowNoOps[p] {
			if has {
				t.Errorf("%s is in the allowlist but has ops", p)
			}
			continue
		}
		if !has {
			t.Errorf("%s has no %s annotation", p, cli.OpsAnnotation)
		}
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{skillPath, coveragePath, specPath, readmePath} {
		b, err := os.ReadFile(filepath.Join(repoRoot, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var errb bytes.Buffer
	if code := run([]string{"-root", dir}, &errb, testRoot()); code != 0 {
		t.Fatalf("generate: code %d, %s", code, errb.String())
	}
	if code := run([]string{"-root", dir, "-check"}, &errb, testRoot()); code != 0 {
		t.Fatalf("check after generate: code %d, %s", code, errb.String())
	}
	if err := os.WriteFile(filepath.Join(dir, coveragePath), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := run([]string{"-root", dir, "-check"}, &errb, testRoot()); code != 1 {
		t.Fatalf("check on a stale file: code %d", code)
	}
	if got := errb.String(); !strings.Contains(got, coveragePath) || strings.Contains(got, skillPath) || strings.Count(got, "\n") != 1 {
		t.Errorf("message %q", got)
	}
}

func TestStoryFieldsText(t *testing.T) {
	spec, err := readSpec(filepath.Join(repoRoot, specPath))
	if err != nil {
		t.Fatal(err)
	}
	got := storyFieldsText(spec)
	for _, want := range []string{"DO NOT EDIT", "package cli\n", "\t\"estimate\",\n", "\t\"workflow_state\",\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
	if !slices.IsSorted(spec.storyFields) {
		t.Errorf("not sorted: %v", spec.storyFields)
	}
}

func TestCommittedFilesCurrent(t *testing.T) {
	var errb bytes.Buffer
	if code := run([]string{"-root", repoRoot, "-check"}, &errb, testRoot()); code != 0 {
		t.Errorf("code %d: %s (run make skill)", code, errb.String())
	}
}

func TestReadmeText(t *testing.T) {
	in := "# x\n\n" + beginMark + "\n- old\n" + endMark + "\n\n## P\n\n" + permsMark + "\nold\n" + permsEnd + "\n\n## Next\n"
	got, err := readmeText(in, "| table |")
	if err != nil {
		t.Fatal(err)
	}
	want := "# x\n\n" + beginMark + "\n" + notSupported() + "\n" + endMark + "\n\n## P\n\n" + permsMark + "\n| table |\n" + permsEnd + "\n\n## Next\n"
	if got != want {
		t.Errorf("got %q", got)
	}
	if !strings.HasPrefix(notSupported(), "- ") || strings.Contains(notSupported(), "##") {
		t.Errorf("list %q", notSupported())
	}
	both := beginMark + endMark + permsMark + permsEnd
	for _, bad := range []string{"no markers", beginMark + " no end", endMark + " no begin",
		beginMark + endMark, permsMark + permsEnd, permsMark + " no end" + beginMark + endMark} {
		if _, err := readmeText(bad, ""); err == nil {
			t.Errorf("want an error for %q", bad)
		}
	}
	if _, err := readmeText(both, ""); err != nil {
		t.Errorf("both markers: %v", err)
	}
}

func TestPermissionsTable(t *testing.T) {
	root := testRoot()
	table := permissionsText(root, loadSpec(t))
	for _, c := range leafCommands(root) {
		if !strings.Contains(table, "| `"+cmdPath(c)+"` |") {
			t.Errorf("missing %q", cmdPath(c))
		}
	}
	for _, want := range []string{
		"| `story view` | no |",
		"| `story search` | no |",
		"| `cache clear` | no |",
		"| `story comment` | `createStoryComment` |",
		"| `story unlink` | `deleteStoryLink` |",
		"| `story create` | `createStory` |",
		"| `worktree` | `updateStory` |",
		"| `api` | any operation; a DELETE needs `--yes` |",
	} {
		if !strings.Contains(table+"\n", want+"\n") {
			t.Errorf("no %q in\n%s", want, table)
		}
	}
}

func TestReadmeErrors(t *testing.T) {
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join(repoRoot, specPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, specPath), b, 0o644); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	if code := run([]string{"-root", dir}, &errb, testRoot()); code != 1 || !strings.Contains(errb.String(), readmePath) {
		t.Errorf("no README: code %d, %s", code, errb.String())
	}
	if err := os.WriteFile(filepath.Join(dir, readmePath), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := run([]string{"-root", dir}, &errb, testRoot()); code != 1 || !strings.Contains(errb.String(), "markers") {
		t.Errorf("no markers: code %d, %s", code, errb.String())
	}
}
