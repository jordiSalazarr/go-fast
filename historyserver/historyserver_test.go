package historyserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/historyserver"
	"github.com/jordiSalazarr/go-fast/workflow/remote"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

const token = "t0ken"

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// serve starts a server with the project wallets, and returns a client of
// it with token.
func serve(t *testing.T, token string) (*remote.Client, *historyserver.Store, string) {
	t.Helper()
	store, err := historyserver.Open(filepath.Join(t.TempDir(), "history.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	srv := httptest.NewServer(historyserver.Handler(store, map[string]historyserver.Project{"wallets": {TokenSHA256: hash("t0ken")}}, nil))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return remote.NewClient(remote.NewConfig(u, "wallets"), token, "test", nil), store, srv.URL
}

// clock is the second each new line occurs at.
var clock int

// line is an event of the log, as gf writes it, occurring after the
// previous one.
func line(id, stream string, version int, eventType, payload string) string {
	clock++
	return fmt.Sprintf(`{"id":%q,"position":%d,"stream":%q,"version":%d,"type":%q,"schema":1,"occurredAt":"2026-10-18T09:%02d:%02dZ","actor":{"kind":"owner","name":"Jordi"},"payload":%s}`,
		id, clock, stream, version, eventType, clock/60, clock%60, payload)
}

var (
	started   = line("e1", "work-w-1", 1, "WorkStarted", `{"workId":"w-1","workType":"new-feature","description":"wallets","branch":"main"}`)
	entered   = line("e2", "work-w-1", 2, "StageEntered", `{"workId":"w-1","stage":"discovery","visit":1,"gate":"human","budget":3}`)
	completed = line("e3", "work-w-1", 3, "WorkCompleted", `{"workId":"w-1"}`)
)

func refusal(t *testing.T, err error) wire.Problem {
	t.Helper()
	var problem *remote.ProblemError
	require.ErrorAs(t, err, &problem)
	return problem.Problem
}

func TestServer_WithoutTheProjectsToken_RefusesEverything(t *testing.T) {
	client, _, _ := serve(t, "wrong")
	_, err := client.Streams(context.Background())
	assert.Equal(t, wire.ProblemUnauthorized, refusal(t, err).Error)
	_, err = client.HasBlob(context.Background(), hash("x"))
	assert.Error(t, err)

	other, _, base := serve(t, token)
	u, _ := url.Parse(base)
	_, err = remote.NewClient(remote.NewConfig(u, "ledger"), token, "", nil).Streams(context.Background())
	assert.Equal(t, wire.ProblemUnauthorized, refusal(t, err).Error, "an unknown project looks the same as a wrong token")
	_, err = other.Streams(context.Background())
	assert.NoError(t, err)
}

func TestServer_AppendsEvents_Idempotently_AndKeepsEachStreamsDigest(t *testing.T) {
	client, _, _ := serve(t, token)
	ctx := context.Background()

	out, err := client.SendEvents(ctx, wire.Events{Seal: "s1", Events: []string{started, entered}})
	require.NoError(t, err)
	assert.Equal(t, wire.EventsAccepted{Accepted: 2}, out)
	out, err = client.SendEvents(ctx, wire.Events{Seal: "s2", Events: []string{started, entered, completed}})
	require.NoError(t, err)
	assert.Equal(t, wire.EventsAccepted{Accepted: 1, Duplicates: 2}, out, "an interrupted sync's events again")

	heads, err := client.Streams(ctx)
	require.NoError(t, err)
	digest := wire.Digest(wire.Digest(wire.Digest("", []byte(started)), []byte(entered)), []byte(completed))
	assert.Equal(t, map[string]wire.StreamHead{"work-w-1": {Version: 3, Digest: digest}}, heads)
}

func TestServer_RefusesAWholeBatch_ForOneBadEvent(t *testing.T) {
	client, _, _ := serve(t, token)
	ctx := context.Background()
	_, err := client.SendEvents(ctx, wire.Events{Events: []string{started}})
	require.NoError(t, err)

	other := line("e9", "work-w-2", 1, "WorkStarted", `{"workId":"w-2","workType":"fix-bug","description":"x","branch":"b"}`)
	for name, c := range map[string]struct {
		line, kind string
	}{
		"tampered": {line("e1", "work-w-1", 1, "WorkStarted", `{"workId":"w-1","workType":"fix-bug","description":"edited","branch":"main"}`), wire.ProblemTampered},
		"diverged": {line("x1", "work-w-1", 1, "WorkStarted", `{"workId":"w-1","workType":"new-feature","description":"wallets","branch":"main"}`), wire.ProblemDiverged},
		"gap":      {completed, wire.ProblemGap},
		"invalid":  {`{"id":"e5"}`, wire.ProblemInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.SendEvents(ctx, wire.Events{Events: []string{other, c.line}})
			p := refusal(t, err)
			assert.Equal(t, c.kind, p.Error, p.Message)
			if c.kind != wire.ProblemInvalid {
				assert.Equal(t, "work-w-1", p.Stream)
			}
			heads, err := client.Streams(ctx)
			require.NoError(t, err)
			assert.NotContains(t, heads, "work-w-2", "nothing of a refused batch is kept")
		})
	}
}

func TestServer_KeepsBlobsByHash_AndFilesOnlyForHeldBlobs(t *testing.T) {
	client, _, base := serve(t, token)
	ctx := context.Background()
	content := "# Discovery\n"

	_, err := client.RecordFiles(ctx, []wire.File{{Path: ".gofast/works/w-1/discovery-v1.md", SHA256: hash(content), Work: "w-1"}})
	assert.Equal(t, wire.ProblemMissingBlob, refusal(t, err).Error)
	assert.Equal(t, wire.ProblemInvalid, refusal(t, client.PutBlob(ctx, hash(content), []byte("other bytes"))).Error)

	held, err := client.HasBlob(ctx, hash(content))
	require.NoError(t, err)
	assert.False(t, held)
	require.NoError(t, client.PutBlob(ctx, hash(content), []byte(content)))
	require.NoError(t, client.PutBlob(ctx, hash(content), []byte(content)), "again changes nothing")
	held, err = client.HasBlob(ctx, hash(content))
	require.NoError(t, err)
	assert.True(t, held)
	recorded, err := client.RecordFiles(ctx, []wire.File{{Path: ".gofast/works/w-1/discovery-v1.md", SHA256: hash(content), Work: "w-1"}})
	require.NoError(t, err)
	assert.Equal(t, 1, recorded.Recorded)

	req, _ := http.NewRequest(http.MethodGet, base+"/v1/projects/wallets/blobs/"+hash(content), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, content, string(body))
}

func TestServer_ServesTheWorksAndEachWorksTimeline(t *testing.T) {
	client, _, base := serve(t, token)
	ctx := context.Background()
	skill, agent := hash("skill manifest"), hash("agent manifest")
	events := []string{
		started, entered,
		line("o1", "observations-w-1", 1, "AgentStarted", `{"workId":"w-1","stage":"discovery","visit":1,"agent":"gofast:discovery","sha256":"`+agent+`"}`),
		line("o2", "observations-w-1", 2, "SkillLoaded", `{"workId":"w-1","stage":"discovery","visit":1,"skill":"discovery","load":"preloaded","sha256":"`+skill+`"}`),
		line("o3", "observations-w-1", 3, "SkillLoaded", `{"workId":"w-1","stage":"discovery","visit":1,"skill":"other:pdf"}`),
		line("a1", "assignment-w-1-discovery-1", 1, "AssignmentOpened", `{"assignmentId":"w-1-discovery-1","workId":"w-1","stage":"discovery","visit":1,"gate":"human","budget":3}`),
		line("e3", "work-w-1", 3, "WorkCompleted", `{"workId":"w-1"}`),
		line("e8", "work-w-10", 1, "WorkStarted", `{"workId":"w-10","workType":"fix-bug","description":"another work","branch":"b"}`),
	}
	_, err := client.SendEvents(ctx, wire.Events{Events: events})
	require.NoError(t, err)
	for path, content := range map[string]string{".gofast/works/w-1/discovery-v1.md": "# Discovery\n", ".gofast/instructions/" + skill + "/SKILL.md": "# Skill\n"} {
		require.NoError(t, client.PutBlob(ctx, hash(content), []byte(content)))
		work := ""
		if filepath.Dir(path) == ".gofast/works/w-1" {
			work = "w-1"
		}
		_, err := client.RecordFiles(ctx, []wire.File{{Path: path, SHA256: hash(content), Size: int64(len(content)), Work: work}})
		require.NoError(t, err)
	}

	var works wire.Works
	get(t, base+"/v1/projects/wallets/works", &works)
	require.Len(t, works.Works, 2)
	assert.Equal(t, wire.Work{ID: "w-1", Type: "new-feature", Description: "wallets", Branch: "main", StartedAt: works.Works[0].StartedAt, Status: "completed"}, works.Works[0])
	assert.Equal(t, "active", works.Works[1].Status)

	var timeline wire.Timeline
	get(t, base+"/v1/projects/wallets/works/w-1", &timeline)
	var types []string
	for _, e := range timeline.Events {
		types = append(types, e.Type)
	}
	assert.Equal(t, []string{"WorkStarted", "StageEntered", "AgentStarted", "SkillLoaded", "SkillLoaded", "AssignmentOpened", "WorkCompleted"}, types,
		"the work's own streams only: not w-10's")
	require.Len(t, timeline.Visits, 1)
	assert.Equal(t, []wire.Instruction{
		{Kind: "agent", Name: "gofast:discovery", Version: agent},
		{Kind: "skill", Name: "discovery", Load: "preloaded", Version: skill, Files: []wire.FileLink{
			{Path: ".gofast/instructions/" + skill + "/SKILL.md", SHA256: hash("# Skill\n"), Size: 8, URL: "/v1/projects/wallets/blobs/" + hash("# Skill\n")}}},
		{Kind: "skill", Name: "other:pdf", Load: "invoked"},
	}, timeline.Visits[0].Instructions)
	assert.Equal(t, []wire.FileLink{{Path: ".gofast/works/w-1/discovery-v1.md", SHA256: hash("# Discovery\n"), Size: 12, URL: "/v1/projects/wallets/blobs/" + hash("# Discovery\n")}}, timeline.Files)
}

func TestStore_EventsAreAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := historyserver.Open(path)
	require.NoError(t, err)
	_, err = store.AppendEvents(context.Background(), "wallets", wire.Events{Events: []string{started}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	assert.ErrorContains(t, historyserver.ExecForTest(path, `UPDATE events SET line = 'x'`), "events are append-only")
	assert.ErrorContains(t, historyserver.ExecForTest(path, `DELETE FROM events`), "events are append-only")
}

func TestLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"listen": ":8080", "db": "h.db", "projects": {"wallets": {"tokenSha256": "`+hash(token)+`"}}}`), 0o644))
	c, err := historyserver.LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, hash(token), c.Projects["wallets"].TokenSHA256)

	require.NoError(t, os.WriteFile(path, []byte(`{"listen": ":8080", "db": "h.db", "projects": {"wallets": {"tokenSha256": "t0ken"}}}`), 0o644))
	_, err = historyserver.LoadConfig(path)
	assert.ErrorContains(t, err, "tokenSha256 must be the token's SHA-256")
}

func get(t *testing.T, u string, v any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
}
