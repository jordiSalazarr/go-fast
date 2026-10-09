package eventlog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
)

// The seal makes the event log tamper-evident. After every append gf records
// the log's SHA-256, its size and the git HEAD in .gofast/runtime/log.sum;
// every read compares the log with it. A log changed outside gf blocks
// writes until the owner accepts it; readers still read it, with a warning.
// Git changes the log legitimately (checkout, merge, pull, rebase): when the
// log is the one committed at HEAD, and either HEAD has moved since the seal
// or the log gf last wrote is committed on a branch (so nothing gf wrote is
// lost), the seal follows git. Reverting uncommitted events to the committed
// log without moving HEAD (git stash, git checkout -- <log>) stays a change.

const sealFile = "log.sum"

// ErrLogChangedOutsideGf: the event log differs from what gf last wrote.
var ErrLogChangedOutsideGf = errors.New("event log changed outside gf")

// LogChangedError reports a log changed outside gf since gf's last write.
type LogChangedError struct {
	Appended bool // gf's content is intact and lines were added after it
}

func (e *LogChangedError) Error() string {
	if e.Appended {
		return ErrLogChangedOutsideGf.Error() + ": lines were appended after gf's last write"
	}
	return ErrLogChangedOutsideGf.Error() + ": its content differs from gf's last write"
}

func (e *LogChangedError) Unwrap() error { return ErrLogChangedOutsideGf }

// Commits is what the seal needs from git.
type Commits interface {
	// Head is the commit HEAD points at; empty before the first commit.
	Head() (string, error)
	// LogAt is the event log committed at commit; nil when it has none.
	LogAt(commit string) ([]byte, error)
	// LogsOnBranches are the event logs committed at the local branch tips.
	LogsOnBranches() ([][]byte, error)
}

// WithSeal makes the store tamper-evident (see LogChangedError), with git's
// commits telling legitimate changes apart.
func WithSeal(commits Commits) Option { return func(s *Store) { s.commits = commits } }

type seal struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Head   string `json:"head"`
}

func sealOf(sum hash.Hash, size int64, head string) seal {
	return seal{SHA256: hex.EncodeToString(sum.Sum(nil)), Size: size, Head: head}
}

func sealData(data []byte, head string) seal {
	sum := sha256.New()
	sum.Write(data)
	return sealOf(sum, int64(len(data)), head)
}

func (s seal) matches(data []byte) bool {
	return s.Size == int64(len(data)) && s == sealData(data, s.Head)
}

// prefixOf: data is the sealed content followed by more.
func (s seal) prefixOf(data []byte) bool {
	return int64(len(data)) > s.Size && s.matches(data[:s.Size])
}

// checkSeal compares the log's complete lines with the seal. A missing seal
// is created, and a log git changed along with HEAD is resealed.
func (s *Store) checkSeal(data []byte) error {
	if s.commits == nil {
		return nil
	}
	recorded, ok := s.readSeal()
	if !ok {
		return s.writeSeal(sealData(data, s.head()))
	}
	if recorded.matches(data) {
		return nil
	}
	if head, ok := s.followsGit(recorded, data); ok {
		s.logger.Info("event log follows git", "from", recorded.Head, "to", head)
		return s.writeSeal(sealData(data, head))
	}
	changed := &LogChangedError{Appended: recorded.prefixOf(data)}
	s.logger.Warn("event log changed outside gf", "appended", changed.Appended,
		"sealed_size", recorded.Size, "size", len(data), "sealed_head", recorded.Head)
	return changed
}

// followsGit reports whether git, not someone else, changed the log since
// the seal, and returns HEAD.
func (s *Store) followsGit(recorded seal, data []byte) (string, bool) {
	head := s.head()
	committed, err := s.commits.LogAt(head)
	if err != nil {
		s.logger.Warn("could not read the event log committed at HEAD", "head", head, "error", err.Error())
		return head, false
	}
	if !bytes.Equal(committed, data) {
		return head, false
	}
	if head != recorded.Head {
		return head, true
	}
	logs, err := s.commits.LogsOnBranches()
	if err != nil {
		s.logger.Warn("could not read the event logs committed on branches", "error", err.Error())
		return head, false
	}
	for _, l := range logs {
		if recorded.matches(l) {
			return head, true
		}
	}
	return head, false
}

func (s *Store) head() string {
	head, err := s.commits.Head()
	if err != nil {
		s.logger.Warn("could not read git HEAD for the event log seal", "error", err.Error())
	}
	return head
}

// readSeal reads log.sum; a missing or unreadable one counts as missing.
func (s *Store) readSeal() (seal, bool) {
	data, err := os.ReadFile(s.sealPath())
	if err != nil {
		return seal{}, false
	}
	var recorded seal
	if err := json.Unmarshal(data, &recorded); err != nil {
		s.logger.Warn("ignoring unreadable event log seal", "error", err.Error())
		return seal{}, false
	}
	return recorded, true
}

// writeSeal replaces log.sum atomically; readers may write it concurrently.
func (s *Store) writeSeal(sl seal) error {
	dir := filepath.Dir(s.sealPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write event log seal: %w", err)
	}
	data, err := json.Marshal(sl)
	if err != nil {
		return fmt.Errorf("write event log seal: %w", err)
	}
	tmp, err := os.CreateTemp(dir, sealFile+".*")
	if err != nil {
		return fmt.Errorf("write event log seal: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write event log seal: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write event log seal: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.sealPath()); err != nil {
		return fmt.Errorf("write event log seal: %w", err)
	}
	return nil
}

func (s *Store) sealPath() string { return filepath.Join(s.dir, RuntimeDir, sealFile) }

// Accept seals the log as it is now, after the owner checked a change made
// outside gf. It returns how many events the log holds.
func (s *Store) Accept() (int, error) {
	unlock, err := s.lock(lockExclusive)
	if err != nil {
		return 0, err
	}
	defer unlock()

	path := filepath.Join(s.dir, eventsFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	data, err := s.readComplete(f, true)
	if err != nil {
		return 0, err
	}
	records, err := parse(data)
	if err != nil {
		return 0, err
	}
	head := ""
	if s.commits != nil {
		head = s.head()
	}
	if err := s.writeSeal(sealData(data, head)); err != nil {
		return 0, err
	}
	s.logger.Info("owner accepted the event log", "size", len(data), "events", len(records))
	return len(records), nil
}
