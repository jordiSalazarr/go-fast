package exitchecks_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

func writeConfig(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(exitchecks.ConfigPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestLoadConfig_WithoutAFile_IsTheDefaultWithoutIntegration(t *testing.T) {
	config, err := exitchecks.LoadConfig(t.TempDir())

	require.NoError(t, err)
	assert.Equal(t, exitchecks.Config{Timeout: 10 * time.Minute}, config)
}

func TestLoadConfig_ReadsTheIntegrationCommandAndTimeouts(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{
		"integration": { "command": ["go", "test", "-tags=integration", "./..."], "timeout": "15m" },
		"timeout": "2m"
	}`)

	config, err := exitchecks.LoadConfig(root)

	require.NoError(t, err)
	assert.Equal(t, exitchecks.Config{
		Integration: &exitchecks.Command{Argv: []string{"go", "test", "-tags=integration", "./..."}, Timeout: 15 * time.Minute},
		Timeout:     2 * time.Minute,
		Configured:  true,
	}, config)
}

func TestLoadConfig_IntegrationTimeoutDefaultsToTheCheckTimeout(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"integration": {"command": ["make", "it"]}, "timeout": "3m"}`)

	config, err := exitchecks.LoadConfig(root)

	require.NoError(t, err)
	assert.Equal(t, 3*time.Minute, config.Integration.Timeout)
}

func TestLoadConfig_RejectsWhatItCannotUse(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":          `{`,
		"unknown field":     `{"integartion": {"command": ["x"]}}`,
		"command as string": `{"integration": {"command": "go test ./..."}}`,
		"empty command":     `{"integration": {"command": []}}`,
		"blank program":     `{"integration": {"command": [" "]}}`,
		"bad timeout":       `{"timeout": "ten minutes"}`,
		"negative timeout":  `{"timeout": "-1m"}`,
		"bad integration":   `{"integration": {"command": ["x"], "timeout": "0s"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, content)

			_, err := exitchecks.LoadConfig(root)

			var configErr *exitchecks.ConfigError
			require.ErrorAs(t, err, &configErr)
		})
	}
}
