// Package caller resolves who runs gf: the owner, identified by git config,
// or an agent, when GF_ACTOR=agent. Owner-only commands also need a terminal:
// Claude Code's Bash tool has none, whatever its environment says.
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

// OwnerOnlyError is returned when an agent, or a caller without a terminal,
// runs an owner-only command.
type OwnerOnlyError struct {
	Action     string // e.g. "approve"
	Command    string // e.g. "gf approve"
	NoTerminal bool   // refused because stdin is not a terminal, not because of GF_ACTOR
}

func (e *OwnerOnlyError) Error() string {
	if e.NoTerminal {
		return fmt.Sprintf("%s: %s refused: stdin is not a terminal", ErrOwnerOnly, e.Action)
	}
	return fmt.Sprintf("%s: %s refused for agent", ErrOwnerOnly, e.Action)
}

func (e *OwnerOnlyError) Unwrap() error { return ErrOwnerOnly }

// Caller is who runs the current command.
type Caller struct {
	agent    bool
	terminal bool
	owner    domain.Owner
}

// Resolve works out the caller from the environment, git config and whether
// stdin is a terminal.
func Resolve(getenv func(string) string, gitConfig func(key string) (string, error), terminal bool) (Caller, error) {
	if getenv(EnvActor) == agentValue {
		return Caller{agent: true, terminal: terminal}, nil
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
	return Caller{owner: owner, terminal: terminal}, nil
}

// Actor is how the caller is recorded on events.
func (c Caller) Actor() eventlog.Actor {
	if c.agent {
		return eventlog.AgentActor(agentValue)
	}
	return eventlog.OwnerActor(c.owner)
}

// RequireOwner returns the owner, or refuses before the command reaches the
// domain: an agent (GF_ACTOR=agent), and anyone without a terminal, since
// GF_ACTOR is easy to remove (`env -i`).
func (c Caller) RequireOwner(action, command string) (domain.Owner, error) {
	if c.agent {
		return domain.Owner{}, &OwnerOnlyError{Action: action, Command: command}
	}
	if !c.terminal {
		return domain.Owner{}, &OwnerOnlyError{Action: action, Command: command, NoTerminal: true}
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
