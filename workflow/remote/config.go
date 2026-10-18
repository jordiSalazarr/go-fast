// Package remote syncs a repository's work history to a gofast history
// server: the event log, and the files its events tell about (the works'
// artifacts and decision documents, and the kept versions of the plugin's
// instructions). The event log stays the source of truth; the server holds
// an append-only copy and never feeds anything back into the workflow.
package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConfigPath is the sync configuration, relative to the repository root.
// It is committed; the token never is.
const ConfigPath = ".gofast/sync.json"

// TokenEnv holds the project's token.
const TokenEnv = "GOFAST_SYNC_TOKEN"

// What a sync may send, besides the events, which it always sends.
const (
	IncludeEvents       = "events"
	IncludeInstructions = "instructions"
	IncludeArtifacts    = "artifacts"
	IncludeDecisions    = "decisions"
)

// includes are the known values of "include", in the order gf shows them.
var includes = []string{IncludeEvents, IncludeInstructions, IncludeArtifacts, IncludeDecisions}

// Config is the sync configuration. Without a file, Configured is false.
type Config struct {
	Configured bool
	Server     *url.URL
	Project    string
	include    map[string]bool
}

// Includes reports whether a sync sends what.
func (c Config) Includes(what string) bool { return c.include[what] }

// Included lists what a sync sends, in a stable order.
func (c Config) Included() []string {
	var out []string
	for _, i := range includes {
		if c.include[i] {
			out = append(out, i)
		}
	}
	return out
}

// ConfigError says what is wrong with ConfigPath.
type ConfigError struct{ Problem string }

func (e *ConfigError) Error() string { return ConfigPath + ": " + e.Problem }

var projectID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type configFile struct {
	Server  string   `json:"server"`
	Project string   `json:"project"`
	Include []string `json:"include"`
}

// LoadConfig reads the sync configuration of the repository at root.
func LoadConfig(root string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ConfigPath)))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read sync configuration: %w", err)
	}
	var file configFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return Config{}, &ConfigError{Problem: "not valid JSON for gf: " + err.Error()}
	}
	server, err := url.Parse(strings.TrimSuffix(file.Server, "/"))
	if err != nil || (server.Scheme != "http" && server.Scheme != "https") || server.Host == "" || server.RawQuery != "" || server.Fragment != "" {
		return Config{}, &ConfigError{Problem: fmt.Sprintf(`"server" must be the history server's http:// or https:// URL, such as "https://history.example.com", not %q`, file.Server)}
	}
	if !projectID.MatchString(file.Project) {
		return Config{}, &ConfigError{Problem: fmt.Sprintf(`"project" must be the project's id on the server: lowercase letters, digits and dashes, such as "wallets", not %q`, file.Project)}
	}
	config := Config{Configured: true, Server: server, Project: file.Project, include: map[string]bool{}}
	if file.Include == nil {
		file.Include = includes
	}
	for _, i := range file.Include {
		known := false
		for _, k := range includes {
			known = known || i == k
		}
		if !known {
			return Config{}, &ConfigError{Problem: fmt.Sprintf(`"include" lists %q; it may list %s`, i, strings.Join(includes, ", "))}
		}
		config.include[i] = true
	}
	if !config.include[IncludeEvents] {
		return Config{}, &ConfigError{Problem: `"include" must list "events": the event log is what a sync is for`}
	}
	return config, nil
}

// Warnings are what the owner should fix before syncing: a missing token,
// and a server reached without TLS anywhere but on this machine.
func (c Config) Warnings(token string) []string {
	if !c.Configured {
		return nil
	}
	var out []string
	if token == "" {
		out = append(out, fmt.Sprintf("$%s is not set: gf sync needs the project's token. Set it in your own shell, never in a file.", TokenEnv))
	}
	if c.Server.Scheme != "https" && !loopback(c.Server.Hostname()) {
		out = append(out, fmt.Sprintf("the sync server %s is not https: the token and the history would cross the network unencrypted.", c.Server))
	}
	return out
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
