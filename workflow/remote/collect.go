package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Log is the event log as a sync reads it: its records, each one's line as
// written, and its seal.
type Log struct {
	Records []eventlog.Recorded
	Lines   [][]byte
	Seal    string
}

// ReadLog takes a copy of the log under the shared lock, so the network
// never holds it.
func ReadLog(store *eventlog.Store) (Log, error) {
	var log Log
	err := store.Shared(func(s *eventlog.Snapshot) error {
		records, err := s.ReadAll()
		if err != nil {
			return err
		}
		log = Log{Records: records, Lines: s.Lines(), Seal: s.Seal()}
		return nil
	})
	return log, err
}

// pending is an event the server doesn't hold yet.
type pending struct {
	stream  string
	version int
	line    string
}

// Refusal is a stream gf doesn't send, and why.
type Refusal struct {
	Stream  string
	Version int
	Reason  string
}

// eventsToSend are the log's events after each stream's head on the
// server, in log order. A stream whose events up to the server's head
// differ from the server's is refused whole: the history the server holds
// isn't this log's.
func eventsToSend(log Log, heads map[string]wire.StreamHead) (events []pending, refused []Refusal) {
	digests := map[string]string{}
	counts := map[string]int{}
	bad := map[string]bool{}
	for i, r := range log.Records {
		stream := string(r.Stream)
		head, held := heads[stream]
		counts[stream]++
		if held && r.Version <= head.Version {
			digests[stream] = wire.Digest(digests[stream], log.Lines[i])
			if r.Version == head.Version && digests[stream] != head.Digest && !bad[stream] {
				bad[stream] = true
				refused = append(refused, Refusal{Stream: stream, Version: head.Version,
					Reason: "the server holds other events up to this version: this log's history of the stream was rewritten, or another clone advanced the same work"})
			}
			continue
		}
		events = append(events, pending{stream: stream, version: r.Version, line: string(log.Lines[i])})
	}
	kept := events[:0]
	for _, e := range events {
		if !bad[e.stream] {
			kept = append(kept, e)
		}
	}
	return kept, refused
}

// localFile is a file a sync names, and where it is.
type localFile struct {
	wire.File
	abs string
}

// Skip is a file gf can't send as the log tells about it.
type Skip struct {
	Path   string
	Reason string
}

// filesToSend are the files the configured includes send: the works'
// artifacts and inputs, their decision documents, and the kept versions
// the log names, sorted by path.
func filesToSend(root string, c Config, log Log, decisions domain.DecisionsDir) ([]localFile, []Skip, error) {
	var files []localFile
	var skipped []Skip
	seen := map[string]bool{}
	add := func(rel, work string) error {
		if seen[rel] {
			return nil
		}
		f, err := fileAt(root, rel, work)
		if err != nil {
			return err
		}
		seen[rel] = true
		files = append(files, f)
		return nil
	}
	var works []domain.WorkID
	versions := map[string]bool{}
	for _, r := range log.Records {
		switch e := r.Event.(type) {
		case domain.WorkStarted:
			works = append(works, e.WorkID)
			if !c.Includes(IncludeArtifacts) {
				continue
			}
			for _, in := range e.Inputs {
				f, err := fileAt(root, in.Path().String(), e.WorkID.String())
				switch {
				case errors.Is(err, fs.ErrNotExist):
					skipped = append(skipped, Skip{Path: in.Path().String(), Reason: "the work's input is gone"})
				case err != nil:
					return nil, nil, err
				case f.SHA256 != in.Hash().String():
					skipped = append(skipped, Skip{Path: in.Path().String(), Reason: "the work's input changed since the work started"})
				case !seen[f.Path]:
					seen[f.Path] = true
					files = append(files, f)
				}
			}
		case domain.SkillLoaded:
			if !e.Version.IsZero() {
				versions[e.Version.String()] = true
			}
		case domain.AgentStarted:
			if !e.Version.IsZero() {
				versions[e.Version.String()] = true
			}
		}
	}
	for _, w := range works {
		if c.Includes(IncludeArtifacts) {
			if err := walk(root, domain.ArtifactDir(w).String(), func(rel string) error { return add(rel, w.String()) }); err != nil {
				return nil, nil, err
			}
		}
		if c.Includes(IncludeDecisions) {
			entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(decisions.Path().String())))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, nil, err
			}
			for _, entry := range entries {
				rel := decisions.Path().String() + "/" + entry.Name()
				p, err := domain.ParseRepoPath(rel)
				if err == nil && entry.Type().IsRegular() && decisions.IsDecisionOf(p, w) {
					if err := add(rel, w.String()); err != nil {
						return nil, nil, err
					}
				}
			}
		}
	}
	if c.Includes(IncludeInstructions) {
		for v := range versions {
			dir := eventlog.InstructionsPath() + "/" + v
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); errors.Is(err, fs.ErrNotExist) {
				skipped = append(skipped, Skip{Path: dir, Reason: "the log names this version, but it isn't kept in this clone"})
				continue
			}
			if err := walk(root, dir, func(rel string) error { return add(rel, "") }); err != nil {
				return nil, nil, err
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	return files, skipped, nil
}

// walk calls fn with every regular file under dir, relative to root.
func walk(root, dir string, fn func(rel string) error) error {
	base := filepath.Join(root, filepath.FromSlash(dir))
	err := filepath.WalkDir(base, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel))
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func fileAt(root, rel, work string) (localFile, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	content, err := os.ReadFile(abs)
	if err != nil {
		return localFile{}, err
	}
	sum := sha256.Sum256(content)
	return localFile{File: wire.File{Path: rel, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)), Work: work}, abs: abs}, nil
}
