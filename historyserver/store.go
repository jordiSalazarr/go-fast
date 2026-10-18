// Package historyserver is the reference gofast history server: it keeps an
// append-only copy of each project's work history, as gf sync sends it
// (protocol: workflow/remote/wire), in SQLite. It reads the event log's
// envelopes, never gf's domain: an older server keeps accepting the events
// of a newer gf. It can't change any workflow state; nothing it holds goes
// back to a repository.
package historyserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Store is the server's database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	project     TEXT NOT NULL,
	id          TEXT NOT NULL,
	stream      TEXT NOT NULL,
	version     INTEGER NOT NULL,
	type        TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	line        TEXT NOT NULL,
	received_at TEXT NOT NULL,
	UNIQUE (project, id),
	UNIQUE (project, stream, version)
);
CREATE TABLE IF NOT EXISTS streams (
	project TEXT NOT NULL,
	stream  TEXT NOT NULL,
	version INTEGER NOT NULL,
	digest  TEXT NOT NULL,
	PRIMARY KEY (project, stream)
);
CREATE TABLE IF NOT EXISTS blobs (
	project TEXT NOT NULL,
	sha256  TEXT NOT NULL,
	size    INTEGER NOT NULL,
	content BLOB NOT NULL,
	PRIMARY KEY (project, sha256)
);
CREATE TABLE IF NOT EXISTS files (
	project    TEXT NOT NULL,
	path       TEXT NOT NULL,
	sha256     TEXT NOT NULL,
	size       INTEGER NOT NULL,
	work       TEXT NOT NULL,
	first_seen TEXT NOT NULL,
	PRIMARY KEY (project, path, sha256)
);
CREATE TABLE IF NOT EXISTS syncs (
	project     TEXT NOT NULL,
	received_at TEXT NOT NULL,
	seal        TEXT NOT NULL,
	client      TEXT NOT NULL,
	accepted    INTEGER NOT NULL,
	duplicates  INTEGER NOT NULL
);
CREATE TRIGGER IF NOT EXISTS events_append_only_update BEFORE UPDATE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS events_append_only_delete BEFORE DELETE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
`

// Open opens, and creates if needed, the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; SQLite serialises them anyway
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create the schema: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Backup writes a consistent copy of the database to path, which must not
// exist, while the server keeps serving.
func (s *Store) Backup(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

func (s *Store) stamp() string { return s.now().UTC().Format(time.RFC3339Nano) }

// Streams are the project's stream heads.
func (s *Store) Streams(ctx context.Context, project string) (map[string]wire.StreamHead, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT stream, version, digest FROM streams WHERE project = ?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]wire.StreamHead{}
	for rows.Next() {
		var stream string
		var head wire.StreamHead
		if err := rows.Scan(&stream, &head.Version, &head.Digest); err != nil {
			return nil, err
		}
		out[stream] = head
	}
	return out, rows.Err()
}

// problemError is a request refused with a protocol problem.
type problemError struct {
	status  int
	problem wire.Problem
}

func (e *problemError) Error() string { return e.problem.Error + ": " + e.problem.Message }

func refuse(status int, kind, format string, args ...any) *problemError {
	return &problemError{status: status, problem: wire.Problem{Error: kind, Message: fmt.Sprintf(format, args...)}}
}

// AppendEvents appends a batch, all or nothing: idempotent by event ID,
// one event per stream version, each stream's versions without gaps.
func (s *Store) AppendEvents(ctx context.Context, project string, batch wire.Events) (wire.EventsAccepted, error) {
	var out wire.EventsAccepted
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	heads := map[string]wire.StreamHead{}
	head := func(stream string) (wire.StreamHead, error) {
		if h, ok := heads[stream]; ok {
			return h, nil
		}
		var h wire.StreamHead
		err := tx.QueryRowContext(ctx, `SELECT version, digest FROM streams WHERE project = ? AND stream = ?`, project, stream).Scan(&h.Version, &h.Digest)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		heads[stream] = h
		return h, err
	}
	now := s.stamp()
	for i, line := range batch.Events {
		env, problem := envelopeOf(line)
		if problem != nil {
			problem.problem.Message = fmt.Sprintf("event %d of the batch: %s", i+1, problem.problem.Message)
			return wire.EventsAccepted{}, problem
		}
		var held string
		err := tx.QueryRowContext(ctx, `SELECT line FROM events WHERE project = ? AND id = ?`, project, env.ID).Scan(&held)
		switch {
		case err == nil && held == line:
			out.Duplicates++
			continue
		case err == nil:
			p := refuse(409, wire.ProblemTampered, "the server holds event %s with other content: the log was changed after it was synced", env.ID)
			p.problem.Stream, p.problem.Version, p.problem.ID = env.Stream, env.Version, env.ID
			return wire.EventsAccepted{}, p
		case !errors.Is(err, sql.ErrNoRows):
			return wire.EventsAccepted{}, err
		}
		h, err := head(env.Stream)
		if err != nil {
			return wire.EventsAccepted{}, err
		}
		if env.Version != h.Version+1 {
			kind, msg := wire.ProblemGap, fmt.Sprintf("the server holds this stream up to version %d, so version %d can't come next", h.Version, env.Version)
			if env.Version <= h.Version {
				kind, msg = wire.ProblemDiverged, fmt.Sprintf("the server holds another event at version %d of this stream: the same work was advanced in another clone", env.Version)
			}
			p := refuse(409, kind, "%s", msg)
			p.problem.Stream, p.problem.Version, p.problem.ID = env.Stream, env.Version, env.ID
			return wire.EventsAccepted{}, p
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events (project, id, stream, version, type, occurred_at, line, received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			project, env.ID, env.Stream, env.Version, env.Type, env.OccurredAt.UTC().Format(time.RFC3339Nano), line, now); err != nil {
			return wire.EventsAccepted{}, err
		}
		heads[env.Stream] = wire.StreamHead{Version: env.Version, Digest: wire.Digest(h.Digest, []byte(line))}
		out.Accepted++
	}
	for stream, h := range heads {
		if _, err := tx.ExecContext(ctx, `INSERT INTO streams (project, stream, version, digest) VALUES (?, ?, ?, ?)
			ON CONFLICT (project, stream) DO UPDATE SET version = excluded.version, digest = excluded.digest`, project, stream, h.Version, h.Digest); err != nil {
			return wire.EventsAccepted{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO syncs (project, received_at, seal, client, accepted, duplicates) VALUES (?, ?, ?, ?, ?, ?)`,
		project, now, batch.Seal, batch.Client, out.Accepted, out.Duplicates); err != nil {
		return wire.EventsAccepted{}, err
	}
	return out, tx.Commit()
}

// envelopeOf reads what the server needs of an event's line.
func envelopeOf(line string) (wire.Envelope, *problemError) {
	var env wire.Envelope
	if strings.ContainsAny(line, "\n\r") {
		return env, refuse(400, wire.ProblemInvalid, "a line of the event log holds no newline")
	}
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return env, refuse(400, wire.ProblemInvalid, "not an event of the log: %v", err)
	}
	switch {
	case env.ID == "" || env.Stream == "" || env.Type == "":
		return env, refuse(400, wire.ProblemInvalid, "an event needs an id, a stream and a type")
	case env.Version < 1 || env.Schema < 1:
		return env, refuse(400, wire.ProblemInvalid, "event %s: version and schema start at 1", env.ID)
	case env.OccurredAt.IsZero():
		return env, refuse(400, wire.ProblemInvalid, "event %s has no occurredAt", env.ID)
	case len(env.Payload) == 0 || env.Payload[0] != '{':
		return env, refuse(400, wire.ProblemInvalid, "event %s: its payload must be an object", env.ID)
	}
	return env, nil
}

// HasBlob reports whether the project holds the blob, and its size.
func (s *Store) HasBlob(ctx context.Context, project, sha string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM blobs WHERE project = ? AND sha256 = ?`, project, sha).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Blob is a blob's bytes; ok is false when the project doesn't hold it.
func (s *Store) Blob(ctx context.Context, project, sha string) (content []byte, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT content FROM blobs WHERE project = ? AND sha256 = ?`, project, sha).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return content, err == nil, err
}

// PutBlob keeps a blob, once, if content hashes to sha.
func (s *Store) PutBlob(ctx context.Context, project, sha string, content []byte) error {
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != sha {
		return refuse(400, wire.ProblemInvalid, "the bytes hash to %s, not %s", hex.EncodeToString(sum[:]), sha)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO blobs (project, sha256, size, content) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		project, sha, len(content), content)
	return err
}

// RecordFiles records where blobs were, all or nothing; every file's blob
// must be held.
func (s *Store) RecordFiles(ctx context.Context, project string, files []wire.File) (wire.FilesRecorded, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wire.FilesRecorded{}, err
	}
	defer tx.Rollback()
	now := s.stamp()
	recorded := 0
	for _, f := range files {
		if f.Path == "" || strings.HasPrefix(f.Path, "/") {
			return wire.FilesRecorded{}, refuse(400, wire.ProblemInvalid, "a file's path is relative to the repository's root, not %q", f.Path)
		}
		var size int64
		err := tx.QueryRowContext(ctx, `SELECT size FROM blobs WHERE project = ? AND sha256 = ?`, project, f.SHA256).Scan(&size)
		if errors.Is(err, sql.ErrNoRows) {
			p := refuse(409, wire.ProblemMissingBlob, "%s: upload blob %s first", f.Path, f.SHA256)
			p.problem.SHA256 = f.SHA256
			return wire.FilesRecorded{}, p
		}
		if err != nil {
			return wire.FilesRecorded{}, err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO files (project, path, sha256, size, work, first_seen) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			project, f.Path, f.SHA256, size, f.Work, now)
		if err != nil {
			return wire.FilesRecorded{}, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			recorded++
		}
	}
	return wire.FilesRecorded{Recorded: recorded}, tx.Commit()
}
