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
// a temporary index, seeded from the real one (with its timestamp) so
// unchanged files are not rehashed, and never touches the real index.
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
	if err := dst.Close(); err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	// Git rechecks the content of entries as new as the index file ("racy
	// git"). A copy dated now would make a same-size edit made in the second
	// the real index was written look unchanged.
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	if err := os.Chtimes(to, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	return nil
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
	changed, err = b.changedSinceTree(id, base)
	return changed, false, err
}

// ErrNoWorkBaseline: gf has no baseline of a work's first stage visit, so
// it can't tell what the work changed.
var ErrNoWorkBaseline = errors.New("the baseline of the work's first stage visit is missing")

// WorkChanges returns the files a work changed since its first stage visit
// (first) began, except the ones gf itself writes.
func (b Baselines) WorkChanges(first domain.AssignmentID) (domain.ChangedFiles, error) {
	base, ok, err := b.read(first)
	if err != nil {
		return domain.ChangedFiles{}, err
	}
	if !ok {
		return domain.ChangedFiles{}, fmt.Errorf("changes since %s: %w", first, ErrNoWorkBaseline)
	}
	return b.changedSinceTree(first, base)
}

func (b Baselines) changedSinceTree(id domain.AssignmentID, base string) (domain.ChangedFiles, error) {
	current, err := Snapshot(b.root)
	if err != nil {
		return domain.ChangedFiles{}, fmt.Errorf("changes since baseline of %s: %w", id, err)
	}
	names, err := Diff(b.root, base, current)
	if err != nil {
		return domain.ChangedFiles{}, fmt.Errorf("changes since baseline of %s: %w", id, err)
	}
	added, err := Added(b.root, base, current)
	if err != nil {
		return domain.ChangedFiles{}, fmt.Errorf("changes since baseline of %s: %w", id, err)
	}
	paths, err := b.repoPaths(id, names)
	if err != nil {
		return domain.ChangedFiles{}, err
	}
	addedPaths, err := b.repoPaths(id, added)
	if err != nil {
		return domain.ChangedFiles{}, err
	}
	return domain.NewChangedFiles(paths...).WithAdded(addedPaths...), nil
}

// repoPaths are names as repository paths, except the files gf writes.
func (b Baselines) repoPaths(id domain.AssignmentID, names []string) ([]domain.RepoPath, error) {
	var paths []domain.RepoPath
	for _, name := range names {
		if gfWrites[name] {
			continue
		}
		p, err := domain.NewRepoPath(b.root, filepath.FromSlash(name))
		if err != nil {
			return nil, fmt.Errorf("changes since baseline of %s: %w", id, err)
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// Tree returns the baseline tree of a stage visit, if it was recorded on
// this machine and git still has it.
func (b Baselines) Tree(id domain.AssignmentID) (tree string, ok bool, err error) { return b.read(id) }

// Diff returns the slash-separated paths that differ between two trees.
func Diff(root, from, to string) ([]string, error) {
	return diff(root, from, to)
}

// Added returns the slash-separated paths in to that aren't in from.
func Added(root, from, to string) ([]string, error) {
	return diff(root, from, to, "--diff-filter=A")
}

func diff(root, from, to string, options ...string) ([]string, error) {
	args := append([]string{"diff", "--name-only", "-z", "--no-renames"}, options...)
	out, err := git(root, nil, append(args, from, to)...)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// FileAt returns a file's content in a tree; ok is false when the tree has
// no such file.
func FileAt(root, tree string, path domain.RepoPath) (content []byte, ok bool, err error) {
	object := tree + ":" + path.String()
	if _, err := git(root, nil, "cat-file", "-e", object); err != nil {
		return nil, false, nil
	}
	out, err := git(root, nil, "cat-file", "blob", object)
	if err != nil {
		return nil, false, err
	}
	return []byte(out), true, nil
}

// Uncommitted reports whether a file, relative to root and slash-separated,
// differs from what HEAD has: modified, staged, deleted or untracked.
func Uncommitted(root, path string) (bool, error) {
	out, err := git(root, nil, "status", "--porcelain", "--untracked-files=all", "--", path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Errors of CommittedFile.
var (
	ErrNotAFile     = errors.New("not a file")
	ErrNotCommitted = errors.New("not committed")
)

// CommittedFile returns the content of a file that is committed as it is in
// the working tree: tracked, and not modified, staged or deleted since HEAD.
func CommittedFile(root string, path domain.RepoPath) ([]byte, error) {
	abs := filepath.Join(root, filepath.FromSlash(path.String()))
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("%s: %w", path, ErrNotAFile)
	}
	if err != nil {
		return nil, err
	}
	if _, err := git(root, nil, "ls-files", "--error-unmatch", "--", path.String()); err != nil {
		return nil, fmt.Errorf("%s is not tracked: %w", path, ErrNotCommitted)
	}
	changed, err := Uncommitted(root, path.String())
	if err != nil {
		return nil, err
	}
	if changed {
		return nil, fmt.Errorf("%s has uncommitted changes: %w", path, ErrNotCommitted)
	}
	return os.ReadFile(abs)
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
