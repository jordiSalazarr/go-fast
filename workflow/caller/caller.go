// Package caller resolves who runs gf: the owner, identified by git config,
// or an agent, when GF_ACTOR=agent.
package caller

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const (
	EnvActor   = "GF_ACTOR"
	agentValue = "agent"
)

var (
	ErrNoOwnerIdentity = errors.New("owner identity unknown: git config user.name is not set")
	ErrOwnerOnly       = errors.New("only the owner can run this command")
)

// OwnerOnlyError is returned when an agent runs an owner-only command.
type OwnerOnlyError struct {
	Action  string // e.g. "approve"
	Command string // e.g. "gf approve"
}

func (e *OwnerOnlyError) Error() string {
	return fmt.Sprintf("%s: %s refused for agent", ErrOwnerOnly, e.Action)
}

func (e *OwnerOnlyError) Unwrap() error { return ErrOwnerOnly }

// Caller is who runs the current command.
type Caller struct {
	agent bool
	owner domain.Owner
}

// Resolve works out the caller from the environment and git config.
func Resolve(getenv func(string) string, gitConfig func(key string) (string, error)) (Caller, error) {
	if getenv(EnvActor) == agentValue {
		return Caller{agent: true}, nil
	}
	name, err := gitConfig("user.name")
	if err != nil {
		return Caller{}, fmt.Errorf("resolve owner: %w", err)
	}
	email, err := gitConfig("user.email")
	if err != nil {
		return Caller{}, fmt.Errorf("resolve owner: %w", err)
	}
	owner, err := domain.NewOwner(name, email)
	if err != nil {
		return Caller{}, fmt.Errorf("resolve owner: %w: %w", ErrNoOwnerIdentity, err)
	}
	return Caller{owner: owner}, nil
}

// Actor is how the caller is recorded on events.
func (c Caller) Actor() eventlog.Actor {
	if c.agent {
		return eventlog.AgentActor(agentValue)
	}
	return eventlog.OwnerActor(c.owner)
}

// RequireOwner returns the owner, or refuses an agent before the command
// reaches the domain.
func (c Caller) RequireOwner(action, command string) (domain.Owner, error) {
	if c.agent {
		return domain.Owner{}, &OwnerOnlyError{Action: action, Command: command}
	}
	return c.owner, nil
}

// GitConfig reads git config values of the repository at root. An unset key
// reads as empty.
func GitConfig(root string) func(key string) (string, error) {
	return func(key string) (string, error) {
		out, err := exec.Command("git", "-C", root, "config", "--get", key).Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("git config %s: %w", key, err)
		}
		return strings.TrimSpace(string(out)), nil
	}
}
