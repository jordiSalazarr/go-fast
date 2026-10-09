// Package gitbranch tells what a git repository has checked out: the branch,
// and the commit, with the event log committed in it.
package gitbranch

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
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

// Commits tells the event log's seal what git has committed, so a log changed
// by checkout, merge, pull or rebase is not mistaken for tampering.
type Commits struct{ root string }

func NewCommits(root string) Commits { return Commits{root: root} }

// Head is the commit HEAD points at; empty before the first commit.
func (c Commits) Head() (string, error) {
	out, err := exec.Command("git", "-C", c.root, "rev-parse", "-q", "--verify", "HEAD").Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("HEAD of %s: %w", c.root, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// LogAt is the event log committed at commit; nil when it has none.
func (c Commits) LogAt(commit string) ([]byte, error) {
	if commit == "" {
		return nil, nil
	}
	object := commit + ":" + eventlog.LogPath()
	if err := exec.Command("git", "-C", c.root, "cat-file", "-e", object).Run(); err != nil {
		return nil, nil
	}
	out, err := exec.Command("git", "-C", c.root, "cat-file", "blob", object).Output()
	if err != nil {
		return nil, fmt.Errorf("event log at %s: %w", commit, err)
	}
	return out, nil
}

// LogsOnBranches are the event logs committed at the tips of the local
// branches that have one.
func (c Commits) LogsOnBranches() ([][]byte, error) {
	tips, err := exec.Command("git", "-C", c.root, "for-each-ref", "--format=%(objectname)", "refs/heads/").Output()
	if err != nil {
		return nil, fmt.Errorf("branch tips of %s: %w", c.root, err)
	}
	var query bytes.Buffer
	for _, tip := range strings.Fields(string(tips)) {
		query.WriteString(tip + ":" + eventlog.LogPath() + "\n")
	}
	if query.Len() == 0 {
		return nil, nil
	}
	cmd := exec.Command("git", "-C", c.root, "cat-file", "--batch")
	cmd.Stdin = &query
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("event logs on branches of %s: %w", c.root, err)
	}
	// Each answer is "<oid> blob <size>\n<content>\n", or "<name> missing\n".
	var logs [][]byte
	r := bufio.NewReader(bytes.NewReader(out))
	for {
		header, err := r.ReadString('\n')
		if err != nil {
			return logs, nil
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		var size int
		if _, err := fmt.Sscan(fields[2], &size); err != nil {
			return nil, fmt.Errorf("event logs on branches of %s: size %q: %w", c.root, fields[2], err)
		}
		content := make([]byte, size+1) // and the newline after it
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, fmt.Errorf("event logs on branches of %s: %w", c.root, err)
		}
		logs = append(logs, content[:size])
	}
}
