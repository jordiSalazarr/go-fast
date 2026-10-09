//go:build darwin || dragonfly || freebsd || netbsd || openbsd || linux

package caller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What Claude Code's Bash tool, or an agent's shortcut, can give gf as stdin.
func TestIsTerminal_RefusesWhatIsNotATerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull) // a character device, but not a terminal
	require.NoError(t, err)
	defer devNull.Close()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	file, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	require.NoError(t, err)
	defer file.Close()

	assert.False(t, isTerminal(devNull), "/dev/null")
	assert.False(t, isTerminal(r), "pipe")
	assert.False(t, isTerminal(file), "file")
}
