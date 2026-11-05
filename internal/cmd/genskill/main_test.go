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
	"gopkg.in/yaml.v3"

	"github.com/nono/scut/internal/cli"
)

const repoRoot = "../../.."

func testRoot() *cobra.Command {
	return cli.NewRoot(cli.Env{Getenv: func(string) string { return "" }})
}

// testSkill is the SKILL.md text of the real command tree and spec.
func testSkill(t *testing.T) string {
	t.Helper()
	return skillText(testRoot(), loadSpec(t))
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
	text := testSkill(t)
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
	if strings.Contains(testSkill(t), "MCP") {
		t.Error("the skill mentions MCP")
	}
}

func TestSkillNotSupported(t *testing.T) {
	text := testSkill(t)
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
	text := testSkill(t)
	for _, want := range []string{
		"scut usage", "--body-file", "--description-file", "130", "suggestion", "story search", "scut api",
		// Item 7 of the skill design: the rules on risky writes.
		"Never add `--yes` yourself", "only when the user asks",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// The frontmatter is YAML that a strict parser reads, with the description
// and allowed-tools as they are generated.
func TestSkillFrontmatterParses(t *testing.T) {
	spec := loadSpec(t)
	text := skillText(testRoot(), spec)
	body, ok := strings.CutPrefix(text, "---\n")
	fm, _, ok2 := strings.Cut(body, "\n---\n")
	if !ok || !ok2 {
		t.Fatalf("no frontmatter in %q", text[:min(len(text), 200)])
	}
	var m map[string]string
	if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
		t.Fatalf("frontmatter is not valid YAML: %v", err)
	}
	if m["name"] != "scut" || m["description"] != description || m["allowed-tools"] != allowedTools(testRoot(), spec) || len(m) != 3 {
		t.Errorf("frontmatter %v", m)
	}
	for _, want := range []string{"sc-1234", "app.shortcut.com", "ticket", "sub-task", "In Review"} {
		if !strings.Contains(m["description"], want) {
			t.Errorf("description lacks %q", want)
		}
	}
}

// A command with no ops annotation is not a read: allowed-tools leaves it out,
// unless it is on the named list of local reads.
func TestAllowedToolsFailsClosed(t *testing.T) {
	root := testRoot()
	root.AddCommand(&cobra.Command{Use: "newcmd", Run: func(*cobra.Command, []string) {}})
	out := &cobra.Command{Use: "newout", Annotations: map[string]string{cli.OpsAnnotation: "getStory"}, Run: func(*cobra.Command, []string) {}}
	out.Flags().String("out", "", "")
	root.AddCommand(out)
	got := allowedTools(root, loadSpec(t))
	if strings.Contains(got, "newcmd") {
		t.Errorf("a command with no ops is allowed: %q", got)
	}
	if strings.Contains(got, "newout") {
		t.Errorf("a command with --out is allowed: %q", got)
	}
}

// allowed-tools lists the commands that make no write to Shortcut, to the
// keyring or to a path that the caller gives. Reads also write the cache.
func TestSkillAllowedTools(t *testing.T) {
	spec := loadSpec(t)
	line := allowedTools(testRoot(), spec) // TestSkillFrontmatterParses checks that the skill holds it
	for _, want := range []string{"Bash(scut story view:*)", "Bash(scut story search:*)", "Bash(scut usage:*)", "Bash(scut story usage:*)", "Bash(scut spec:*)", "Bash(scut whoami:*)"} {
		if !strings.Contains(line, want) {
			t.Errorf("allowed-tools lacks %s: %q", want, line)
		}
	}
	for _, bad := range []string{"story update", "story create", "story comment:", "story link", "story unlink", "worktree", "scut api", "auth login", "cache clear", "epic comment", "doc view", "story files"} {
		if strings.Contains(line, bad) {
			t.Errorf("allowed-tools has %q: %q", bad, line)
		}
	}
	// The lists above cover the commands of today; this loop guards the ones added later.
	for _, c := range leafCommands(testRoot()) {
		if !strings.Contains(line, "Bash(scut "+cmdPath(c)+":*)") {
			continue
		}
		for _, op := range cli.Ops(c) {
			if spec.methods[op] != "GET" {
				t.Errorf("%s is allowed but calls %s %s", cmdPath(c), spec.methods[op], op)
			}
		}
	}
}

// The command list gives flag names, not cobra types; a repeatable flag ends in "…".
func TestSkillFlagNames(t *testing.T) {
	text := testSkill(t)
	for _, bad := range []string{"stringArray", " string,", " string.", " int."} {
		if strings.Contains(text, bad) {
			t.Errorf("has %q", bad)
		}
	}
	for _, want := range []string{"--add-owner …", "--archive", "--depth"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q", want)
		}
	}
}

// The skill loads into the context of each task that needs Shortcut: keep it short.
// The command list grows with the commands, so the limits are on the
// hand-written head and on each command line, not on the whole file.
func TestSkillSize(t *testing.T) {
	if n := len(skillHead); n >= 6000 {
		t.Errorf("skill_head.md has %d characters, want under 6000", n)
	}
	_, list, _ := strings.Cut(testSkill(t), skillHead)
	for _, l := range strings.Split(list, "\n") {
		if len(l) >= 300 {
			t.Errorf("command line of %d characters, want under 300: %q", len(l), l)
		}
	}
}

func TestSkillStable(t *testing.T) {
	first := testSkill(t)
	if first != testSkill(t) {
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
	for _, bad := range []string{"no markers", beginMark + " no end", endMark + " no begin",
		beginMark + endMark, permsMark + permsEnd, permsMark + " no end" + beginMark + endMark} {
		if _, err := readmeText(bad, ""); err == nil {
			t.Errorf("want an error for %q", bad)
		}
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
		"| `story view` | no | yes |",
		"| `story search` | no | yes |",
		"| `cache clear` | no | no |",
		"| `story comment` | `createStoryComment` | no |",
		"| `story unlink` | `deleteStoryLink` | no |",
		"| `story create` | `createStory` | no |",
		"| `worktree` | `updateStory` | no |",
		"| `api` | any operation; a DELETE needs `--yes` | no |",
		"| `doc view` | no | no |",
		"| `spec` | no | yes |",
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
