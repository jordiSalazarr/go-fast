// Package eventlog stores workflow events as JSON lines in .gofast/events.jsonl
// at the repository root, guarded by a file lock.
package eventlog

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// Init creates .gofast/ under root if needed, and makes sure its .gitignore
// lists the files that stay local: the lock, the log and runtime markers.
func Init(root string) error {
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, ".gitignore")
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(string(current), "\n") {
		listed[strings.TrimSpace(line)] = true
	}
	var missing strings.Builder
	if len(current) > 0 && !bytes.HasSuffix(current, []byte("\n")) {
		missing.WriteString("\n")
	}
	for _, entry := range ignored {
		if !listed[entry] {
			missing.WriteString(entry + "\n")
		}
	}
	if missing.Len() == 0 || missing.String() == "\n" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("update %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(missing.String()); err != nil {
		return fmt.Errorf("update %s: %w", path, err)
	}
	return nil
}

// ignored lists what .gofast/.gitignore keeps out of git.
var ignored = []string{lockFile, logFile, RuntimeDir + "/"}

// Dir is gofast's directory under root.
func Dir(root string) string { return filepath.Join(root, dirName) }

// LogFile is where gf writes its diagnostic log.
func LogFile(root string) string { return filepath.Join(root, dirName, logFile) }

// Store is the event log of one repository.
type Store struct {
	dir    string
	now    func() time.Time
	newID  func() string
	logger *slog.Logger
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

// Exclusive runs fn holding the exclusive lock, for a whole
// read → decide → append cycle. An interrupted final line is truncated.
func (s *Store) Exclusive(fn func(*Session) error) error {
	unlock, err := s.lock(syscall.LOCK_EX)
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

	records, err := s.load(f, true)
	if err != nil {
		return err
	}
	return fn(newSession(s, f, records))
}

// Shared runs fn holding the shared lock, for reading only. An interrupted
// final line is ignored; the next writer truncates it.
func (s *Store) Shared(fn func(*Snapshot) error) error {
	unlock, err := s.lock(syscall.LOCK_SH)
	if err != nil {
		return err
	}
	defer unlock()

	path := filepath.Join(s.dir, eventsFile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return fn(&Snapshot{})
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	records, err := s.load(f, false)
	if err != nil {
		return err
	}
	return fn(&Snapshot{records: records})
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

// load reads every complete line. A final line without a newline is an
// interrupted write: it is ignored, and truncated when truncate is set.
func (s *Store) load(f *os.File, truncate bool) ([]Recorded, error) {
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
	return parse(complete)
}

func parse(data []byte) ([]Recorded, error) {
	var records []Recorded
	versions := map[Stream]int{}
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines[:len(lines)-1] {
		r, err := decodeLine(line)
		if err != nil {
			return nil, &MalformedLogError{Line: i + 1, Err: err}
		}
		if r.Position != i+1 {
			return nil, &MalformedLogError{Line: i + 1, Err: fmt.Errorf("position %d, want %d", r.Position, i+1)}
		}
		if want := versions[r.Stream] + 1; r.Version != want {
			return nil, &MalformedLogError{Line: i + 1, Err: fmt.Errorf("stream %s version %d, want %d", r.Stream, r.Version, want)}
		}
		versions[r.Stream] = r.Version
		records = append(records, r)
	}
	return records, nil
}

// Session reads and appends while holding the exclusive lock.
type Session struct {
	store    *Store
	file     *os.File
	records  []Recorded
	versions map[Stream]int
}

func newSession(s *Store, f *os.File, records []Recorded) *Session {
	versions := map[Stream]int{}
	for _, r := range records {
		versions[r.Stream] = r.Version
	}
	return &Session{store: s, file: f, records: records, versions: versions}
}

// ReadAll returns every event in log order.
func (s *Session) ReadAll() ([]Recorded, error) {
	return append([]Recorded(nil), s.records...), nil
}

// Append writes events to stream if the stream is at expectedVersion. All
// lines are written in one write and synced to disk before returning.
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
			Position:   len(s.records) + i + 1,
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
	s.versions[stream] = expectedVersion + len(events)
	return nil
}

// Snapshot is what a reader sees while holding the shared lock.
type Snapshot struct{ records []Recorded }

func (s *Snapshot) ReadAll() ([]Recorded, error) {
	return append([]Recorded(nil), s.records...), nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return hex.EncodeToString(b)
}
