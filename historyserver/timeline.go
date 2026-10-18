package historyserver

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// The event names and payload fields the read endpoints use. They are the
// event log's stable names (workflow/eventlog/codec.go): never renamed.
const (
	typeWorkStarted   = "WorkStarted"
	typeWorkCompleted = "WorkCompleted"
	typeWorkAbandoned = "WorkAbandoned"
	typeSkillLoaded   = "SkillLoaded"
	typeAgentStarted  = "AgentStarted"
)

type workStarted struct {
	WorkID      string `json:"workId"`
	WorkType    string `json:"workType"`
	Description string `json:"description"`
	Branch      string `json:"branch"`
}

type instructionPayload struct {
	Stage   string `json:"stage"`
	Visit   int    `json:"visit"`
	Agent   string `json:"agent"`
	Skill   string `json:"skill"`
	Load    string `json:"load"`
	Version string `json:"sha256"`
}

func (s *Store) events(ctx context.Context, project, where string, args ...any) ([]wire.Envelope, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT line FROM events WHERE project = ? AND (`+where+`) ORDER BY occurred_at, seq`, append([]any{project}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wire.Envelope
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		var env wire.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

// Works lists the project's works, oldest first.
func (s *Store) Works(ctx context.Context, project string) (wire.Works, error) {
	events, err := s.events(ctx, project, `type IN (?, ?, ?)`, typeWorkStarted, typeWorkCompleted, typeWorkAbandoned)
	if err != nil {
		return wire.Works{}, err
	}
	out := wire.Works{Works: []wire.Work{}}
	index := map[string]int{}
	for _, e := range events {
		var p workStarted
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		switch e.Type {
		case typeWorkStarted:
			index[p.WorkID] = len(out.Works)
			out.Works = append(out.Works, wire.Work{ID: p.WorkID, Type: p.WorkType, Description: p.Description, Branch: p.Branch, StartedAt: e.OccurredAt, Status: "active"})
		case typeWorkCompleted, typeWorkAbandoned:
			if i, ok := index[p.WorkID]; ok {
				out.Works[i].Status = map[string]string{typeWorkCompleted: "completed", typeWorkAbandoned: "abandoned"}[e.Type]
			}
		}
	}
	return out, nil
}

// Timeline is one work's history; ok is false for a work the server
// doesn't hold.
func (s *Store) Timeline(ctx context.Context, project, work string) (wire.Timeline, bool, error) {
	works, err := s.Works(ctx, project)
	if err != nil {
		return wire.Timeline{}, false, err
	}
	var t wire.Timeline
	found := false
	for _, w := range works.Works {
		if w.ID == work {
			t.Work, found = w, true
		}
	}
	if !found {
		return wire.Timeline{}, false, nil
	}
	assignments := "assignment-" + work + "-"
	events, err := s.events(ctx, project, `stream = ? OR stream = ? OR substr(stream, 1, ?) = ?`,
		"work-"+work, "observations-"+work, len(assignments), assignments)
	if err != nil {
		return wire.Timeline{}, false, err
	}
	t.Events, t.Visits, t.Files = []wire.Event{}, []wire.Visit{}, []wire.FileLink{}
	visits := map[[2]any]int{}
	for _, e := range events {
		t.Events = append(t.Events, wire.Event{ID: e.ID, Stream: e.Stream, Version: e.Version, Type: e.Type, OccurredAt: e.OccurredAt, Actor: e.Actor, Payload: e.Payload})
		if e.Type != typeSkillLoaded && e.Type != typeAgentStarted {
			continue
		}
		var p instructionPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		key := [2]any{p.Stage, p.Visit}
		i, ok := visits[key]
		if !ok {
			i = len(t.Visits)
			visits[key] = i
			t.Visits = append(t.Visits, wire.Visit{Stage: p.Stage, Visit: p.Visit, Instructions: []wire.Instruction{}})
		}
		in := wire.Instruction{Kind: "skill", Name: p.Skill, Load: p.Load, Version: p.Version}
		if e.Type == typeAgentStarted {
			in = wire.Instruction{Kind: "agent", Name: p.Agent, Version: p.Version}
		} else if in.Load == "" {
			in.Load = "invoked"
		}
		if in.Version != "" {
			if in.Files, err = s.files(ctx, project, `substr(path, 1, ?) = ?`, len(instructionsDir+in.Version+"/"), instructionsDir+in.Version+"/"); err != nil {
				return wire.Timeline{}, false, err
			}
		}
		t.Visits[i].Instructions = append(t.Visits[i].Instructions, in)
	}
	if t.Files, err = s.files(ctx, project, `work = ?`, work); err != nil {
		return wire.Timeline{}, false, err
	}
	for i := range t.Files {
		t.Files[i].URL = blobURL(project, t.Files[i].SHA256)
	}
	return t, true, nil
}

// instructionsDir is where gf keeps the instruction versions in a
// repository (eventlog.InstructionsPath).
const instructionsDir = ".gofast/instructions/"

func (s *Store) files(ctx context.Context, project, where string, args ...any) ([]wire.FileLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, sha256, size FROM files WHERE project = ? AND (`+where+`) ORDER BY path, first_seen`, append([]any{project}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wire.FileLink{}
	for rows.Next() {
		var f wire.FileLink
		if err := rows.Scan(&f.Path, &f.SHA256, &f.Size); err != nil {
			return nil, err
		}
		f.URL = blobURL(project, f.SHA256)
		out = append(out, f)
	}
	return out, rows.Err()
}

func blobURL(project, sha string) string {
	return wire.Version + "/projects/" + url.PathEscape(project) + "/blobs/" + sha
}
