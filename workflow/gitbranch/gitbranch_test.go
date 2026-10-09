package gitbranch

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestCurrent(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")

	branch, err := Current(dir)
	require.NoError(t, err, "a branch without commits counts")
	assert.Equal(t, "main", branch.String())

	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "first")
	git(t, dir, "checkout", "-q", "-b", "fix/double-charge")
	branch, err = Current(dir)
	require.NoError(t, err)
	assert.Equal(t, "fix/double-charge", branch.String())

	git(t, dir, "checkout", "-q", "--detach")
	_, err = Current(dir)
	require.ErrorIs(t, err, ErrDetachedHead)
}

func TestCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commits := NewCommits(dir)

	head, err := commits.Head()
	require.NoError(t, err)
	assert.Empty(t, head, "no commits yet")
	log, err := commits.LogAt(head)
	require.NoError(t, err)
	assert.Nil(t, log)

	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "first")
	first, err := commits.Head()
	require.NoError(t, err)
	assert.Len(t, first, 40)
	log, err = commits.LogAt(first)
	require.NoError(t, err)
	assert.Nil(t, log, "no event log in that commit")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gofast"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gofast", "events.jsonl"), []byte("{}\n"), 0o644))
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "second")
	second, err := commits.Head()
	require.NoError(t, err)
	log, err = commits.LogAt(second)
	require.NoError(t, err)
	assert.Equal(t, []byte("{}\n"), log)

	git(t, dir, "branch", "-q", "old", first)
	git(t, dir, "checkout", "-q", "-b", "other")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gofast", "events.jsonl"), []byte("{}\n{}\n"), 0o644))
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", "third")
	logs, err := commits.LogsOnBranches()
	require.NoError(t, err)
	assert.ElementsMatch(t, [][]byte{[]byte("{}\n"), []byte("{}\n{}\n")}, logs, "main and other; old has none")
}
