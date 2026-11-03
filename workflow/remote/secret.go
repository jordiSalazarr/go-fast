package remote

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// SecretPath is where gf keeps the project secret, relative to the
// repository root. .gofast/.gitignore lists it: it never leaves the
// machine through git, so each clone saves it once (`gf sync secret`).
const SecretPath = ".gofast/" + eventlog.SecretFile

// Secret is the project secret gf syncs with, and where it came from.
type Secret struct {
	Value string
	// Source is "$GOFAST_PROJECT_SECRET" or SecretPath; empty without one.
	Source string
	// Exposed: the file can be read by other users of this machine.
	Exposed bool
}

var secretPattern = regexp.MustCompile(`^gfps_[0-9a-f]{64}$`)

// ValidSecret reports whether s looks like a project secret, as the
// history server makes them.
func ValidSecret(s string) bool { return secretPattern.MatchString(s) }

// LoadSecret is the project secret: $GOFAST_PROJECT_SECRET when set,
// which wins, or else the one saved in SecretPath.
func LoadSecret(root string, getenv func(string) string) (Secret, error) {
	if v := getenv(SecretEnv); v != "" {
		return Secret{Value: v, Source: "$" + SecretEnv}, nil
	}
	path := filepath.Join(root, filepath.FromSlash(SecretPath))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Secret{}, nil
	}
	if err != nil {
		return Secret{}, fmt.Errorf("read the project secret: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Secret{}, fmt.Errorf("read the project secret: %w", err)
	}
	return Secret{Value: strings.TrimSpace(string(data)), Source: SecretPath, Exposed: info.Mode().Perm()&0o077 != 0}, nil
}

// SaveSecret keeps secret in SecretPath, readable by this user only, and
// makes sure git ignores it.
func SaveSecret(root, secret string) error {
	if err := eventlog.Init(root); err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(SecretPath))
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return fmt.Errorf("save the project secret: %w", err)
	}
	return os.Chmod(path, 0o600) // WriteFile keeps an existing file's mode
}
