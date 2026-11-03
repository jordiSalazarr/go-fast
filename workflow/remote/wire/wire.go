// Package wire is the history server's protocol, version 1: the messages gf
// sync and the server exchange, and those the server answers its members
// with, as JSON over HTTP. Every request carries a bearer token
// ("Authorization: Bearer <token>"): gf sync the project secret, which
// only syncs; a member their own token, which reads and assesses every
// project of their organisation and never syncs.
//
// Syncing, with the project secret:
//
//	GET  /v1/projects/{project}/streams           → Streams
//	HEAD /v1/projects/{project}/blobs/{sha256}    → 200 held, 404 not held
//	PUT  /v1/projects/{project}/blobs/{sha256}    ← the file's bytes; 201 (400 when they don't hash to sha256)
//	POST /v1/projects/{project}/files             ← Files → FilesRecorded (409 Problem "missing-blob")
//	POST /v1/projects/{project}/events            ← Events → EventsAccepted (409 Problem "diverged", "tampered" or "gap")
//
// Inspecting and assessing, with a member's token:
//
//	GET  /v1/me                                   → Me
//	POST /v1/projects                             ← NewProject → 201 ProjectSecret (409 Problem "taken")
//	POST /v1/projects/{project}/secret            → ProjectSecret: a new secret, which replaces the old one
//	GET  /v1/projects/{project}/blobs/{sha256}    → the file's bytes
//	GET  /v1/projects/{project}/works             → Works
//	GET  /v1/projects/{project}/works/{id}        → Timeline
//	GET  /v1/projects/{project}/instructions      → InstructionNames; ?name= → Instructions
//	GET  /v1/projects/{project}/instructions/{a}/diff/{b} → VersionDiff
//	GET  /v1/projects/{project}/troubles          → Troubles
//	PUT  /v1/projects/{project}/works/{id}/assessment                      ← Assess → Assessment
//	PUT  /v1/projects/{project}/works/{id}/visits/{stage}/{visit}/assessment ← Assess → Assessment
//	GET  /v1/projects/{project}/assessments       → Assessments
//
// The works list takes ?status=, ?type=, ?branch= and ?q= (in the
// description, ignoring case); the troubles ?type= and ?stage=; the
// assessments ?verdict=, ?stage=, ?member= and ?work=. To a member,
// another organisation's project is "not-found".
//
// Assessments are written on the server and live only there: gf never
// fetches them, and they never move a work.
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
	// Error is the problem's kind: "unauthorized", "forbidden" (a member
	// syncing, or the project secret reading), "invalid", "not-found",
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
	ProblemTaken        = "taken"
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
// the instructions each stage visit started with, its files, its troubles
// and how members assessed it.
type Timeline struct {
	Work   Work       `json:"work"`
	Events []Event    `json:"events"`
	Visits []Visit    `json:"visits"`
	Files  []FileLink `json:"files"`
	// Troubles are the work's, in the order they occurred.
	Troubles []Trouble `json:"troubles"`
	// Assessments are every member's current ones of the work and its
	// stage visits, newest first.
	Assessments []Assessment `json:"assessments"`
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

// InstructionNames are the skills and agents the project's history names,
// by name.
type InstructionNames struct {
	Names []InstructionName `json:"names"`
}

type InstructionName struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"` // "agent" or "skill"
	Versions int       `json:"versions"`
	LastSeen time.Time `json:"lastSeen"`
}

// Instructions are one skill's or agent's versions, oldest first, and the
// stage visits each instructed.
type Instructions struct {
	Name     string               `json:"name"`
	Kind     string               `json:"kind"`
	Versions []InstructionVersion `json:"versions"`
}

type InstructionVersion struct {
	// Version is the SHA-256; empty for loads recorded unversioned.
	Version   string           `json:"version"`
	FirstSeen time.Time        `json:"firstSeen"`
	Files     []FileLink       `json:"files"`
	Uses      []InstructionUse `json:"uses"`
}

// InstructionUse is a stage visit a version instructed.
type InstructionUse struct {
	Work  string    `json:"work"`
	Stage string    `json:"stage"`
	Visit int       `json:"visit"`
	Load  string    `json:"load,omitempty"`
	At    time.Time `json:"at"`
}

// VersionDiff compares two kept versions file by file.
type VersionDiff struct {
	From  string     `json:"from"`
	To    string     `json:"to"`
	Files []FileDiff `json:"files"`
}

type FileDiff struct {
	// Path is relative to the version's directory, e.g. "SKILL.md".
	Path string `json:"path"`
	// Status is "added", "removed", "changed" or "same".
	Status string `json:"status"`
	// Unified is the unified diff of a changed text file; empty otherwise.
	Unified string `json:"unified,omitempty"`
}

// Me is the member a token is, and the projects of their organisation.
type Me struct {
	Member       string    `json:"member"`
	Organisation string    `json:"organisation"`
	Projects     []Project `json:"projects"`
}

// Project is a project of the member's organisation: who created it, and
// who last set its secret.
type Project struct {
	ID        string    `json:"id"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	SecretBy  string    `json:"secretBy"`
	SecretAt  time.Time `json:"secretAt"`
}

// NewProject is a project a member creates in their organisation. Its id
// is lowercase letters, digits and dashes, and the server's own: no two
// organisations have a project of the same id.
type NewProject struct {
	ID string `json:"id"`
}

// ProjectSecret is a project's secret, shown once: the server keeps only
// its hash. gf sync reads it from .gofast/sync.secret, or
// $GOFAST_PROJECT_SECRET.
type ProjectSecret struct {
	Project string `json:"project"`
	Secret  string `json:"secret"`
}

// Verdicts of an assessment.
const (
	VerdictGood      = "good"
	VerdictWentWrong = "went-wrong"
)

// Assess is a member's judgement: good, or went wrong with the reason.
type Assess struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// Assessment is a member's current judgement of a stage visit, or of the
// whole work when Stage is empty. A member has one per stage visit or
// work: assessing again replaces it.
type Assessment struct {
	Work       string    `json:"work"`
	Stage      string    `json:"stage,omitempty"`
	Visit      int       `json:"visit,omitempty"`
	Member     string    `json:"member"`
	Verdict    string    `json:"verdict"`
	Reason     string    `json:"reason,omitempty"`
	AssessedAt time.Time `json:"assessedAt"`
}

// Assessments are the current ones, newest first.
type Assessments struct {
	Assessments []Assessment `json:"assessments"`
}

// Troubles are the moments works went wrong on their own record, newest
// first.
type Troubles struct {
	Troubles []Trouble `json:"troubles"`
}

// Trouble is a failed attempt, a rejection, an escalation, a rework, a
// return or an abandon: the event that recorded it, and the stage visit
// it happened in (none for an abandon).
type Trouble struct {
	Work       string    `json:"work"`
	Stage      string    `json:"stage,omitempty"`
	Visit      int       `json:"visit,omitempty"`
	Type       string    `json:"type"`
	Event      string    `json:"event"`
	OccurredAt time.Time `json:"occurredAt"`
	// Reason is what the event says went wrong.
	Reason string `json:"reason"`
}
