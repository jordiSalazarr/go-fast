// Package exitchecks runs the exit checks a stage requires and reports how
// each went. It never decides whether a stage passes; the domain does.
package exitchecks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConfigPath is the per-repository check configuration, relative to the
// repository root. The owner writes and commits it; it sits outside every
// artifact directory, so agents can't change it.
const ConfigPath = ".gofast/checks.json"

// DefaultTimeout bounds each command a check runs, unless configured.
const DefaultTimeout = 10 * time.Minute

// Config is what a repository configures about its exit checks: what counts
// as its integration suite, and how long a check's command may take.
type Config struct {
	// Integration is nil when the repository has no integration command;
	// integration-passes then fails.
	Integration *Command
	Timeout     time.Duration
	// Configured is false when there is no ConfigPath; the defaults apply.
	Configured bool
}

// Command is a program and its arguments, run without a shell.
type Command struct {
	Argv    []string
	Timeout time.Duration
}

func (c Command) String() string { return strings.Join(c.Argv, " ") }

// ConfigError says what is wrong with ConfigPath.
type ConfigError struct{ Problem string }

func (e *ConfigError) Error() string { return ConfigPath + ": " + e.Problem }

// configFile is ConfigPath's JSON form.
type configFile struct {
	Integration *struct {
		Command []string `json:"command"`
		Timeout string   `json:"timeout"`
	} `json:"integration"`
	Timeout string `json:"timeout"`
}

// LoadConfig reads the check configuration of the repository at root. A
// missing file is the default configuration, with no integration command.
func LoadConfig(root string) (Config, error) {
	config := Config{Timeout: DefaultTimeout}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ConfigPath)))
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read check configuration: %w", err)
	}
	config.Configured = true
	var file configFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return Config{}, &ConfigError{Problem: "not valid JSON for gf: " + err.Error()}
	}
	if file.Timeout != "" {
		if config.Timeout, err = parseTimeout("timeout", file.Timeout); err != nil {
			return Config{}, err
		}
	}
	if i := file.Integration; i != nil {
		if len(i.Command) == 0 || strings.TrimSpace(i.Command[0]) == "" {
			return Config{}, &ConfigError{Problem: `"integration.command" must name a program, e.g. ["go", "test", "-tags=integration", "./..."]`}
		}
		command := Command{Argv: i.Command, Timeout: config.Timeout}
		if i.Timeout != "" {
			if command.Timeout, err = parseTimeout("integration.timeout", i.Timeout); err != nil {
				return Config{}, err
			}
		}
		config.Integration = &command
	}
	return config, nil
}

func parseTimeout(field, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, &ConfigError{Problem: fmt.Sprintf("%q must be a positive Go duration such as \"10m\", not %q", field, s)}
	}
	return d, nil
}
