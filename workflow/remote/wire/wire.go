// Package wire is the history server's protocol, version 1: the messages gf
// sync and the server exchange, as JSON over HTTP. Every request carries
// the project's token as "Authorization: Bearer <token>".
//
//	GET  /v1/projects/{project}/streams           → Streams
//	HEAD /v1/projects/{project}/blobs/{sha256}    → 200 held, 404 not held
//	PUT  /v1/projects/{project}/blobs/{sha256}    ← the file's bytes; 201 (400 when they don't hash to sha256)
//	GET  /v1/projects/{project}/blobs/{sha256}    → the file's bytes
//	POST /v1/projects/{project}/files             ← Files → FilesRecorded (409 Problem "missing-blob")
//	POST /v1/projects/{project}/events            ← Events → EventsAccepted (409 Problem "diverged", "tampered" or "gap")
//	GET  /v1/projects/{project}/works             → Works
//	GET  /v1/projects/{project}/works/{id}        → Timeline
//
// Blobs come before the files that name them, and files before events, so
// the server never holds an event whose files are missing. Every write is
// idempotent: sending what the server holds already changes nothing.
package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Version prefixes every path.
const Version = "/v1"

// Streams is, for each stream the server holds, its last version and the
// digest of its events up to it.
type Streams struct {
	Streams map[string]StreamHead `json:"streams"`
}

// StreamHead is how far the server holds a stream.
type StreamHead struct {
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

// Digest chains a stream's events: the digest after an event is the
// SHA-256 of the digest before it ("" before the first) and the event's
// line. Equal digests at a version mean equal events up to it, byte for
// byte.
func Digest(previous string, line []byte) string {
	h := sha256.New()
	h.Write([]byte(previous))
	sum := sha256.Sum256(line)
	h.Write(sum[:])
	return hex.EncodeToString(h.Sum(nil))
}

// Files names files the server holds as blobs: where they were in the
// repository, and the work they belong to, if any.
type Files struct {
	Files []File `json:"files"`
}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Work is the work the file belongs to: its artifacts, decision
	// documents and inputs. Empty for the kept instructions.
	Work string `json:"work,omitempty"`
}

type FilesRecorded struct {
	Recorded int `json:"recorded"`
}

// Events is a batch of the event log's lines, each exactly as written in
// .gofast/events.jsonl, in log order. Seal is the SHA-256 of the whole log
// when the batch was sent; Client is gf's build.
type Events struct {
	Seal   string   `json:"seal"`
	Client string   `json:"client,omitempty"`
	Events []string `json:"events"`
}

// EventsAccepted: Accepted were new; Duplicates the server held already.
type EventsAccepted struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
}

// Problem is a refused request. A refused batch of events or files changes
// nothing.
type Problem struct {
	// Error is the problem's kind: "unauthorized", "forbidden" (a read
	// token writing), "invalid",
	// "missing-blob", "diverged" (the server holds another event at this
	// stream version), "tampered" (it holds this event ID with other
	// content), "gap" (the stream's previous version is missing).
	Error   string `json:"error"`
	Message string `json:"message"`
	Stream  string `json:"stream,omitempty"`
	Version int    `json:"version,omitempty"`
	ID      string `json:"id,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

// Problem kinds.
const (
	ProblemUnauthorized = "unauthorized"
	ProblemForbidden    = "forbidden"
	ProblemInvalid      = "invalid"
	ProblemMissingBlob  = "missing-blob"
	ProblemDiverged     = "diverged"
	ProblemTampered     = "tampered"
	ProblemGap          = "gap"
	ProblemNotFound     = "not-found"
)

// Envelope is what the server reads of an event's line; the rest it keeps
// as written.
type Envelope struct {
	ID         string          `json:"id"`
	Position   int             `json:"position"`
	Stream     string          `json:"stream"`
	Version    int             `json:"version"`
	Type       string          `json:"type"`
	Schema     int             `json:"schema"`
	OccurredAt time.Time       `json:"occurredAt"`
	Actor      Actor           `json:"actor"`
	Payload    json.RawMessage `json:"payload"`
}

type Actor struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Works lists the works the server holds, oldest first.
type Works struct {
	Works []Work `json:"works"`
}

type Work struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Branch      string    `json:"branch"`
	StartedAt   time.Time `json:"startedAt"`
	// Status is "active", "completed" or "abandoned", as far as the server
	// knows.
	Status string `json:"status"`
}

// Timeline is one work's history: its events in the order they occurred,
// the instructions each stage visit started with, and its files.
type Timeline struct {
	Work   Work       `json:"work"`
	Events []Event    `json:"events"`
	Visits []Visit    `json:"visits"`
	Files  []FileLink `json:"files"`
}

type Event struct {
	ID         string          `json:"id"`
	Stream     string          `json:"stream"`
	Version    int             `json:"version"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Actor      Actor           `json:"actor"`
	Payload    json.RawMessage `json:"payload"`
}

// Visit is a stage visit's instructions: each agent start and skill load
// recorded, with its version.
type Visit struct {
	Stage        string        `json:"stage"`
	Visit        int           `json:"visit"`
	Instructions []Instruction `json:"instructions"`
}

type Instruction struct {
	Kind string `json:"kind"` // "agent" or "skill"
	Name string `json:"name"`
	Load string `json:"load,omitempty"`
	// Version is the instructions' SHA-256; empty when unversioned.
	Version string `json:"version,omitempty"`
	// Files are the version's kept files, when the server holds them.
	Files []FileLink `json:"files,omitempty"`
}

// FileLink is a file and where to read its bytes.
type FileLink struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	URL    string `json:"url"`
}
