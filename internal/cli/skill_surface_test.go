package cli

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// The skill is what a model reads before it touches the CLI, so it must name
// only commands and flags that exist, name every command there is, and stay
// small. These tests pin the skill text to the command tree.

const skillDir = "../../skills/lorvik"

// skillBudget caps each model-facing file in bytes, about 5% above its size
// when the budget was set. Lower a budget when the text shrinks; raise one
// only on purpose, and say why in the commit message.
var skillBudget = map[string]int{
	"SKILL.md":               20_650,
	"reference/commands.md":  43_900,
	"reference/workflows.md": 15_000,
	"reference/hooks.md":     8_600,
	"reference/waiting.md":   3_300,
	"reference/pages.md":     4_300,
	"reference/delegate.md":  5_300,
}

// retiredWords are terms Lorvik decided against; the skill must not teach them.
var retiredWords = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bhand-?offs?\b`),
	regexp.MustCompile(`\bMCP\b`),
}

var (
	codeSpan   = regexp.MustCompile("`([^`]+)`")
	tableCell  = regexp.MustCompile("(?m)^\\| `([^`]+)`")
	flagToken  = regexp.MustCompile(`(?:^|[\s\[(|])(--?[A-Za-z][A-Za-z0-9-]*)`)
	mdLink     = regexp.MustCompile(`\]\(([^)\s#]+)[^)]*\)`)
	wordToken  = regexp.MustCompile(`^[a-z][a-z-]*(\\?\|[a-z][a-z-]*)*$`)
	skillFiles = slices.Sorted(maps.Keys(skillBudget))
)

func readSkill(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(skillDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func skillRoot(t *testing.T) *cobra.Command {
	t.Helper()
	streams, _, _ := cmdutil.Test(nil)
	return NewRoot(&cmdutil.Factory{IOStreams: streams})
}

// invocation is one `lorvik ...` span found in the skill text.
type invocation struct {
	file, span string
	path       []string
	flags      []string
}

// invocations lists every command the skill names: inline backtick spans that
// start with `lorvik `, command lines of fenced blocks, and in the command
// reference the first cell of each table row, which omits the prefix.
func invocations(t *testing.T) []invocation {
	t.Helper()
	var out []invocation
	for _, name := range skillFiles {
		text := readSkill(t, name)
		spans := []string{}
		prose, fenced := splitFenced(text)
		for _, m := range codeSpan.FindAllStringSubmatch(prose, -1) {
			if rest, ok := strings.CutPrefix(m[1], "lorvik "); ok {
				spans = append(spans, rest)
			}
			// A span may name the CLI mid-sentence, as in "run lorvik inbox".
			if i := strings.Index(m[1], " run lorvik "); i >= 0 {
				spans = append(spans, m[1][i+len(" run lorvik "):])
			}
		}
		for _, block := range fenced {
			for line := range strings.SplitSeq(block, "\n") {
				line = strings.TrimPrefix(strings.TrimSpace(line), "$ ")
				if rest, ok := strings.CutPrefix(line, "lorvik "); ok {
					spans = append(spans, rest)
				}
			}
		}
		if name == "reference/commands.md" {
			for _, m := range tableCell.FindAllStringSubmatch(text, -1) {
				spans = append(spans, strings.TrimPrefix(m[1], "lorvik "))
			}
		}
		for _, span := range spans {
			inv := invocation{file: name, span: span}
			for w := range strings.FieldsSeq(span) {
				if !wordToken.MatchString(w) {
					break
				}
				inv.path = append(inv.path, w)
			}
			if len(inv.path) == 0 {
				continue
			}
			for _, m := range flagToken.FindAllStringSubmatch(span, -1) {
				inv.flags = append(inv.flags, m[1])
			}
			out = append(out, inv)
		}
	}
	return out
}

// splitFenced separates prose from the contents of ``` blocks, so inline
// spans are matched only in prose and shell lines only in blocks.
func splitFenced(text string) (prose string, fenced []string) {
	parts := strings.Split(text, "```")
	var b strings.Builder
	for i, part := range parts {
		if i%2 == 1 {
			fenced = append(fenced, part)
			continue
		}
		b.WriteString(part)
	}
	return b.String(), fenced
}

// resolve walks the command tree as far as the path's words name commands;
// the first word that is not a subcommand is an argument. A word written as
// alternatives (`link\|unlink`) must resolve on each of them.
func resolve(root *cobra.Command, path []string) (*cobra.Command, bool) {
	cmd := root
	for i, w := range path {
		var next *cobra.Command
		for alt := range strings.SplitSeq(strings.ReplaceAll(w, "\\", ""), "|") {
			c := subcommand(cmd, alt)
			if c == nil {
				next = nil
				break
			}
			if next == nil {
				next = c
			}
		}
		if next == nil {
			return cmd, i > 0
		}
		cmd = next
	}
	return cmd, true
}

func subcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

func hasFlag(cmd *cobra.Command, flag string) bool {
	if name, ok := strings.CutPrefix(flag, "--"); ok {
		return cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil
	}
	short := strings.TrimPrefix(flag, "-")
	if len(short) != 1 {
		return false
	}
	return cmd.Flags().ShorthandLookup(short) != nil || cmd.InheritedFlags().ShorthandLookup(short) != nil
}

func TestSkillNamesOnlyRealCommandsAndFlags(t *testing.T) {
	t.Parallel()
	root := skillRoot(t)
	for _, inv := range invocations(t) {
		cmd, ok := resolve(root, inv.path)
		if !ok {
			t.Errorf("%s: `lorvik %s` names no command", inv.file, inv.span)
			continue
		}
		for _, f := range inv.flags {
			if !hasFlag(cmd, f) {
				t.Errorf("%s: `lorvik %s`: %s has no flag %s", inv.file, inv.span, cmd.CommandPath(), f)
			}
		}
	}
}

func TestSkillReferenceListsEveryCommand(t *testing.T) {
	t.Parallel()
	ref := readSkill(t, "reference/commands.md")
	for _, c := range skillRoot(t).Commands() {
		if c.Hidden || !c.Runnable() && !c.HasSubCommands() || c.Name() == "completion" || c.Name() == "help" {
			continue
		}
		if !strings.Contains(ref, "`"+c.Name()+" ") && !strings.Contains(ref, "`"+c.Name()+"`") {
			t.Errorf("reference/commands.md does not mention `%s`", c.Name())
		}
	}
}

func TestSkillStaysWithinBudget(t *testing.T) {
	t.Parallel()
	for name, budget := range skillBudget {
		if n := len(readSkill(t, name)); n > budget {
			t.Errorf("%s is %d bytes, over its budget of %d", name, n, budget)
		}
	}
}

func TestSkillAvoidsRetiredWords(t *testing.T) {
	t.Parallel()
	for _, name := range skillFiles {
		text := readSkill(t, name)
		for _, re := range retiredWords {
			if m := re.FindString(text); m != "" {
				t.Errorf("%s uses the retired word %q", name, m)
			}
		}
	}
}

func TestSkillAndRequirementsLinksResolve(t *testing.T) {
	t.Parallel()
	dirs := []string{skillDir, "../../docs/requirements"}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range mdLink.FindAllStringSubmatch(string(b), -1) {
				target := m[1]
				if strings.Contains(target, "://") {
					continue
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(path), target)); err != nil {
					t.Errorf("%s links to %s, which does not exist", path, target)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}
