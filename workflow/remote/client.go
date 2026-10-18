package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Client speaks the history server's protocol for one project.
type Client struct {
	base      string // the project's URL: <server>/v1/projects/<project>
	server    string
	token     string
	userAgent string
	http      *http.Client
}

// NewClient is a client of the configured project. httpClient may be nil.
func NewClient(c Config, token, userAgent string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		base:   c.Server.String() + wire.Version + "/projects/" + url.PathEscape(c.Project),
		server: c.Server.String(), token: token, userAgent: userAgent, http: httpClient,
	}
}

// ProblemError is a request the server refused.
type ProblemError struct {
	Status  int
	Problem wire.Problem
}

func (e *ProblemError) Error() string {
	return fmt.Sprintf("history server refused the request (%d %s): %s", e.Status, e.Problem.Error, e.Problem.Message)
}

// UnreachableError: the server could not be reached, or answered with
// something that isn't the protocol.
type UnreachableError struct {
	Server string
	Err    error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("history server %s: %v", e.Server, e.Err)
}

func (e *UnreachableError) Unwrap() error { return e.Err }

// Streams is how far the server holds each stream.
func (c *Client) Streams(ctx context.Context) (map[string]wire.StreamHead, error) {
	var out wire.Streams
	if _, err := c.do(ctx, http.MethodGet, "/streams", nil, "", &out); err != nil {
		return nil, err
	}
	if out.Streams == nil {
		out.Streams = map[string]wire.StreamHead{}
	}
	return out.Streams, nil
}

// HasBlob reports whether the server holds the blob.
func (c *Client) HasBlob(ctx context.Context, sha256 string) (bool, error) {
	status, err := c.do(ctx, http.MethodHead, "/blobs/"+sha256, nil, "", nil)
	var problem *ProblemError
	if errors.As(err, &problem) && problem.Status == http.StatusNotFound {
		return false, nil
	}
	return err == nil && status == http.StatusOK, err
}

// PutBlob uploads a file's bytes.
func (c *Client) PutBlob(ctx context.Context, sha256 string, content []byte) error {
	_, err := c.do(ctx, http.MethodPut, "/blobs/"+sha256, content, "application/octet-stream", nil)
	return err
}

// RecordFiles tells the server where its blobs were in the repository.
func (c *Client) RecordFiles(ctx context.Context, files []wire.File) (wire.FilesRecorded, error) {
	var out wire.FilesRecorded
	body, err := json.Marshal(wire.Files{Files: files})
	if err != nil {
		return out, err
	}
	_, err = c.do(ctx, http.MethodPost, "/files", body, "application/json", &out)
	return out, err
}

// SendEvents sends a batch of the log's lines.
func (c *Client) SendEvents(ctx context.Context, batch wire.Events) (wire.EventsAccepted, error) {
	var out wire.EventsAccepted
	body, err := json.Marshal(batch)
	if err != nil {
		return out, err
	}
	_, err = c.do(ctx, http.MethodPost, "/events", body, "application/json", &out)
	return out, err
}

// maxResponse bounds what gf reads of a response.
const maxResponse = 16 << 20

func (c *Client) do(ctx context.Context, method, path string, body []byte, contentType string, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, &UnreachableError{Server: c.server, Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, &UnreachableError{Server: c.server, Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, &UnreachableError{Server: c.server, Err: err}
	}
	if resp.StatusCode >= 300 {
		problem := wire.Problem{Error: http.StatusText(resp.StatusCode)}
		if method != http.MethodHead && json.Unmarshal(data, &problem) != nil {
			return resp.StatusCode, &UnreachableError{Server: c.server, Err: fmt.Errorf("%s %s: %s", method, path, resp.Status)}
		}
		if method == http.MethodHead && resp.StatusCode == http.StatusNotFound {
			problem.Error = wire.ProblemNotFound
		}
		return resp.StatusCode, &ProblemError{Status: resp.StatusCode, Problem: problem}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, &UnreachableError{Server: c.server, Err: fmt.Errorf("%s %s: not the history protocol: %w", method, path, err)}
		}
	}
	return resp.StatusCode, nil
}
