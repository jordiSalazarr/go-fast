package eventlog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// fakeGit is HEAD, the event log committed at each commit, and the commits
// at branch tips.
type fakeGit struct {
	head      string
	committed map[string][]byte
	tips      []string
}

func (g *fakeGit) Head() (string, error) { return g.head, nil }

func (g *fakeGit) LogAt(commit string) ([]byte, error) { return g.committed[commit], nil }

func (g *fakeGit) LogsOnBranches() ([][]byte, error) {
	var logs [][]byte
	for _, tip := range g.tips {
		logs = append(logs, g.committed[tip])
	}
	return logs, nil
}

func sealedStore(t *testing.T) (*Store, string, *fakeGit) {
	t.Helper()
	git := &fakeGit{head: "c1", committed: map[string][]byte{}}
	s, root := openStore(t, WithSeal(git))
	return s, root, git
}

func appendStarted(t *testing.T, s *Store, id string) {
	t.Helper()
	events := startedWork(id, "main")
	require.NoError(t, s.Exclusive(func(sess *Session) error {
		return sess.Append(WorkStream(must(domain.NewWorkID(id))), 0, agent, events...)
	}))
}

func appendRaw(t *testing.T, root string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(eventsPath(root), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.Write(data)
	require.NoError(t, err)
}

func writeAttempt(s *Store) error {
	return s.Exclusive(func(*Session) error { return nil })
}

func readChanged(t *testing.T, s *Store) bool {
	t.Helper()
	var changed bool
	require.NoError(t, s.Shared(func(snap *Snapshot) error {
		changed = snap.ChangedOutsideGf()
		return nil
	}))
	return changed
}

func TestSeal_AppendsByGfKeepTheLogSealed(t *testing.T) {
	s, root, _ := sealedStore(t)

	appendStarted(t, s, "w1")
	appendStarted(t, s, "w2")

	require.FileExists(t, filepath.Join(root, dirName, RuntimeDir, sealFile))
	assert.NoError(t, writeAttempt(s))
	assert.False(t, readChanged(t, s))
}

func TestSeal_LinesAppendedOutsideGf_BlockWritesAndWarnReaders(t *testing.T) {
	s, root, _ := sealedStore(t)
	appendStarted(t, s, "w1")
	data, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)

	appendRaw(t, root, mustLine(t, recordAt(t, "w9", data)))

	err = writeAttempt(s)
	var changed *LogChangedError
	require.ErrorAs(t, err, &changed)
	assert.True(t, changed.Appended)
	assert.ErrorIs(t, err, ErrLogChangedOutsideGf)
	assert.True(t, readChanged(t, s), "readers still read, with a warning")
	assert.Len(t, readAll(t, s), 3)
}

func TestSeal_ContentRewrittenOutsideGf_BlocksWrites(t *testing.T) {
	s, root, _ := sealedStore(t)
	appendStarted(t, s, "w1")
	appendStarted(t, s, "w2")
	data, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(eventsPath(root), data[:len(data)/2+1], 0o644)) // drops lines

	err = writeAttempt(s)
	var changed *LogChangedError
	require.ErrorAs(t, err, &changed)
	assert.False(t, changed.Appended)
}

func TestSeal_Accept_SealsTheLogAsItIs(t *testing.T) {
	s, root, _ := sealedStore(t)
	appendStarted(t, s, "w1")
	data, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)
	appendRaw(t, root, mustLine(t, recordAt(t, "w9", data)))
	require.Error(t, writeAttempt(s))

	n, err := s.Accept()

	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.NoError(t, writeAttempt(s))
	assert.False(t, readChanged(t, s))
}

func TestSeal_Missing_IsCreatedWithoutComplaint(t *testing.T) {
	s, root, _ := sealedStore(t)
	appendStarted(t, s, "w1")
	require.NoError(t, os.Remove(filepath.Join(root, dirName, RuntimeDir, sealFile)))

	assert.False(t, readChanged(t, s))
	require.FileExists(t, filepath.Join(root, dirName, RuntimeDir, sealFile))
	appendRaw(t, root, []byte("{}\n"))
	assert.ErrorIs(t, writeAttempt(s), ErrLogChangedOutsideGf, "the recreated seal holds")
}

func TestSeal_LogGitCommittedAtANewHead_IsAccepted(t *testing.T) {
	s, root, git := sealedStore(t)
	appendStarted(t, s, "w1")
	appendStarted(t, s, "w2")
	data, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)

	// git checkout of a branch whose committed log has only w1.
	other := data[:len(data)/2]
	other = other[:lastNewline(other)+1]
	require.NoError(t, os.WriteFile(eventsPath(root), other, 0o644))
	git.head, git.committed["c2"] = "c2", other

	assert.NoError(t, writeAttempt(s))
	assert.False(t, readChanged(t, s))
}

func TestSeal_LogMissingAtANewHeadThatHasNone_IsAccepted(t *testing.T) {
	s, root, git := sealedStore(t)
	appendStarted(t, s, "w1")

	require.NoError(t, os.Remove(eventsPath(root))) // a branch from before gofast
	git.head = "c2"

	assert.False(t, readChanged(t, s))
	assert.NoError(t, writeAttempt(s))
}

func TestSeal_LogDifferentFromTheNewHead_IsStillChanged(t *testing.T) {
	s, root, git := sealedStore(t)
	appendStarted(t, s, "w1")
	appendRaw(t, root, []byte("{}\n"))
	git.head, git.committed["c2"] = "c2", []byte("something else\n")

	assert.ErrorIs(t, writeAttempt(s), ErrLogChangedOutsideGf)
}

func TestSeal_RevertedToTheCommittedLogWithoutMovingHead_IsChanged(t *testing.T) {
	s, root, git := sealedStore(t)
	appendStarted(t, s, "w1")
	committed, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)
	git.committed["c1"] = committed
	appendStarted(t, s, "w2")

	// git stash / git checkout -- .gofast/events.jsonl drops w2's events.
	require.NoError(t, os.WriteFile(eventsPath(root), committed, 0o644))

	assert.ErrorIs(t, writeAttempt(s), ErrLogChangedOutsideGf)
}

// gf sealed at c1, the owner committed the log as c2 without running gf, then
// checked out a branch still at c1: HEAD is back where the seal says, but
// what gf wrote is safe on a branch.
func TestSeal_BackAtTheSealedHeadWithTheSealedLogCommittedElsewhere_IsAccepted(t *testing.T) {
	s, root, git := sealedStore(t)
	git.committed["c1"] = nil
	appendStarted(t, s, "w1")
	written, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)
	git.committed["c2"], git.tips = written, []string{"c1", "c2"}

	require.NoError(t, os.Remove(eventsPath(root))) // c1 has no log

	assert.NoError(t, writeAttempt(s))
}

func TestSeal_Unsealed_StoreNeverWritesASeal(t *testing.T) {
	s, root := openStore(t)
	appendStarted(t, s, "w1")

	assert.NoFileExists(t, filepath.Join(root, dirName, RuntimeDir, sealFile))
	assert.NoError(t, writeAttempt(s))
}

// recordAt makes a valid line for a new work stream after the log's content.
func recordAt(t *testing.T, id string, data []byte) Recorded {
	t.Helper()
	records, err := parse(data)
	require.NoError(t, err)
	e := startedWork(id, "main")[0]
	return Recorded{ID: "x", Position: len(records) + 1, Stream: WorkStream(must(domain.NewWorkID(id))), Version: 1,
		Schema: schemaV1, OccurredAt: records[0].OccurredAt, Actor: agent, Event: e}
}

func lastNewline(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}
