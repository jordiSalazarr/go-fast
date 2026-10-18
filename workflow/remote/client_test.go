package remote_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/remote"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// fakeServer answers like the history server, recording each request.
func fakeServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, body []byte)) (*remote.Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		handle(w, r, body)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return remote.NewClient(remote.Config{Configured: true, Server: u, Project: "wallets"}, "t0ken", "gf test", nil), &seen
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestClient_SpeaksTheProtocol(t *testing.T) {
	var sent wire.Events
	client, seen := fakeServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/projects/wallets/streams":
			reply(w, 200, wire.Streams{Streams: map[string]wire.StreamHead{"work-w-1": {Version: 2, Digest: "d"}}})
		case "HEAD /v1/projects/wallets/blobs/aa":
			w.WriteHeader(200)
		case "HEAD /v1/projects/wallets/blobs/bb":
			w.WriteHeader(404)
		case "PUT /v1/projects/wallets/blobs/bb":
			assert.Equal(t, "bytes", string(body))
			w.WriteHeader(201)
		case "POST /v1/projects/wallets/files":
			reply(w, 200, wire.FilesRecorded{Recorded: 1})
		case "POST /v1/projects/wallets/events":
			require.NoError(t, json.Unmarshal(body, &sent))
			reply(w, 200, wire.EventsAccepted{Accepted: 1})
		}
	})
	ctx := context.Background()

	streams, err := client.Streams(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]wire.StreamHead{"work-w-1": {Version: 2, Digest: "d"}}, streams)
	held, err := client.HasBlob(ctx, "aa")
	require.NoError(t, err)
	assert.True(t, held)
	held, err = client.HasBlob(ctx, "bb")
	require.NoError(t, err)
	assert.False(t, held)
	require.NoError(t, client.PutBlob(ctx, "bb", []byte("bytes")))
	recorded, err := client.RecordFiles(ctx, []wire.File{{Path: "a.md", SHA256: "bb", Size: 5}})
	require.NoError(t, err)
	assert.Equal(t, 1, recorded.Recorded)
	line := `{"id":"e1","stream":"work-w-1"}`
	accepted, err := client.SendEvents(ctx, wire.Events{Seal: "s", Events: []string{line}})
	require.NoError(t, err)
	assert.Equal(t, 1, accepted.Accepted)
	assert.Equal(t, []string{line}, sent.Events, "lines travel exactly as written")

	for _, s := range *seen {
		assert.Contains(t, s, " Bearer t0ken", "every request carries the token")
	}
}

func TestClient_ReportsARefusal(t *testing.T) {
	client, _ := fakeServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		reply(w, 409, wire.Problem{Error: wire.ProblemDiverged, Message: "another event", Stream: "work-w-1", Version: 3})
	})
	_, err := client.SendEvents(context.Background(), wire.Events{})
	var problem *remote.ProblemError
	require.ErrorAs(t, err, &problem)
	assert.Equal(t, 409, problem.Status)
	assert.Equal(t, wire.Problem{Error: "diverged", Message: "another event", Stream: "work-w-1", Version: 3}, problem.Problem)
}

func TestClient_SomethingElseThanTheServer_IsUnreachable(t *testing.T) {
	client, _ := fakeServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>Bad gateway</html>"))
	})
	_, err := client.Streams(context.Background())
	var unreachable *remote.UnreachableError
	require.ErrorAs(t, err, &unreachable)

	u, _ := url.Parse("http://127.0.0.1:1")
	_, err = remote.NewClient(remote.Config{Configured: true, Server: u, Project: "p"}, "t", "", nil).Streams(context.Background())
	require.ErrorAs(t, err, &unreachable)
}

func TestDigest_ChainsEveryByte(t *testing.T) {
	a := wire.Digest(wire.Digest("", []byte("one")), []byte("two"))
	assert.Equal(t, a, wire.Digest(wire.Digest("", []byte("one")), []byte("two")))
	assert.NotEqual(t, a, wire.Digest(wire.Digest("", []byte("one ")), []byte("two")))
	assert.NotEqual(t, a, wire.Digest(wire.Digest("", []byte("two")), []byte("one")))
}
