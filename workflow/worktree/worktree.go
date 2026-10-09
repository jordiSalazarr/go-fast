// Package worktree snapshots the working tree with git, so gf can tell which
// files a stage visit changed, however they were written. A stage visit's
// baseline is a git tree of the working state when it began, kept in
// .gofast/runtime/baselines/<assignment id>.
package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Snapshot writes the working state as a git tree and returns its id: tracked
// files and untracked, non-ignored ones, as `git add -A` stages them. It uses
// a temporary index, seeded from the real one so unchanged files are not
// rehashed, and never touches the real index.
func Snapshot(root string) (string, error) {
	tmp, err := os.MkdirTemp("", "gf-index-")
	if err != nil {
		return "", fmt.Errorf("snapshot working tree: %w", err)
	}
	defer os.RemoveAll(tmp)
	index := filepath.Join(tmp, "index")
	if err := copyIndex(root, index); err != nil {
		return "", fmt.Errorf("snapshot working tree: %w", err)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+index)
	if _, err := git(root, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("snapshot working tree: %w", err)
	}
	tree, err := git(root, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("snapshot working tree: %w", err)
	}
	return strings.TrimSpace(tree), nil
}

func copyIndex(root, to string) error {
	path, err := git(root, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	src, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // no index yet: start empty
	}
	if err != nil {
		return fmt.Errorf("read index: %w", err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	return dst.Close()
}

// Baselines are the stage visits' baselines in one repository.
type Baselines struct{ root, dir string }

func OpenBaselines(root string) Baselines {
	return Baselines{root: root, dir: filepath.Join(eventlog.Dir(root), eventlog.RuntimeDir, "baselines")}
}

// Ensure records the baseline of a stage visit unless it has one. It reports
// whether it took one now.
func (b Baselines) Ensure(id domain.AssignmentID) (taken bool, err error) {
	if _, ok, err := b.read(id); err != nil || ok {
		return false, err
	}
	tree, err := Snapshot(b.root)
	if err != nil {
		return false, fmt.Errorf("baseline of %s: %w", id, err)
	}
	if err := b.write(id, tree); err != nil {
		return false, err
	}
	return true, nil
}

// ChangedSince returns the files changed since the stage visit's baseline,
// except the ones gf itself writes. Without a baseline it takes one now,
// reports started, and nothing has changed yet.
func (b Baselines) ChangedSince(id domain.AssignmentID) (changed domain.ChangedFiles, started bool, err error) {
	base, ok, err := b.read(id)
	if err != nil {
		return domain.ChangedFiles{}, false, err
	}
	if !ok {
		started, err := b.Ensure(id)
		return domain.ChangedFiles{}, started, err
	}
	current, err := Snapshot(b.root)
	if err != nil {
		return domain.ChangedFiles{}, false, fmt.Errorf("changes since baseline of %s: %w", id, err)
	}
	out, err := git(b.root, nil, "diff", "--name-only", "-z", "--no-renames", base, current)
	if err != nil {
		return domain.ChangedFiles{}, false, fmt.Errorf("changes since baseline of %s: %w", id, err)
	}
	var paths []domain.RepoPath
	for _, name := range strings.Split(out, "\x00") {
		if name == "" || gfWrites[name] {
			continue
		}
		p, err := domain.NewRepoPath(b.root, filepath.FromSlash(name))
		if err != nil {
			return domain.ChangedFiles{}, false, fmt.Errorf("changes since baseline of %s: %w", id, err)
		}
		paths = append(paths, p)
	}
	return domain.NewChangedFiles(paths...), false, nil
}

// gfWrites are the committed files gf itself changes during a stage visit.
var gfWrites = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range eventlog.CommittedFiles() {
		m[f] = true
	}
	return m
}()

// read returns the baseline tree of a stage visit, if it was recorded and git
// still has it (git gc prunes unreferenced trees after a while).
func (b Baselines) read(id domain.AssignmentID) (string, bool, error) {
	data, err := os.ReadFile(b.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read baseline of %s: %w", id, err)
	}
	tree := strings.TrimSpace(string(data))
	if _, err := git(b.root, nil, "cat-file", "-e", tree+"^{tree}"); err != nil {
		return "", false, nil
	}
	return tree, true, nil
}

// write writes atomically: a crash leaves no baseline or the whole one.
func (b Baselines) write(id domain.AssignmentID, tree string) error {
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", b.dir, err)
	}
	path := b.path(id)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tree+"\n"), 0o644); err != nil {
		return fmt.Errorf("write baseline of %s: %w", id, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write baseline of %s: %w", id, err)
	}
	return nil
}

func (b Baselines) path(id domain.AssignmentID) string { return filepath.Join(b.dir, id.String()) }

// git runs git in root and returns its stdout.
func git(root string, env []string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
