package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// Work belongs to the branch it was started on, end to end.

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir, "-c", "user.name=Jordi", "-c", "user.email=jordi@example.com"}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

func (r *repo) eventsFile() string { return filepath.Join(r.dir, ".gofast", "events.jsonl") }

func TestBranches_EachBranchHasItsOwnActiveWork(t *testing.T) {
	r := newRepo(t)
	r.git("commit", "-q", "--allow-empty", "-m", "initial")
	r.gf("start", "--type", "fix-bug", "orders double-charge")
	onMain := r.status()
	assert.Equal(t, "main", onMain.Work.Branch)

	r.git("checkout", "-q", "-b", "b")
	v := r.status()
	assert.Nil(t, v.Work, "no active work on b")
	assert.Equal(t, []progress.OtherBranchView{{
		Branch: "main", WorkID: onMain.Work.ID, Type: "fix-bug", Description: "orders double-charge", Stage: "discovery",
	}}, v.OtherBranches)
	text := r.gf("status")
	assert.Contains(t, text, "No active work on branch b.")
	assert.Contains(t, text, "fix-bug 'orders double-charge' is in progress on branch main.")

	out := r.gf("start", "--type", "fix-bug", "login button does nothing")
	assert.Contains(t, out, "on branch b")
	onB := r.status()
	assert.Equal(t, "b", onB.Work.Branch)
	assert.Equal(t, "login button does nothing", onB.Work.Description)

	r.git("checkout", "-q", "main")
	v = r.status()
	require.NotNil(t, v.Work)
	assert.Equal(t, onMain.Work.ID, v.Work.ID, "the original work is still active on main")
	assert.Equal(t, "b", v.OtherBranches[0].Branch)

	// Commands act on the current branch's work only.
	r.gf("submit", "--passed")
	assert.Equal(t, progress.AssignmentAwaitingApproval, r.status().Current.Assignment)
	r.git("checkout", "-q", "b")
	assert.Equal(t, progress.AssignmentOpen, r.status().Current.Assignment)
}

func TestBranches_DetachedHead_GetsAFriendlyMessage(t *testing.T) {
	r := newRepo(t)
	r.git("commit", "-q", "--allow-empty", "-m", "initial")
	r.git("checkout", "-q", "--detach")

	for _, args := range [][]string{{"status"}, {"start", "--type", "fix-bug", "x"}, {"submit", "--passed"}} {
		res := r.gfFails(nil, args...)
		assert.Equal(t, "You are not on a branch. Check out a branch before running gf.\n", res.stderr, args)
	}
}

// The event lines written on two branches, concatenated into one file, as a
// union merge leaves them: positions repeat.
func TestBranches_ConcatenatedLogsOfTwoBranches_StillWorkOnEach(t *testing.T) {
	r := newRepo(t)
	r.git("commit", "-q", "--allow-empty", "-m", "initial")
	r.git("branch", "a")
	r.git("branch", "b")

	r.git("checkout", "-q", "a")
	r.gf("start", "--type", "fix-bug", "work on a")
	r.gf("submit", "--passed")
	linesA, err := os.ReadFile(r.eventsFile())
	require.NoError(t, err)
	require.NoError(t, os.Remove(r.eventsFile()))

	r.git("checkout", "-q", "b")
	r.gf("start", "--type", "fix-bug", "work on b")
	linesB, err := os.ReadFile(r.eventsFile())
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(r.eventsFile(), append(linesA, linesB...), 0o644))

	v := r.status()
	assert.Equal(t, "work on b", v.Work.Description)
	assert.Equal(t, progress.AssignmentOpen, v.Current.Assignment)
	assert.Equal(t, "work on a", v.OtherBranches[0].Description)

	r.git("checkout", "-q", "a")
	v = r.status()
	assert.Equal(t, "work on a", v.Work.Description)
	assert.Equal(t, progress.AssignmentAwaitingApproval, v.Current.Assignment)

	// Writing after the merge continues from the highest position.
	r.gf("approve")
	assert.Equal(t, "specify", r.status().Current.Stage)
}

// A real git merge of two branches that both appended to the committed log:
// .gofast/.gitattributes makes it a union merge, without conflicts.
func TestBranches_GitMergeOfTheCommittedLog_HasNoConflict(t *testing.T) {
	r := newRepo(t)
	r.gf("status") // creates .gofast/ with .gitignore and .gitattributes
	r.git("add", ".gofast")
	r.git("commit", "-q", "-m", "initial")
	r.git("branch", "b")

	r.gf("start", "--type", "fix-bug", "work on main")
	r.git("add", ".gofast")
	r.git("commit", "-q", "-m", "start work on main")

	r.git("checkout", "-q", "b")
	r.gf("start", "--type", "fix-bug", "work on b")
	r.git("add", ".gofast")
	r.git("commit", "-q", "-m", "start work on b")

	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--no-edit", "b")

	v := r.status()
	assert.Equal(t, "work on main", v.Work.Description)
	assert.Equal(t, "work on b", v.OtherBranches[0].Description)
	assert.Equal(t, 2, count(r.eventTypes(), "WorkStarted"))
}
