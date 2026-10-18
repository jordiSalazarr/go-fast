package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

var assignment = func() domain.AssignmentID {
	id, err := domain.ParseAssignmentID("w1-specify-1")
	if err != nil {
		panic(err)
	}
	return id
}()

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	writeFile(t, root, "main.go", "package main\n")
	writeFile(t, root, ".gitignore", "build/\n")
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.name=T", "-c", "user.email=t@x", "commit", "-q", "-m", "initial")
	require.NoError(t, eventlog.Init(root))
	return root
}

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func changed(t *testing.T, root string) []string {
	t.Helper()
	files, started, err := worktree.OpenBaselines(root).ChangedSince(assignment)
	require.NoError(t, err)
	require.False(t, started)
	var names []string
	for _, p := range files.Paths() {
		names = append(names, p.String())
	}
	return names
}

func TestChangedSince_SeesEveryWayOfChangingFiles(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "dirty.go", "package main // uncommitted before the visit\n")
	taken, err := worktree.OpenBaselines(root).Ensure(assignment)
	require.NoError(t, err)
	require.True(t, taken)

	writeFile(t, root, "main.go", "package main // edited\n")        // tracked, modified
	writeFile(t, root, "internal/x.go", "package internal\n")        // untracked, new
	require.NoError(t, os.Remove(filepath.Join(root, "dirty.go")))   // deleted
	writeFile(t, root, "build/out.bin", "ignored")                   // ignored by .gitignore
	writeFile(t, root, ".gofast/events.jsonl", "{}\n")               // written by gf
	writeFile(t, root, ".gofast/instructions/0a1b/SKILL.md", "kept") // written by gf's hooks
	writeFile(t, root, ".gofast/gf.log", "ignored by .gofast/.gitignore")
	writeFile(t, root, ".gofast/works/w1/specify-v1.md", "artifact")
	git(t, root, "-c", "user.name=T", "-c", "user.email=t@x", "commit", "-q", "-am", "committing hides nothing")

	assert.Equal(t, []string{".gofast/works/w1/specify-v1.md", "dirty.go", "internal/x.go", "main.go"}, changed(t, root))
}

func TestChangedSince_NothingChanged(t *testing.T) {
	root := newRepo(t)
	_, err := worktree.OpenBaselines(root).Ensure(assignment)
	require.NoError(t, err)

	assert.Empty(t, changed(t, root))
}

func TestChangedSince_DoesNotTouchTheRealIndex(t *testing.T) {
	root := newRepo(t)
	_, err := worktree.OpenBaselines(root).Ensure(assignment)
	require.NoError(t, err)
	writeFile(t, root, "new.go", "package main\n")

	changed(t, root)

	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "?? new.go", "still untracked: nothing was staged")
}

func TestEnsure_KeepsTheFirstBaseline(t *testing.T) {
	root := newRepo(t)
	baselines := worktree.OpenBaselines(root)
	_, err := baselines.Ensure(assignment)
	require.NoError(t, err)
	writeFile(t, root, "main.go", "package main // edited\n")

	taken, err := baselines.Ensure(assignment)

	require.NoError(t, err)
	assert.False(t, taken)
	assert.Equal(t, []string{"main.go"}, changed(t, root))
}

func TestChangedSince_WithoutBaseline_StartsNow(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, "main.go", "package main // edited before the baseline\n")
	baselines := worktree.OpenBaselines(root)

	files, started, err := baselines.ChangedSince(assignment)

	require.NoError(t, err)
	assert.True(t, started)
	assert.Empty(t, files.Paths())
	assert.Empty(t, changed(t, root), "the baseline taken now is used from then on")
}

func TestChangedSince_BaselineGitNoLongerHas_StartsNow(t *testing.T) {
	root := newRepo(t)
	dir := filepath.Join(root, ".gofast", "runtime", "baselines")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, assignment.String()), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644))

	_, started, err := worktree.OpenBaselines(root).ChangedSince(assignment)

	require.NoError(t, err)
	assert.True(t, started)
}

func TestSnapshot_WorksBeforeTheFirstCommit(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	writeFile(t, root, "a.go", "package a\n")

	tree, err := worktree.Snapshot(root)

	require.NoError(t, err)
	assert.Len(t, tree, 40)
}

func TestUncommitted(t *testing.T) {
	root := newRepo(t)
	uncommitted := func(path string) bool {
		t.Helper()
		got, err := worktree.Uncommitted(root, path)
		require.NoError(t, err)
		return got
	}

	assert.False(t, uncommitted("main.go"), "committed")
	assert.False(t, uncommitted("absent.json"), "neither committed nor present")
	writeFile(t, root, "dir/new.json", "{}")
	assert.True(t, uncommitted("dir/new.json"), "untracked, in an untracked directory")
	git(t, root, "add", "dir/new.json")
	assert.True(t, uncommitted("dir/new.json"), "staged")
	git(t, root, "-c", "user.name=T", "-c", "user.email=t@x", "commit", "-q", "-m", "add")
	assert.False(t, uncommitted("dir/new.json"), "committed")
	writeFile(t, root, "dir/new.json", "{\"x\": 1}")
	assert.True(t, uncommitted("dir/new.json"), "modified")
	require.NoError(t, os.Remove(filepath.Join(root, "dir", "new.json")))
	assert.True(t, uncommitted("dir/new.json"), "deleted")
}

func TestTreeDiffAndFileAt_ReadBaselines(t *testing.T) {
	root := newRepo(t)
	baselines := worktree.OpenBaselines(root)
	_, ok, err := baselines.Tree(assignment)
	require.NoError(t, err)
	require.False(t, ok, "no baseline yet")
	_, err = baselines.Ensure(assignment)
	require.NoError(t, err)
	base, ok, err := baselines.Tree(assignment)
	require.NoError(t, err)
	require.True(t, ok)

	writeFile(t, root, "main.go", "package main // edited\n")
	writeFile(t, root, "new_test.go", "package main\n")
	now, err := worktree.Snapshot(root)
	require.NoError(t, err)

	names, err := worktree.Diff(root, base, now)
	require.NoError(t, err)
	assert.Equal(t, []string{"main.go", "new_test.go"}, names)

	mainGo, err := domain.NewRepoPath(root, "main.go")
	require.NoError(t, err)
	content, ok, err := worktree.FileAt(root, base, mainGo)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "package main\n", string(content))

	newTest, err := domain.NewRepoPath(root, "new_test.go")
	require.NoError(t, err)
	_, ok, err = worktree.FileAt(root, base, newTest)
	require.NoError(t, err)
	assert.False(t, ok, "not in the baseline")
}

// Git trusts an index entry whose file stat is unchanged, unless the file
// is as new as the index itself ("racy git"). A same-size edit in the second
// the index was written must be seen even when the snapshot is taken later.
func TestChangedSince_SeesASameSizeEditMadeInTheSecondOfTheLastCommit(t *testing.T) {
	root := newRepo(t)
	start := time.Now().Truncate(time.Second).Add(time.Second)
	time.Sleep(time.Until(start))

	writeFile(t, root, "main.go", "package main // 1\n")
	git(t, root, "-c", "user.name=T", "-c", "user.email=t@x", "commit", "-q", "-am", "one")
	_, err := worktree.OpenBaselines(root).Ensure(assignment)
	require.NoError(t, err)
	writeFile(t, root, "main.go", "package main // 2\n") // same size
	if time.Since(start) >= time.Second {
		t.Skip("too slow to stay within one second")
	}
	time.Sleep(time.Until(start.Add(time.Second)))

	assert.Equal(t, []string{"main.go"}, changed(t, root))
}
