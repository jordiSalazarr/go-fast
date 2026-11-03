package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const config = `{"db": "/data/history.db", "organisations": {"acme": {"members": {"jordi": {"tokenSha256": "` +
	"5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8" + `"}}}}}`

func env(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

func TestLoadConfig_FromTheEnvironment_ListeningOnPORT(t *testing.T) {
	c, err := loadConfig("missing.json", env(map[string]string{"GOFAST_SERVER_CONFIG": config, "PORT": "4321"}))
	require.NoError(t, err)
	assert.Equal(t, ":4321", c.Listen)
	assert.Equal(t, "/data/history.db", c.DB)

	_, err = loadConfig("missing.json", env(map[string]string{"GOFAST_SERVER_CONFIG": `{"db": ""}`}))
	assert.ErrorContains(t, err, "$GOFAST_SERVER_CONFIG: set db")
}

func TestLoadConfig_FromTheFile_ListeningOn8080ByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	require.NoError(t, os.WriteFile(path, []byte(config), 0o644))
	c, err := loadConfig(path, env(nil))
	require.NoError(t, err)
	assert.Equal(t, ":8080", c.Listen)

	require.NoError(t, os.WriteFile(path, []byte(`{"listen": "127.0.0.1:9000", `+config[1:]), 0o644))
	c, err = loadConfig(path, env(map[string]string{"PORT": "4321"}))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9000", c.Listen, "the configuration's listen wins")
}
