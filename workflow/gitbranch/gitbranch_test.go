package gitbranch

import (
	"os/exec"
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
