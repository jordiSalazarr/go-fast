package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	gosync "sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/historyserver"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

const syncToken = "t0ken"

// historyServer is a running reference server with the project wallets,
// and the requests it served, in order.
type historyServer struct {
	url   string
	store *historyserver.Store
	mu    gosync.Mutex
	seen  []string
	// dropEvents is how many event batches to drop, as a lost connection.
	dropEvents int
}

func newHistoryServer(t *testing.T) *historyServer {
	t.Helper()
	store, err := historyserver.Open(filepath.Join(t.TempDir(), "history.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	sum := sha256Hex(syncToken)
	inner := historyserver.Handler(store, map[string]historyserver.Project{"wallets": {TokenSHA256: sum}}, nil)
	h := &historyServer{store: store}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.seen = append(h.seen, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/v1/projects/wallets"))
		drop := r.URL.Path == "/v1/projects/wallets/events" && h.dropEvents > 0
		if drop {
			h.dropEvents--
		}
		h.mu.Unlock()
		if drop {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	h.url = srv.URL
	return h
}

func (h *historyServer) requests() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

// heldEvents is how many events the server holds for the project.
func (h *historyServer) heldEvents(t *testing.T) int {
	t.Helper()
	heads, err := h.store.Streams(context.Background(), "wallets")
	require.NoError(t, err)
	n := 0
	for _, head := range heads {
		n += head.Version
	}
	return n
}

func sha256Hex(s string) string { return hashHex([]byte(s)) }

// syncingRepo is a repository whose committed sync.json points at server.
func syncingRepo(t *testing.T, server *historyServer) *repo {
	t.Helper()
	r := newRepo(t)
	r.write(".gofast/sync.json", `{"server": "`+server.url+`", "project": "wallets"}`)
	r.gf("status") // creates .gofast/ with .gitignore and .gitattributes
	r.git("add", ".")
	r.git("commit", "-q", "-m", "gofast")
	return r
}

// clone is another clone of r, as a teammate's or another machine's.
func (r *repo) clone() *repo {
	r.t.Helper()
	dir := r.t.TempDir()
	out, err := execGit("clone", "-q", r.dir, dir)
	require.NoError(r.t, err, out)
	c := &repo{t: r.t, dir: dir}
	c.git("config", "user.name", "Jordi")
	c.git("config", "user.email", "jordi@example.com")
	return c
}

func (r *repo) sync(args ...string) result {
	r.t.Helper()
	return r.run(map[string]string{"GOFAST_SYNC_TOKEN": syncToken}, append([]string{"sync"}, args...)...)
}

func (r *repo) events() int { return len(r.eventTypes()) }

func TestSync_TwoClonesAndAMerge_TheServerHoldsEveryEventOnce(t *testing.T) {
	server := newHistoryServer(t)
	origin := syncingRepo(t, server)
	a, b := origin.clone(), origin.clone()

	a.git("checkout", "-q", "-b", "a")
	a.gf("start", "--type", "fix-bug", "work in clone a")
	a.workOnStage()
	a.git("add", ".")
	a.git("commit", "-q", "-m", "work a")
	res := a.sync()
	require.Equal(t, 0, res.code, res.stderr)
	assert.Contains(t, res.stdout, "Synced with "+server.url+" (project wallets):\n")
	assert.Contains(t, res.stdout, "events   ")
	assert.Equal(t, a.events(), server.heldEvents(t))
	assert.Contains(t, a.gf("status"), "; nothing pending.\n")

	b.git("checkout", "-q", "-b", "b")
	b.gf("start", "--type", "fix-bug", "work in clone b")
	b.git("add", ".")
	b.git("commit", "-q", "-m", "work b")
	res = b.sync()
	require.Equal(t, 0, res.code, res.stderr)

	// b merges a: the merged log holds both works; nothing is new to the server.
	b.git("fetch", "-q", a.dir, "a:a")
	b.git("merge", "-q", "--no-edit", "a")
	res = b.sync()
	require.Equal(t, 0, res.code, res.stderr)
	assert.Contains(t, res.stdout, "  events   0 sent, 0 the server held already\n")
	assert.Equal(t, b.events(), server.heldEvents(t), "every event of the merged log, once")

	var works wire.Works
	getJSON(t, server.url+"/v1/projects/wallets/works", &works)
	require.Len(t, works.Works, 2)
	assert.ElementsMatch(t, []string{"work in clone a", "work in clone b"}, []string{works.Works[0].Description, works.Works[1].Description})
	var timeline wire.Timeline
	getJSON(t, server.url+"/v1/projects/wallets/works/"+works.Works[0].ID, &timeline)
	assert.NotEmpty(t, timeline.Files, "the work's artifact")
}

func TestSync_BlobsAndFilesGoBeforeEvents(t *testing.T) {
	server := newHistoryServer(t)
	r := syncingRepo(t, server)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.workOnStage()
	require.Equal(t, 0, r.sync().code)

	var lastFile, firstEvents = -1, -1
	for i, req := range server.requests() {
		switch {
		case strings.HasPrefix(req, "PUT /blobs/") || req == "POST /files":
			lastFile = i
		case req == "POST /events" && firstEvents < 0:
			firstEvents = i
		}
	}
	require.GreaterOrEqual(t, lastFile, 0, "the artifact was uploaded")
	assert.Less(t, lastFile, firstEvents, "every blob and file before the first event")
}

func TestSync_AnInterruptedSync_Resumes(t *testing.T) {
	server := newHistoryServer(t)
	r := syncingRepo(t, server)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	server.dropEvents = 1

	res := r.sync()
	assert.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "gf could not reach the history server "+server.url)
	assert.Contains(t, res.stderr, "Run `gf sync` again; it resumes where it stopped.")
	assert.Zero(t, server.heldEvents(t))
	assert.Contains(t, r.gf("status"), "Sync: never synced; ")

	res = r.sync()
	require.Equal(t, 0, res.code, res.stderr)
	assert.Equal(t, r.events(), server.heldEvents(t))
	res = r.sync()
	assert.Contains(t, res.stdout, "  events   0 sent")
}

func TestSync_ALogChangedAfterASync_IsRefused_AndTheRestSyncs(t *testing.T) {
	server := newHistoryServer(t)
	r := syncingRepo(t, server)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	require.Equal(t, 0, r.sync().code)

	log, err := os.ReadFile(r.eventsFile())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(r.eventsFile(), []byte(strings.Replace(string(log), "login button does nothing", "login button works", 1)), 0o644))

	res := r.sync()
	assert.Equal(t, 1, res.code)
	assert.Regexp(t, `Refused: stream work-w-[0-9a-f]+ at version \d+: the server holds other events up to this version`, res.stdout)
	assert.Contains(t, res.stderr, "1 streams were not synced; the rest were.")
	var works wire.Works
	getJSON(t, server.url+"/v1/projects/wallets/works", &works)
	require.Len(t, works.Works, 1)
	assert.Equal(t, "login button does nothing", works.Works[0].Description, "the server keeps what it was sent")
}

func TestSync_DryRun_SendsNothing(t *testing.T) {
	server := newHistoryServer(t)
	r := syncingRepo(t, server)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.workOnStage()

	res := r.sync("--dry-run")
	require.Equal(t, 0, res.code, res.stderr)
	assert.Contains(t, res.stdout, "Dry run: nothing was sent to "+server.url+" (project wallets). A sync would send:\n")
	assert.Zero(t, server.heldEvents(t))
	for _, req := range server.requests() {
		assert.False(t, strings.HasPrefix(req, "PUT") || strings.HasPrefix(req, "POST"), req)
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+syncToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, jsonDecode(resp, v))
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func execGit(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	return string(out), err
}

func jsonDecode(resp *http.Response, v any) error { return json.NewDecoder(resp.Body).Decode(v) }

// Change 0: within a log a stream's versions never repeat (gf refuses such
// a log), but two clones that advance the same work before merging each
// write their own event at the same version. The server holds the first
// and refuses the second clone's stream; the rest of its history syncs.
func TestSync_TheSameWorkAdvancedInTwoClones_IsRefusedForTheSecond(t *testing.T) {
	server := newHistoryServer(t)
	origin := syncingRepo(t, server)
	origin.gf("start", "--type", "fix-bug", "login button does nothing")
	origin.git("add", ".")
	origin.git("commit", "-q", "-m", "start")
	a, b := origin.clone(), origin.clone()

	for _, c := range []*repo{a, b} {
		c.workOnStage()
		c.submitAsAgent("--passed")
	}
	require.Equal(t, 0, a.sync().code)

	res := b.sync()
	assert.Equal(t, 1, res.code)
	assert.Regexp(t, `Refused: stream assignment-w-[0-9a-f]+-discovery-1 at version \d+: `, res.stdout)
	assert.Less(t, server.heldEvents(t), a.events()+b.events())
}
