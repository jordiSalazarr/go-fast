// Package eventlog stores workflow events as JSON lines in .gofast/events.jsonl
// at the repository root, guarded by a file lock.
package eventlog

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

const (
	dirName    = ".gofast"
	eventsFile = "events.jsonl"
	lockFile   = "events.lock"
	logFile    = "gf.log"

	// RuntimeDir holds local session bookkeeping under .gofast/; never committed.
	RuntimeDir = "runtime"
)

var (
	ErrNoRepository    = errors.New("not inside a git repository")
	ErrVersionConflict = errors.New("stream version conflict")
	ErrMalformedLog    = errors.New("malformed event log")
)

// VersionConflictError reports an append whose expected stream version is stale.
type VersionConflictError struct {
	Stream   Stream
	Expected int
	Actual   int
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("%s: stream %s is at version %d, expected %d", ErrVersionConflict, e.Stream, e.Actual, e.Expected)
}

func (e *VersionConflictError) Unwrap() error { return ErrVersionConflict }

// ErrDivergedStream: a stream's versions repeat or skip, which usually means
// the same work was advanced on two branches that were then merged.
var ErrDivergedStream = errors.New("stream versions diverge")

// DivergedStreamError reports the line where a stream's version is not the
// next one.
type DivergedStreamError struct {
	Line    int
	Stream  Stream
	Version int
	Want    int
}

func (e *DivergedStreamError) Error() string {
	return fmt.Sprintf("%s at line %d: %s: stream %s has version %d, want %d",
		ErrMalformedLog, e.Line, ErrDivergedStream, e.Stream, e.Version, e.Want)
}

func (e *DivergedStreamError) Unwrap() []error { return []error{ErrMalformedLog, ErrDivergedStream} }

// MalformedLogError reports a line of events.jsonl that cannot be read.
type MalformedLogError struct {
	Line int
	Err  error
}

func (e *MalformedLogError) Error() string {
	return fmt.Sprintf("%s at line %d: %v", ErrMalformedLog, e.Line, e.Err)
}

func (e *MalformedLogError) Unwrap() []error { return []error{ErrMalformedLog, e.Err} }

// FindRoot walks up from start to the directory containing .git.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("find repository root from %s: %w", start, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("find repository root from %s: %w", start, ErrNoRepository)
		}
		dir = parent
	}
}

// Init creates .gofast/ under root if needed, and makes sure of two files in
// it: .gitignore lists what stays local (the lock, the log, runtime markers),
// and .gitattributes merges the event log with git's union driver, so work
// appended on two branches merges without conflicts.
func Init(root string) error {
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := ensureLines(filepath.Join(dir, ".gitignore"), ignored); err != nil {
		return err
	}
	return ensureLines(filepath.Join(dir, ".gitattributes"), attributes)
}

// ignored lists what .gofast/.gitignore keeps out of git.
var ignored = []string{lockFile, logFile, RuntimeDir + "/"}

// attributes are .gofast/.gitattributes: union merges keep both branches' lines.
var attributes = []string{eventsFile + " merge=union"}

// ensureLines appends to the file whichever lines it doesn't contain yet.
func ensureLines(path string, lines []string) error {
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	present := map[string]bool{}
	for _, line := range strings.Split(string(current), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	var missing string
	for _, line := range lines {
		if !present[line] {
			missing += line + "\n"
		}
	}
	if missing == "" {
		return nil
	}
	if len(current) > 0 && !bytes.HasSuffix(current, []byte("\n")) {
		missing = "\n" + missing
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("update %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(missing); err != nil {
		return fmt.Errorf("update %s: %w", path, err)
	}
	return nil
}

// LogPath is the event log's path relative to the repository root.
func LogPath() string { return dirName + "/" + eventsFile }

// CommittedFiles are the files gf writes that git tracks, relative to the
// repository root, slash-separated.
func CommittedFiles() []string {
	return []string{LogPath(), dirName + "/.gitignore", dirName + "/.gitattributes"}
}

// Dir is gofast's directory under root.
func Dir(root string) string { return filepath.Join(root, dirName) }

// LogFile is where gf writes its diagnostic log.
func LogFile(root string) string { return filepath.Join(root, dirName, logFile) }

// Store is the event log of one repository.
type Store struct {
	dir     string
	now     func() time.Time
	newID   func() string
	logger  *slog.Logger
	commits Commits // nil: the log is not sealed
}

type Option func(*Store)

func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }
func WithIDs(newID func() string) Option    { return func(s *Store) { s.newID = newID } }
func WithLogger(l *slog.Logger) Option      { return func(s *Store) { s.logger = l } }

// Open opens the event log under root, creating .gofast/ if needed.
func Open(root string, opts ...Option) (*Store, error) {
	if err := Init(root); err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	s := &Store{
		dir:    filepath.Join(root, dirName),
		now:    time.Now,
		newID:  randomID,
		logger: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

const (
	lockExclusive = syscall.LOCK_EX
	lockShared    = syscall.LOCK_SH
)

// Exclusive runs fn holding the exclusive lock, for a whole
// read → decide → append cycle. An interrupted final line is truncated. A
// sealed log changed outside gf is refused with a LogChangedError.
func (s *Store) Exclusive(fn func(*Session) error) error {
	unlock, err := s.lock(lockExclusive)
	if err != nil {
		return err
	}
	defer unlock()

	path := filepath.Join(s.dir, eventsFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	data, err := s.readComplete(f, true)
	if err != nil {
		return err
	}
	if err := s.checkSeal(data); err != nil {
		return err
	}
	records, err := parse(data)
	if err != nil {
		return err
	}
	return fn(newSession(s, f, records, data))
}

// Shared runs fn holding the shared lock, for reading only. An interrupted
// final line is ignored; the next writer truncates it. A sealed log changed
// outside gf is still read; the snapshot says so.
func (s *Store) Shared(fn func(*Snapshot) error) error {
	unlock, err := s.lock(lockShared)
	if err != nil {
		return err
	}
	defer unlock()

	path := filepath.Join(s.dir, eventsFile)
	var data []byte
	f, err := os.Open(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("open %s: %w", path, err)
	default:
		defer f.Close()
		if data, err = s.readComplete(f, false); err != nil {
			return err
		}
	}
	var changed *LogChangedError
	if err := s.checkSeal(data); err != nil && !errors.As(err, &changed) {
		s.logger.Error("could not check the event log seal while reading", "error", err.Error())
	}
	records, err := parse(data)
	if err != nil {
		return err
	}
	return fn(&Snapshot{records: records, changedOutsideGf: changed != nil})
}

func (s *Store) lock(how int) (unlock func(), err error) {
	path := filepath.Join(s.dir, lockFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// readComplete reads every complete line. A final line without a newline is
// an interrupted write: it is ignored, and truncated when truncate is set.
func (s *Store) readComplete(f *os.File, truncate bool) ([]byte, error) {
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name(), err)
	}
	complete := data
	if i := bytes.LastIndexByte(data, '\n'); i < len(data)-1 {
		complete = data[:i+1]
		partial := data[i+1:]
		s.logger.Warn("ignoring interrupted write at end of event log",
			"file", f.Name(), "bytes", len(partial), "truncated", truncate)
		if truncate {
			if err := f.Truncate(int64(len(complete))); err != nil {
				return nil, fmt.Errorf("truncate interrupted write in %s: %w", f.Name(), err)
			}
			if err := f.Sync(); err != nil {
				return nil, fmt.Errorf("sync %s: %w", f.Name(), err)
			}
		}
	}
	return complete, nil
}

// parse reads lines in file order. After a union merge, lines of two
// branches interleave and positions repeat or go backwards, so positions are
// not checked; each stream's versions must still run 1, 2, 3, ...
func parse(data []byte) ([]Recorded, error) {
	var records []Recorded
	versions := map[Stream]int{}
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines[:len(lines)-1] {
		r, err := decodeLine(line)
		if err != nil {
			return nil, &MalformedLogError{Line: i + 1, Err: err}
		}
		if want := versions[r.Stream] + 1; r.Version != want {
			return nil, &DivergedStreamError{Line: i + 1, Stream: r.Stream, Version: r.Version, Want: want}
		}
		versions[r.Stream] = r.Version
		records = append(records, r)
	}
	return records, nil
}

// Session reads and appends while holding the exclusive lock.
type Session struct {
	store       *Store
	file        *os.File
	maxPosition int
	records     []Recorded
	versions    map[Stream]int
	sum         hash.Hash // SHA-256 of the file's content, for the seal
	size        int64
	head        *string // git HEAD, read once per session when sealing
}

func newSession(s *Store, f *os.File, records []Recorded, data []byte) *Session {
	session := &Session{store: s, file: f, records: records, versions: map[Stream]int{}, sum: sha256.New(), size: int64(len(data))}
	session.sum.Write(data)
	for _, r := range records {
		session.versions[r.Stream] = r.Version
		session.maxPosition = max(session.maxPosition, r.Position)
	}
	return session
}

// ReadAll returns every event in log order.
func (s *Session) ReadAll() ([]Recorded, error) {
	return append([]Recorded(nil), s.records...), nil
}

// Append writes events to stream if the stream is at expectedVersion. New
// positions continue from the highest position in the file. All lines are
// written in one write and synced to disk before returning.
func (s *Session) Append(stream Stream, expectedVersion int, actor Actor, events ...domain.Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := actor.validate(); err != nil {
		return fmt.Errorf("append to %s: %w", stream, err)
	}
	if actual := s.versions[stream]; actual != expectedVersion {
		return fmt.Errorf("append to %s: %w", stream, &VersionConflictError{Stream: stream, Expected: expectedVersion, Actual: actual})
	}
	var buf bytes.Buffer
	added := make([]Recorded, 0, len(events))
	for i, e := range events {
		r := Recorded{
			ID:         s.store.newID(),
			Position:   s.maxPosition + i + 1,
			Stream:     stream,
			Version:    expectedVersion + i + 1,
			Schema:     schemaV1,
			OccurredAt: s.store.now().UTC(),
			Actor:      actor,
			Event:      e,
		}
		line, err := encodeLine(r)
		if err != nil {
			return fmt.Errorf("append to %s: %w", stream, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
		added = append(added, r)
	}
	if _, err := s.file.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("append to %s: write %s: %w", stream, s.file.Name(), err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("append to %s: sync %s: %w", stream, s.file.Name(), err)
	}
	s.records = append(s.records, added...)
	s.maxPosition += len(events)
	s.versions[stream] = expectedVersion + len(events)
	s.reseal(buf.Bytes())
	return nil
}

// reseal records the seal of the log after an append. A seal that cannot be
// written is logged, not returned: the events are on disk, and the next
// command reports the log as changed, which the owner can accept.
func (s *Session) reseal(appended []byte) {
	s.sum.Write(appended)
	s.size += int64(len(appended))
	if s.store.commits == nil {
		return
	}
	if s.head == nil {
		head := s.store.head()
		s.head = &head
	}
	if err := s.store.writeSeal(sealOf(s.sum, s.size, *s.head)); err != nil {
		s.store.logger.Error("could not seal the event log after an append", "error", err.Error())
	}
}

// Snapshot is what a reader sees while holding the shared lock.
type Snapshot struct {
	records          []Recorded
	changedOutsideGf bool
}

// ChangedOutsideGf reports that the sealed log was changed outside gf since
// gf last wrote it.
func (s *Snapshot) ChangedOutsideGf() bool { return s.changedOutsideGf }

func (s *Snapshot) ReadAll() ([]Recorded, error) {
	return append([]Recorded(nil), s.records...), nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return hex.EncodeToString(b)
}
