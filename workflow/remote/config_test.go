package remote_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/remote"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gofast"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gofast", "sync.json"), []byte(content), 0o644))
	return root
}

func TestLoadConfig_WithoutAFile_IsNotConfigured(t *testing.T) {
	c, err := remote.LoadConfig(t.TempDir())
	require.NoError(t, err)
	assert.False(t, c.Configured)
	assert.Empty(t, c.Warnings(remote.Secret{}))
}

func TestLoadConfig_IncludesEverythingByDefault(t *testing.T) {
	c, err := remote.LoadConfig(writeConfig(t, `{"server": "https://history.example.com/", "project": "wallets"}`))
	require.NoError(t, err)
	assert.True(t, c.Configured)
	assert.Equal(t, "https://history.example.com", c.Server.String())
	assert.Equal(t, "wallets", c.Project)
	assert.Equal(t, []string{"events", "instructions", "artifacts", "decisions"}, c.Included())

	c, err = remote.LoadConfig(writeConfig(t, `{"server": "https://h.example.com", "project": "wallets", "include": ["instructions", "events"]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"events", "instructions"}, c.Included())
	assert.False(t, c.Includes(remote.IncludeArtifacts))
}

func TestLoadConfig_RefusesWhatItCantUse(t *testing.T) {
	for content, problem := range map[string]string{
		`{"server": "ftp://h", "project": "wallets"}`:                             `"server" must be the history server's http:// or https:// URL`,
		`{"server": "https://h", "project": "Wallets!"}`:                          `"project" must be the project's id on the server`,
		`{"server": "https://h", "project": "wallets", "include": ["secrets"]}`:   `"include" lists "secrets"`,
		`{"server": "https://h", "project": "wallets", "include": ["artifacts"]}`: `"include" must list "events"`,
		`{"server": "https://h", "project": "wallets", "token": "s3cret"}`:        `not valid JSON for gf`,
	} {
		_, err := remote.LoadConfig(writeConfig(t, content))
		var invalid *remote.ConfigError
		require.ErrorAs(t, err, &invalid, content)
		assert.Contains(t, invalid.Problem, problem, content)
	}
}

func TestConfig_Warnings(t *testing.T) {
	c, err := remote.LoadConfig(writeConfig(t, `{"server": "http://history.internal", "project": "wallets"}`))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"gf sync has no project secret: run `gf sync secret` and paste the one the history server showed when the project was created (or set $GOFAST_PROJECT_SECRET).",
		"the sync server http://history.internal is not https: the secret and the history would cross the network unencrypted.",
	}, c.Warnings(remote.Secret{}))
	for _, local := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		c, err := remote.LoadConfig(writeConfig(t, `{"server": "`+local+`", "project": "wallets"}`))
		require.NoError(t, err)
		assert.Empty(t, c.Warnings(remote.Secret{Value: "s3cret", Source: remote.SecretPath}), local)
	}
	assert.Contains(t, c.Warnings(remote.Secret{Value: "s3cret", Source: remote.SecretPath, Exposed: true}),
		"other users of this machine can read .gofast/sync.secret: run chmod 600 .gofast/sync.secret.")
}

func TestSecret_TheEnvironmentWins_OverTheSavedOne(t *testing.T) {
	root := t.TempDir()
	none := func(string) string { return "" }
	s, err := remote.LoadSecret(root, none)
	require.NoError(t, err)
	assert.Equal(t, remote.Secret{}, s)

	secret := "gfps_" + strings.Repeat("ab", 32)
	require.True(t, remote.ValidSecret(secret))
	require.NoError(t, remote.SaveSecret(root, secret))
	s, err = remote.LoadSecret(root, none)
	require.NoError(t, err)
	assert.Equal(t, remote.Secret{Value: secret, Source: ".gofast/sync.secret"}, s)
	info, err := os.Stat(filepath.Join(root, ".gofast", "sync.secret"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	ignore, err := os.ReadFile(filepath.Join(root, ".gofast", ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(ignore), "sync.secret\n")

	require.NoError(t, os.Chmod(filepath.Join(root, ".gofast", "sync.secret"), 0o644))
	s, err = remote.LoadSecret(root, none)
	require.NoError(t, err)
	assert.True(t, s.Exposed)

	s, err = remote.LoadSecret(root, func(k string) string { return map[string]string{"GOFAST_PROJECT_SECRET": "from-env"}[k] })
	require.NoError(t, err)
	assert.Equal(t, remote.Secret{Value: "from-env", Source: "$GOFAST_PROJECT_SECRET"}, s)

	assert.False(t, remote.ValidSecret(strings.Repeat("ab", 32)), "a member's token isn't a project secret")
}
