// Package agentsessions keeps local bookkeeping about Claude Code sessions in
// .gofast/runtime/: which sessions are driving work, and which assignment each
// stage agent started on. These are runtime markers, not workflow events: they
// never go to the event log and are not committed.
package agentsessions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

var errEmptyID = errors.New("empty session or agent id")

// Store is the marker store of one repository.
type Store struct{ dir string }

func Open(root string) Store {
	return Store{dir: filepath.Join(eventlog.Dir(root), eventlog.RuntimeDir)}
}

// AgentStart is what a stage agent started on: the assignment and its
// stream version, to tell later whether the agent submitted.
type AgentStart struct {
	Assignment domain.AssignmentID
	Version    int
}

type agentStartJSON struct {
	Assignment string `json:"assignment"`
	Version    int    `json:"version"`
}

func (s Store) MarkDriving(session string) error {
	path, err := s.path("driving", session)
	if err != nil {
		return err
	}
	return writeFile(path, nil)
}

func (s Store) IsDriving(session string) (bool, error) {
	path, err := s.path("driving", session)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read driving marker: %w", err)
	}
	return true, nil
}

func (s Store) ClearDriving(session string) error {
	path, err := s.path("driving", session)
	if err != nil {
		return err
	}
	return remove(path)
}

func (s Store) RecordAgentStart(agent string, start AgentStart) error {
	path, err := s.path("agents", agent)
	if err != nil {
		return err
	}
	data, err := json.Marshal(agentStartJSON{Assignment: start.Assignment.String(), Version: start.Version})
	if err != nil {
		return fmt.Errorf("encode agent start: %w", err)
	}
	return writeFile(path, data)
}

// AgentStart returns what the agent started on, if it was recorded.
func (s Store) AgentStart(agent string) (AgentStart, bool, error) {
	path, err := s.path("agents", agent)
	if err != nil {
		return AgentStart{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AgentStart{}, false, nil
	}
	if err != nil {
		return AgentStart{}, false, fmt.Errorf("read agent start: %w", err)
	}
	var j agentStartJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return AgentStart{}, false, fmt.Errorf("decode agent start %s: %w", path, err)
	}
	id, err := domain.ParseAssignmentID(j.Assignment)
	if err != nil {
		return AgentStart{}, false, fmt.Errorf("decode agent start %s: %w", path, err)
	}
	return AgentStart{Assignment: id, Version: j.Version}, true, nil
}

func (s Store) ForgetAgent(agent string) error {
	path, err := s.path("agents", agent)
	if err != nil {
		return err
	}
	return remove(path)
}

// path names a marker file by a hash of the id, so any id is a safe filename.
func (s Store) path(kind, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%s marker: %w", kind, errEmptyID)
	}
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, kind, hex.EncodeToString(sum[:16])), nil
}

// writeFile writes atomically: a crash leaves the old marker or the new one.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
