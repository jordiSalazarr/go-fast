// Package exitchecks runs the exit checks a stage requires and reports how
// each went. It never decides whether a stage passes; the domain does.
package exitchecks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// ConfigPath is the per-repository check configuration, relative to the
// repository root. The owner writes and commits it; it sits outside every
// artifact directory, so agents can't change it.
const ConfigPath = ".gofast/checks.json"

// DefaultTimeout bounds each command a check runs, unless configured.
const DefaultTimeout = 10 * time.Minute

// Config is what a repository configures about its exit checks: what counts
// as its integration suite, how to run its service for stress testing, how
// long a check's command may take, and where grill's decision documents go
// (docs.decisionsDir).
type Config struct {
	// Integration is nil when the repository has no integration command;
	// integration-passes then fails.
	Integration *Command
	// Stress is nil when the repository can't be stress tested; the
	// stress-testing stage then fails closed.
	Stress  *Stress
	Timeout time.Duration
	// Decisions is where grill writes its decision documents and gf their
	// index.
	Decisions domain.DecisionsDir
	// Configured is false when there is no ConfigPath; the defaults apply.
	Configured bool
}

// Stress is how gf runs the repository's service for stress testing: the
// command that starts it, the URL that answers 2xx once it is ready, and the
// base URL the k6 scripts send their requests to.
type Stress struct {
	Start        []string
	Ready        string
	BaseURL      string
	StartTimeout time.Duration // to become ready
	Timeout      time.Duration // for each k6 run
}

// Stress defaults.
const (
	DefaultStressStartTimeout = time.Minute
	DefaultStressTimeout      = 15 * time.Minute
)

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
	Stress *struct {
		Start        []string `json:"start"`
		Ready        string   `json:"ready"`
		BaseURL      string   `json:"baseUrl"`
		StartTimeout string   `json:"startTimeout"`
		Timeout      string   `json:"timeout"`
	} `json:"stress"`
	Timeout string `json:"timeout"`
	Docs    *struct {
		DecisionsDir string `json:"decisionsDir"`
	} `json:"docs"`
}

// LoadConfig reads the check configuration of the repository at root. A
// missing file is the default configuration, with no integration command.
func LoadConfig(root string) (Config, error) {
	config := Config{Timeout: DefaultTimeout, Decisions: domain.DefaultDecisionsDir()}
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
	if d := file.Docs; d != nil && d.DecisionsDir != "" {
		path, err := domain.ParseRepoPath(strings.TrimSuffix(d.DecisionsDir, "/"))
		if err == nil {
			config.Decisions, err = domain.NewDecisionsDir(path)
		}
		if err != nil {
			return Config{}, &ConfigError{Problem: fmt.Sprintf(`"docs.decisionsDir" must be a directory of the repository outside .gofast/, relative to its root, such as "docs/decisions", not %q`, d.DecisionsDir)}
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
	if st := file.Stress; st != nil {
		stress := Stress{Start: st.Start, Ready: st.Ready, BaseURL: st.BaseURL, StartTimeout: DefaultStressStartTimeout, Timeout: DefaultStressTimeout}
		if len(st.Start) == 0 || strings.TrimSpace(st.Start[0]) == "" {
			return Config{}, &ConfigError{Problem: `"stress.start" must name the program that starts the service, e.g. ["go", "run", "./cmd/server"]`}
		}
		for field, u := range map[string]string{"stress.ready": st.Ready, "stress.baseUrl": st.BaseURL} {
			if !httpURL(u) {
				return Config{}, &ConfigError{Problem: fmt.Sprintf("%q must be an http:// or https:// URL, not %q", field, u)}
			}
		}
		if st.StartTimeout != "" {
			if stress.StartTimeout, err = parseTimeout("stress.startTimeout", st.StartTimeout); err != nil {
				return Config{}, err
			}
		}
		if st.Timeout != "" {
			if stress.Timeout, err = parseTimeout("stress.timeout", st.Timeout); err != nil {
				return Config{}, err
			}
		}
		config.Stress = &stress
	}
	return config, nil
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func parseTimeout(field, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, &ConfigError{Problem: fmt.Sprintf("%q must be a positive Go duration such as \"10m\", not %q", field, s)}
	}
	return d, nil
}
