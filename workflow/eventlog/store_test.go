package eventlog

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

var (
	workID       = must(domain.NewWorkID("w1"))
	assignmentID = domain.AssignmentIDFor(workID, domain.StageDiscovery, domain.FirstVisit())
	owner        = must(domain.NewOwner("Jordi", "jordi@example.com"))
	reason       = must(domain.NewReason("tests fail"))
	budget3      = must(domain.NewAttemptBudget(3))
	agent        = AgentActor("agent")
)

func allEvents() []domain.Event {
	return []domain.Event{
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("login broken"))},
		domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: domain.FirstVisit(), Gate: domain.GateHuman, Budget: budget3},
		domain.WorkCompleted{WorkID: workID},
		domain.WorkAbandoned{WorkID: workID, Owner: owner, Reason: reason},
		domain.AssignmentOpened{AssignmentID: assignmentID, WorkID: workID, Stage: domain.StageDiscovery, Visit: domain.FirstVisit(), Gate: domain.GateHuman, Budget: budget3},
		domain.AttemptFailed{AssignmentID: assignmentID, Attempt: must(domain.NewAttempt(1)), Reason: reason},
		domain.SubmittedForApproval{AssignmentID: assignmentID, Attempt: must(domain.NewAttempt(2))},
		domain.AssignmentRejected{AssignmentID: assignmentID, Owner: must(domain.NewOwner("Jordi", "")), Feedback: must(domain.NewFeedback("more")), Attempt: must(domain.NewAttempt(2))},
		domain.AssignmentEscalated{AssignmentID: assignmentID, AttemptsUsed: must(domain.NewAttemptCount(3)), Budget: budget3},
		domain.BudgetExtended{AssignmentID: assignmentID, Owner: owner, Additional: must(domain.NewAttemptBudget(2)), NewBudget: must(domain.NewAttemptBudget(5))},
		domain.AssignmentAccepted{AssignmentID: assignmentID, WorkID: workID, Stage: domain.StageDiscovery, Visit: domain.FirstVisit()},
		domain.AssignmentCancelled{AssignmentID: assignmentID, Reason: reason},
	}
}

func openStore(t *testing.T, opts ...Option) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Open(root, opts...)
	require.NoError(t, err)
	return s, root
}

func eventsPath(root string) string { return filepath.Join(root, dirName, eventsFile) }

func readAll(t *testing.T, s *Store) []Recorded {
	t.Helper()
	var records []Recorded
	require.NoError(t, s.Shared(func(snap *Snapshot) error {
		var err error
		records, err = snap.ReadAll()
		return err
	}))
	return records
}

func TestCodec_RoundTripsEveryEventType(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, e := range allEvents() {
		t.Run(fmt.Sprintf("%T", e), func(t *testing.T) {
			_, stream, _, err := encodeEvent(e)
			require.NoError(t, err)
			in := Recorded{ID: "id1", Position: 1, Stream: stream, Version: 1, Schema: schemaV1, OccurredAt: at, Actor: OwnerActor(owner), Event: e}

			line, err := encodeLine(in)
			require.NoError(t, err)
			out, err := decodeLine(line)

			require.NoError(t, err)
			assert.Equal(t, in, out)
		})
	}
}

func TestCodec_RejectsEventOnTheWrongStream(t *testing.T) {
	e := domain.WorkCompleted{WorkID: workID}
	_, err := encodeLine(Recorded{Stream: AssignmentStream(assignmentID), Event: e, Actor: agent, Schema: schemaV1})
	require.Error(t, err)
}

func TestOpen_CreatesDirectoryWithGitignore(t *testing.T) {
	_, root := openStore(t)

	ignore, err := os.ReadFile(filepath.Join(root, dirName, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "events.lock\ngf.log\n", string(ignore))
}

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	nested := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	got, err := FindRoot(nested)
	require.NoError(t, err)
	want, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	assert.Equal(t, want, gotResolved)

	_, err = FindRoot(t.TempDir())
	require.ErrorIs(t, err, ErrNoRepository)
}

func TestAppend_WritesEnvelopeAndReadsBack(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	ids := 0
	s, root := openStore(t, WithClock(func() time.Time { return at }), WithIDs(func() string { ids++; return fmt.Sprintf("e%d", ids) }))
	events := allEvents()[:2]

	require.NoError(t, s.Exclusive(func(sess *Session) error {
		return sess.Append(WorkStream(workID), 0, OwnerActor(owner), events...)
	}))

	records := readAll(t, s)
	require.Len(t, records, 2)
	assert.Equal(t, Recorded{
		ID: "e2", Position: 2, Stream: "work-w1", Version: 2, Schema: 1,
		OccurredAt: at.UTC(), Actor: Actor{Kind: ActorOwner, Name: "Jordi <jordi@example.com>"}, Event: events[1],
	}, records[1])

	raw, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	require.Len(t, lines, 2)
	assert.JSONEq(t, `{"id":"e1","position":1,"stream":"work-w1","version":1,"type":"WorkStarted","schema":1,
		"occurredAt":"2026-10-09T10:00:00Z","actor":{"kind":"owner","name":"Jordi <jordi@example.com>"},
		"payload":{"workId":"w1","workType":"fix-bug","description":"login broken"}}`, string(lines[0]))
}

func TestAppend_WithStaleVersion_Conflicts(t *testing.T) {
	s, _ := openStore(t)
	stream := WorkStream(workID)

	err := s.Exclusive(func(sess *Session) error {
		require.NoError(t, sess.Append(stream, 0, agent, allEvents()[:2]...))
		return sess.Append(stream, 1, agent, allEvents()[2])
	})

	require.ErrorIs(t, err, ErrVersionConflict)
	var conflict *VersionConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, 2, conflict.Actual)
	assert.Len(t, readAll(t, s), 2, "nothing written on conflict")
}

func TestRead_InterruptedFinalLine_IsIgnoredTruncatedAndLogged(t *testing.T) {
	var logs bytes.Buffer
	s, root := openStore(t, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	require.NoError(t, s.Exclusive(func(sess *Session) error {
		return sess.Append(WorkStream(workID), 0, agent, allEvents()[:2]...)
	}))
	complete, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)
	f, err := os.OpenFile(eventsPath(root), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"id":"half","posi`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// A reader ignores it without touching the file.
	assert.Len(t, readAll(t, s), 2)
	assert.Contains(t, logs.String(), "interrupted write")

	// A writer truncates it and appends after the last complete line.
	require.NoError(t, s.Exclusive(func(sess *Session) error {
		records, err := sess.ReadAll()
		require.Len(t, records, 2)
		require.NoError(t, err)
		after, err := os.ReadFile(eventsPath(root))
		require.NoError(t, err)
		assert.Equal(t, complete, after, "truncated")
		return sess.Append(WorkStream(workID), 2, agent, allEvents()[2])
	}))
	records := readAll(t, s)
	require.Len(t, records, 3)
	assert.Equal(t, 3, records[2].Position)
}

func TestRead_MalformedMiddleLine_IsAHardError(t *testing.T) {
	s, root := openStore(t)
	require.NoError(t, s.Exclusive(func(sess *Session) error {
		return sess.Append(WorkStream(workID), 0, agent, allEvents()[:3]...)
	}))
	raw, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)
	lines := bytes.SplitAfter(raw, []byte("\n"))
	lines[1] = []byte("not json\n")
	require.NoError(t, os.WriteFile(eventsPath(root), bytes.Join(lines, nil), 0o644))

	err = s.Shared(func(*Snapshot) error { return nil })
	require.ErrorIs(t, err, ErrMalformedLog)
	var malformed *MalformedLogError
	require.ErrorAs(t, err, &malformed)
	assert.Equal(t, 2, malformed.Line)

	err = s.Exclusive(func(*Session) error { return nil })
	require.ErrorIs(t, err, ErrMalformedLog)
}

func TestRead_OutOfSequencePosition_IsAHardError(t *testing.T) {
	s, root := openStore(t)
	require.NoError(t, s.Exclusive(func(sess *Session) error {
		return sess.Append(WorkStream(workID), 0, agent, allEvents()[:2]...)
	}))
	raw, err := os.ReadFile(eventsPath(root))
	require.NoError(t, err)
	lines := bytes.SplitAfter(raw, []byte("\n"))
	require.NoError(t, os.WriteFile(eventsPath(root), append(lines[1], lines[0]...), 0o644))

	var malformed *MalformedLogError
	require.ErrorAs(t, s.Shared(func(*Snapshot) error { return nil }), &malformed)
	assert.Equal(t, 1, malformed.Line)
}

// Each writer appends a pair of events to its own stream inside one lock.
// With the lock, every pair is contiguous and positions have no gaps.
func TestExclusive_ContendingWritersDoNotInterleave(t *testing.T) {
	root := t.TempDir()
	const writers, rounds = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, writers*rounds)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each writer has its own Store and so its own lock file descriptor,
			// like separate gf processes.
			s, err := Open(root)
			if err != nil {
				errs <- err
				return
			}
			id := must(domain.NewWorkID(fmt.Sprintf("w%d", w)))
			for range rounds {
				errs <- s.Exclusive(func(sess *Session) error {
					records, err := sess.ReadAll()
					if err != nil {
						return err
					}
					version := NewHistory(records).Version(WorkStream(id))
					time.Sleep(time.Millisecond) // widen the race window
					return sess.Append(WorkStream(id), version, agent,
						domain.WorkCompleted{WorkID: id}, domain.WorkCompleted{WorkID: id})
				})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	s, err := Open(root)
	require.NoError(t, err)
	records := readAll(t, s)
	require.Len(t, records, writers*rounds*2)
	for i := 0; i < len(records); i += 2 {
		assert.Equal(t, i+1, records[i].Position)
		assert.Equal(t, records[i].Stream, records[i+1].Stream, "pair at position %d interleaved", i+1)
		assert.Equal(t, records[i].Version+1, records[i+1].Version)
	}
}
