package remote_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/remote"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// memServer keeps the server's rules in memory: idempotent by event ID,
// one event per stream version, versions without gaps, files only for
// blobs it holds, and every batch all or nothing.
type memServer struct {
	calls   []string
	events  map[string]string // id → line
	heads   map[string]wire.StreamHead
	at      map[string]string // stream@version → id
	blobs   map[string][]byte
	files   map[string]string
	refuse  string // a stream whose events it refuses as diverged
	failing int    // event batches to fail as unreachable, after the first
	batches int
}

func newMemServer() *memServer {
	return &memServer{events: map[string]string{}, heads: map[string]wire.StreamHead{}, at: map[string]string{}, blobs: map[string][]byte{}, files: map[string]string{}}
}

func (m *memServer) Streams(context.Context) (map[string]wire.StreamHead, error) {
	m.calls = append(m.calls, "streams")
	out := map[string]wire.StreamHead{}
	for k, v := range m.heads {
		out[k] = v
	}
	return out, nil
}

func (m *memServer) HasBlob(_ context.Context, sha string) (bool, error) {
	m.calls = append(m.calls, "has-blob")
	_, ok := m.blobs[sha]
	return ok, nil
}

func (m *memServer) PutBlob(_ context.Context, sha string, content []byte) error {
	m.calls = append(m.calls, "put-blob")
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != sha {
		return errors.New("hash mismatch")
	}
	m.blobs[sha] = content
	return nil
}

func (m *memServer) RecordFiles(_ context.Context, files []wire.File) (wire.FilesRecorded, error) {
	m.calls = append(m.calls, "files")
	for _, f := range files {
		if _, ok := m.blobs[f.SHA256]; !ok {
			return wire.FilesRecorded{}, &remote.ProblemError{Status: 409, Problem: wire.Problem{Error: wire.ProblemMissingBlob, SHA256: f.SHA256}}
		}
	}
	for _, f := range files {
		m.files[f.Path] = f.SHA256
	}
	return wire.FilesRecorded{Recorded: len(files)}, nil
}

func (m *memServer) SendEvents(_ context.Context, batch wire.Events) (wire.EventsAccepted, error) {
	m.calls = append(m.calls, "events")
	m.batches++
	if m.batches > 1 && m.failing > 0 {
		m.failing--
		return wire.EventsAccepted{}, &remote.UnreachableError{Server: "mem", Err: errors.New("connection reset")}
	}
	heads := map[string]wire.StreamHead{}
	for k, v := range m.heads {
		heads[k] = v
	}
	var out wire.EventsAccepted
	type add struct{ id, key, line string }
	var adds []add
	for _, line := range batch.Events {
		var env wire.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			return out, &remote.ProblemError{Status: 400, Problem: wire.Problem{Error: wire.ProblemInvalid}}
		}
		if env.Stream == m.refuse {
			return out, &remote.ProblemError{Status: 409, Problem: wire.Problem{Error: wire.ProblemDiverged, Message: "the server holds another event", Stream: env.Stream, Version: env.Version}}
		}
		if held, ok := m.events[env.ID]; ok && held == line {
			out.Duplicates++
			continue
		}
		head := heads[env.Stream]
		if env.Version != head.Version+1 {
			return out, &remote.ProblemError{Status: 409, Problem: wire.Problem{Error: wire.ProblemGap, Stream: env.Stream, Version: env.Version}}
		}
		heads[env.Stream] = wire.StreamHead{Version: env.Version, Digest: wire.Digest(head.Digest, []byte(line))}
		adds = append(adds, add{env.ID, env.Stream, line})
		out.Accepted++
	}
	m.heads = heads
	for _, a := range adds {
		m.events[a.id] = a.line
	}
	return out, nil
}

// history is a repository with two works, one started from a spec, an
// artifact each, a decision document, and a kept skill version.
func history(t *testing.T) (root string, log remote.Log) {
	t.Helper()
	root = t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	write("docs/wallets.md", "# Wallets\n")
	w1, w2 := Must(domain.NewWorkID("w-1")), Must(domain.NewWorkID("w-2"))
	spec := Must(domain.NewWorkInput(domain.InputSpec, Must(domain.ParseRepoPath("docs/wallets.md")), domain.HashOf([]byte("# Wallets\n"))))
	write(".gofast/works/w-1/discovery-v1.md", "# Discovery\n")
	write(".gofast/works/w-2/grill-v1.md", "# Grill\n")
	write("docs/decisions/2026-10-18-w-2-wallets.md", "# Decision\n")
	write("docs/decisions/2026-10-18-w-9-other.md", "# Not a work of this log\n")
	version := domain.HashOf([]byte("skill manifest"))
	write(".gofast/instructions/"+version.String()+"/SKILL.md", "# Discovery skill\n")

	store, err := eventlog.Open(root)
	require.NoError(t, err)
	actor := eventlog.OwnerActor(Owner)
	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		started := func(w domain.WorkID, inputs ...domain.WorkInput) domain.WorkStarted {
			return domain.WorkStarted{WorkID: w, WorkType: domain.WorkTypeNewFeature, PathVersion: domain.PathV3, Description: Description, Branch: Branch, Inputs: inputs}
		}
		if err := s.Append(eventlog.WorkStream(w1), 0, actor, started(w1)); err != nil {
			return err
		}
		if err := s.Append(eventlog.WorkStream(w2), 0, actor, started(w2, spec)); err != nil {
			return err
		}
		return s.Append(eventlog.ObservationsStream(w1), 0, actor,
			domain.SkillLoaded{WorkID: w1, Stage: domain.StageDiscovery, Visit: Visit1, Skill: "discovery", Load: domain.SkillPreloaded, Version: version},
			domain.SkillLoaded{WorkID: w1, Stage: domain.StageDiscovery, Visit: Visit1, Skill: "other:pdf", Load: domain.SkillInvoked})
	}))
	log, err = remote.ReadLog(store)
	require.NoError(t, err)
	return root, log
}

func config(include ...string) remote.Config {
	u, _ := url.Parse("https://history.example.com")
	return remote.NewConfig(u, "wallets", include...)
}

var all = []string{remote.IncludeEvents, remote.IncludeInstructions, remote.IncludeArtifacts, remote.IncludeDecisions}

func sync(t *testing.T, root string, c remote.Config, log remote.Log, server remote.Server, opts remote.Options) (remote.Report, error) {
	t.Helper()
	return remote.Sync(context.Background(), root, c, domain.DefaultDecisionsDir(), log, server, opts)
}

func TestSync_SendsBlobsThenFilesThenEvents_AndThenNothing(t *testing.T) {
	root, log := history(t)
	server := newMemServer()

	report, err := sync(t, root, config(all...), log, server, remote.Options{})
	require.NoError(t, err)

	assert.Equal(t, remote.Report{Events: 4, Files: 5, Uploaded: 5}, report)
	assert.Equal(t, []string{"streams", "has-blob", "put-blob", "has-blob", "put-blob", "has-blob", "put-blob", "has-blob", "put-blob", "has-blob", "put-blob", "files", "events"}, server.calls)
	assert.Equal(t, []string{".gofast/instructions/" + domain.HashOf([]byte("skill manifest")).String() + "/SKILL.md",
		".gofast/works/w-1/discovery-v1.md", ".gofast/works/w-2/grill-v1.md", "docs/decisions/2026-10-18-w-2-wallets.md", "docs/wallets.md"}, keys(server.files),
		"the other work's decision document isn't this log's")
	for i, line := range log.Lines {
		assert.Equal(t, string(line), server.events[log.Records[i].ID], "every event exactly as written")
	}

	server.calls = nil
	report, err = sync(t, root, config(all...), log, server, remote.Options{})
	require.NoError(t, err)
	assert.Equal(t, remote.Report{}, report)
	assert.Equal(t, []string{"streams"}, server.calls, "the cache knows the server holds every file")
}

func TestSync_SendsOnlyWhatTheConfigurationIncludes(t *testing.T) {
	root, log := history(t)
	server := newMemServer()
	report, err := sync(t, root, config(remote.IncludeEvents), log, server, remote.Options{})
	require.NoError(t, err)
	assert.Equal(t, 4, report.Events)
	assert.Empty(t, server.files)
	assert.Empty(t, server.blobs)

	server = newMemServer()
	_, err = sync(t, root, config(remote.IncludeEvents, remote.IncludeInstructions), log, server, remote.Options{})
	require.NoError(t, err)
	assert.Len(t, server.files, 1)
}

func TestSync_DryRun_SendsNothing(t *testing.T) {
	root, log := history(t)
	server := newMemServer()
	report, err := sync(t, root, config(all...), log, server, remote.Options{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, remote.Report{DryRun: true, Events: 4, Files: 5, Uploaded: 5}, report)
	assert.Empty(t, server.events)
	assert.Empty(t, server.blobs)
	assert.NotContains(t, server.calls, "put-blob")
	_, err = os.Stat(filepath.Join(root, ".gofast", "runtime", "sync.json"))
	assert.True(t, os.IsNotExist(err), "a dry run remembers nothing")
}

func TestSync_AnInterruptedSyncResumes_AndTheServerHoldsEachEventOnce(t *testing.T) {
	root, log := history(t)
	server := newMemServer()
	server.failing = 1

	_, err := sync(t, root, config(all...), log, server, remote.Options{BatchSize: 2})
	var unreachable *remote.UnreachableError
	require.ErrorAs(t, err, &unreachable)
	assert.Len(t, server.events, 2, "the first batch arrived")

	state, err := remote.StateOf(root, log.Records)
	require.NoError(t, err)
	assert.False(t, state.Configured, "no sync.json in this repository")

	report, err := sync(t, root, config(all...), log, server, remote.Options{BatchSize: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, report.Events)
	assert.Len(t, server.events, 4)
}

func TestSync_AStreamTheServerRefuses_IsLeftOut_AndTheRestSyncs(t *testing.T) {
	root, log := history(t)
	server := newMemServer()
	server.refuse = "work-w-2"

	report, err := sync(t, root, config(all...), log, server, remote.Options{})
	var refused *remote.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, []remote.Refusal{{Stream: "work-w-2", Version: 1, Reason: "the server holds another event"}}, report.Refused)
	assert.Equal(t, 3, report.Events)
	assert.Equal(t, 1, server.heads["work-w-1"].Version)
	assert.Zero(t, server.heads["work-w-2"].Version)
}

func TestSync_ALocalHistoryRewrittenAfterASync_IsRefused(t *testing.T) {
	root, log := history(t)
	server := newMemServer()
	_, err := sync(t, root, config(all...), log, server, remote.Options{})
	require.NoError(t, err)

	rewritten := log
	rewritten.Lines = append([][]byte(nil), log.Lines...)
	rewritten.Lines[0] = []byte(string(log.Lines[0]) + " ")
	report, err := sync(t, root, config(all...), rewritten, server, remote.Options{})
	var refused *remote.RefusedError
	require.ErrorAs(t, err, &refused)
	require.Len(t, report.Refused, 1)
	assert.Equal(t, "work-w-1", report.Refused[0].Stream)
	assert.Contains(t, report.Refused[0].Reason, "the server holds other events up to this version")
}

func TestSync_WhenTheServerLostABlobTheCacheTrusts_ItUploadsItAgain(t *testing.T) {
	root, log := history(t)
	server := newMemServer()
	_, err := sync(t, root, config(all...), log, server, remote.Options{})
	require.NoError(t, err)

	lost := newMemServer()
	lost.heads, lost.events = server.heads, server.events
	// A new file with the content of one the cache says the server holds.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gofast", "works", "w-1", "discovery-v2.md"), []byte("# Discovery\n"), 0o644))
	report, err := sync(t, root, config(all...), log, lost, remote.Options{})
	require.NoError(t, err)
	assert.Equal(t, remote.Report{Files: 6, Uploaded: 5}, report, "every file named again, every blob uploaded once")
	assert.Len(t, lost.files, 6)
}

func TestSync_AnInputChangedSinceTheWorkStarted_IsNotSent(t *testing.T) {
	root, log := history(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", "wallets.md"), []byte("# Wallets, edited\n"), 0o644))
	report, err := sync(t, root, config(all...), log, newMemServer(), remote.Options{})
	require.NoError(t, err)
	assert.Equal(t, []remote.Skip{{Path: "docs/wallets.md", Reason: "the work's input changed since the work started"}}, report.Skipped)
}

func TestStateOf_CountsWhatTheServerDidntHoldAtTheLastSync(t *testing.T) {
	root, log := history(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gofast", "sync.json"), []byte(`{"server": "https://history.example.com", "project": "wallets"}`), 0o644))
	state, err := remote.StateOf(root, log.Records)
	require.NoError(t, err)
	assert.Equal(t, remote.State{Configured: true, Pending: 4}, state)

	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	_, err = sync(t, root, config(all...), log, newMemServer(), remote.Options{Now: func() time.Time { return at }})
	require.NoError(t, err)
	state, err = remote.StateOf(root, log.Records)
	require.NoError(t, err)
	assert.Equal(t, remote.State{Configured: true, Synced: true, SyncedAt: at}, state)
}

func keys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }
