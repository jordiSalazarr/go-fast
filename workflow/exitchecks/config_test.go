package exitchecks_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
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
	assert.Equal(t, exitchecks.Config{Timeout: 10 * time.Minute, Decisions: domain.DefaultDecisionsDir()}, config)
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
		Decisions:   domain.DefaultDecisionsDir(),
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

func TestLoadConfig_ReadsTheStressSection(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"stress": {
		"start": ["go", "run", "./cmd/server"], "ready": "http://localhost:8080/healthz", "baseUrl": "http://localhost:8080",
		"startTimeout": "30s", "timeout": "20m"}}`)

	config, err := exitchecks.LoadConfig(root)

	require.NoError(t, err)
	assert.Equal(t, &exitchecks.Stress{
		Start: []string{"go", "run", "./cmd/server"}, Ready: "http://localhost:8080/healthz", BaseURL: "http://localhost:8080",
		StartTimeout: 30 * time.Second, Timeout: 20 * time.Minute,
	}, config.Stress)
}

func TestLoadConfig_StressTimeoutsHaveDefaults(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"stress": {"start": ["./server"], "ready": "http://127.0.0.1:9000/", "baseUrl": "http://127.0.0.1:9000"}}`)

	config, err := exitchecks.LoadConfig(root)

	require.NoError(t, err)
	assert.Equal(t, time.Minute, config.Stress.StartTimeout)
	assert.Equal(t, 15*time.Minute, config.Stress.Timeout)
}

func TestLoadConfig_RejectsWhatItCannotUse(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":             `{`,
		"unknown field":        `{"integartion": {"command": ["x"]}}`,
		"command as string":    `{"integration": {"command": "go test ./..."}}`,
		"empty command":        `{"integration": {"command": []}}`,
		"blank program":        `{"integration": {"command": [" "]}}`,
		"bad timeout":          `{"timeout": "ten minutes"}`,
		"negative timeout":     `{"timeout": "-1m"}`,
		"bad integration":      `{"integration": {"command": ["x"], "timeout": "0s"}}`,
		"stress without start": `{"stress": {"ready": "http://a/", "baseUrl": "http://a"}}`,
		"stress ready not URL": `{"stress": {"start": ["x"], "ready": "localhost:8080/healthz", "baseUrl": "http://a"}}`,
		"stress no base URL":   `{"stress": {"start": ["x"], "ready": "http://a/"}}`,
		"stress bad timeout":   `{"stress": {"start": ["x"], "ready": "http://a/", "baseUrl": "http://a", "timeout": "long"}}`,
		"stress unknown field": `{"stress": {"start": ["x"], "ready": "http://a/", "baseUrl": "http://a", "vus": 5}}`,
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

func TestLoadConfig_ReadsTheDecisionsDirectory(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"docs": {"decisionsDir": "adr/"}}`)

	config, err := exitchecks.LoadConfig(root)

	require.NoError(t, err)
	assert.Equal(t, "adr", config.Decisions.Path().String())
}

func TestLoadConfig_RefusesADecisionsDirectoryOutsideTheRepositoryOrInGofast(t *testing.T) {
	for _, dir := range []string{"../adr", "/tmp/adr", ".gofast/decisions", "."} {
		root := t.TempDir()
		writeConfig(t, root, `{"docs": {"decisionsDir": "`+dir+`"}}`)

		_, err := exitchecks.LoadConfig(root)

		var invalid *exitchecks.ConfigError
		require.ErrorAs(t, err, &invalid, dir)
		assert.Contains(t, invalid.Problem, `"docs.decisionsDir" must be a directory of the repository outside .gofast/`)
	}
}
