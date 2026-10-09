// Package gitbranch tells which git branch a repository has checked out.
package gitbranch

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

var ErrDetachedHead = errors.New("HEAD is detached: not on a branch")

// Current returns the branch checked out in the repository at root, from
// `git symbolic-ref --short HEAD`. A branch with no commits yet counts.
func Current(root string) (domain.Branch, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("git", "-C", root, "symbolic-ref", "--short", "-q", "HEAD")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		// -q: exit 1 without a message when HEAD is not a symbolic ref.
		return domain.Branch{}, fmt.Errorf("current branch of %s: %w", root, ErrDetachedHead)
	}
	if err != nil {
		return domain.Branch{}, fmt.Errorf("current branch of %s: git symbolic-ref: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	branch, err := domain.NewBranch(strings.TrimSpace(string(out)))
	if err != nil {
		return domain.Branch{}, fmt.Errorf("current branch of %s: %w", root, err)
	}
	return branch, nil
}
