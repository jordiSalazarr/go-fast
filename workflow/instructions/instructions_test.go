package instructions_test

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/instructions"
)

const specifyAgent = `---
# A comment, as the plugin's agents have.
name: specify
tools: Read, Write
skills:
  - gofast:specify
  - gofast:functional-domain
---

You run the **specify** stage.
`

// plugin is a plugin root with the specify agent and two skills.
func plugin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "agents/specify.md", specifyAgent)
	write(t, root, "skills/specify/SKILL.md", "# Specify\n")
	write(t, root, "skills/specify/references/examples.md", "## Examples\n")
	write(t, root, "skills/functional-domain/SKILL.md", "# Functional domain\n")
	write(t, root, ".claude-plugin/plugin.json", `{"name": "gofast", "version": "0.2.0"}`)
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestManifest_IsSha256sumsOutput_SortedByPath(t *testing.T) {
	m := instructions.Manifest(map[string][]byte{"references/examples.md": []byte("## Examples\n"), "SKILL.md": []byte("# Specify\n")})
	assert.Equal(t, sum("# Specify\n")+"  SKILL.md\n"+sum("## Examples\n")+"  references/examples.md\n", string(m))
}

func TestSkill_VersionsEveryFileOfItsDirectory(t *testing.T) {
	root := plugin(t)
	s, err := instructions.Skill(root, "specify")
	require.NoError(t, err)
	assert.Equal(t, []string{"SKILL.md", "references/examples.md"}, slices.Sorted(maps.Keys(s.Files)))

	write(t, root, "skills/specify/.DS_Store", "finder")
	same, err := instructions.Skill(root, "specify")
	require.NoError(t, err)
	assert.Equal(t, s.Version, same.Version, "dotfiles are not part of a skill")

	write(t, root, "skills/specify/references/examples.md", "## Examples, edited\n")
	edited, err := instructions.Skill(root, "specify")
	require.NoError(t, err)
	assert.NotEqual(t, s.Version, edited.Version, "a change to any file is a new version")

	_, err = instructions.Skill(root, "postgres")
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = instructions.Skill(root, "../agents")
	assert.Error(t, err)
}

func TestAgentDefinition_VersionsItsFile_AndReadsItsPreloads(t *testing.T) {
	a, err := instructions.AgentDefinition(plugin(t), "specify")
	require.NoError(t, err)
	assert.Equal(t, []string{"gofast:specify", "gofast:functional-domain"}, a.Preloads)
	assert.Equal(t, domain.HashOf([]byte(sum(specifyAgent)+"  specify.md\n")), a.Version)

	root := plugin(t)
	write(t, root, "agents/review.md", "You review.\n")
	a, err = instructions.AgentDefinition(root, "review")
	require.NoError(t, err)
	assert.Empty(t, a.Preloads, "no frontmatter, no preloads")
	write(t, root, "agents/broken.md", "---\nskills: [\n")
	_, err = instructions.AgentDefinition(root, "broken")
	assert.ErrorContains(t, err, "frontmatter")
}

func TestPluginVersion(t *testing.T) {
	assert.Equal(t, "0.2.0", instructions.PluginVersion(plugin(t)))
	assert.Equal(t, "", instructions.PluginVersion(t.TempDir()))
}

func TestKeep_KeepsACopyOnce_ThatReadsBackAsTheSameVersion(t *testing.T) {
	repo := t.TempDir()
	s, err := instructions.Skill(plugin(t), "specify")
	require.NoError(t, err)
	require.NoError(t, instructions.Keep(repo, s))
	require.NoError(t, instructions.Keep(repo, s), "keeping it again changes nothing")

	got, ok, err := instructions.Read(repo, s.Version)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, s, got)
	entries, err := os.ReadDir(filepath.Join(repo, ".gofast", "instructions"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary directory left behind")
	assert.Equal(t, s.Version.String(), entries[0].Name())

	_, ok, err = instructions.Read(repo, domain.HashOf([]byte("other")))
	require.NoError(t, err)
	assert.False(t, ok)
}

// The documented shell pipeline prints a kept copy's own name.
func TestKeep_TheCopyHashesToItsNameWithSha256sum(t *testing.T) {
	sha, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("no sha256sum")
	}
	repo := t.TempDir()
	s, err := instructions.Skill(plugin(t), "specify")
	require.NoError(t, err)
	require.NoError(t, instructions.Keep(repo, s))
	cmd := exec.Command("sh", "-c", "find . -type f | sed 's|^\\./||' | LC_ALL=C sort | xargs "+sha+" | "+sha)
	cmd.Dir = filepath.Join(repo, ".gofast", "instructions", s.Version.String())
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, s.Version.String(), strings.Fields(string(out))[0])
}

func TestVerify_FindsAKeptCopyChangedAfterwards(t *testing.T) {
	repo := t.TempDir()
	kept, problems, err := instructions.Verify(repo)
	require.NoError(t, err)
	assert.Zero(t, kept)
	assert.Empty(t, problems)

	root := plugin(t)
	for _, name := range []string{"specify", "functional-domain"} {
		s, err := instructions.Skill(root, name)
		require.NoError(t, err)
		require.NoError(t, instructions.Keep(repo, s))
	}
	kept, problems, err = instructions.Verify(repo)
	require.NoError(t, err)
	assert.Equal(t, 2, kept)
	assert.Empty(t, problems)

	s, err := instructions.Skill(root, "specify")
	require.NoError(t, err)
	write(t, repo, ".gofast/instructions/"+s.Version.String()+"/SKILL.md", "# Specify, rewritten\n")
	write(t, repo, ".gofast/instructions/notes.txt", "x")
	_, problems, err = instructions.Verify(repo)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	assert.Contains(t, problems[0], ".gofast/instructions/"+s.Version.String()+" changed after gf kept it")
	assert.Equal(t, ".gofast/instructions/notes.txt is not a kept version", problems[1])
}
