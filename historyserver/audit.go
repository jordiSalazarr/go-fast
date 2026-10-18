package historyserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// WorkFilter narrows the works list; zero fields match everything.
type WorkFilter struct {
	Status, Type, Branch string
	// Query must appear in the description, ignoring case.
	Query string
}

func (f WorkFilter) matches(w wire.Work) bool {
	return (f.Status == "" || w.Status == f.Status) && (f.Type == "" || w.Type == f.Type) &&
		(f.Branch == "" || w.Branch == f.Branch) &&
		(f.Query == "" || strings.Contains(strings.ToLower(w.Description), strings.ToLower(f.Query)))
}

// FilterWorks keeps the works that match.
func FilterWorks(works wire.Works, f WorkFilter) wire.Works {
	out := wire.Works{Works: []wire.Work{}}
	for _, w := range works.Works {
		if f.matches(w) {
			out.Works = append(out.Works, w)
		}
	}
	return out
}

// instructionEvents are the project's agent starts and skill loads, in the
// order they occurred, with the work each belongs to.
func (s *Store) instructionEvents(ctx context.Context, project string) ([]wire.Envelope, []instructionPayload, error) {
	events, err := s.events(ctx, project, `type IN (?, ?)`, typeAgentStarted, typeSkillLoaded)
	if err != nil {
		return nil, nil, err
	}
	payloads := make([]instructionPayload, 0, len(events))
	kept := events[:0]
	for _, e := range events {
		var p instructionPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		if e.Type == typeAgentStarted {
			p.Skill = ""
		} else {
			p.Agent = ""
			if p.Load == "" {
				p.Load = "invoked"
			}
		}
		kept = append(kept, e)
		payloads = append(payloads, p)
	}
	return kept, payloads, nil
}

func (p instructionPayload) name() (name, kind string) {
	if p.Agent != "" {
		return p.Agent, "agent"
	}
	return p.Skill, "skill"
}

// InstructionNames lists every skill and agent the history names.
func (s *Store) InstructionNames(ctx context.Context, project string) (wire.InstructionNames, error) {
	events, payloads, err := s.instructionEvents(ctx, project)
	if err != nil {
		return wire.InstructionNames{}, err
	}
	index := map[string]int{}
	versions := map[string]map[string]bool{}
	out := wire.InstructionNames{Names: []wire.InstructionName{}}
	for i, p := range payloads {
		name, kind := p.name()
		j, ok := index[kind+" "+name]
		if !ok {
			j = len(out.Names)
			index[kind+" "+name] = j
			versions[kind+" "+name] = map[string]bool{}
			out.Names = append(out.Names, wire.InstructionName{Name: name, Kind: kind})
		}
		versions[kind+" "+name][p.Version] = true
		out.Names[j].Versions = len(versions[kind+" "+name])
		out.Names[j].LastSeen = events[i].OccurredAt
	}
	sort.Slice(out.Names, func(a, b int) bool {
		if out.Names[a].Kind != out.Names[b].Kind {
			return out.Names[a].Kind < out.Names[b].Kind
		}
		return out.Names[a].Name < out.Names[b].Name
	})
	return out, nil
}

// Instructions are one skill's or agent's versions and their uses; ok is
// false for a name the history doesn't hold.
func (s *Store) Instructions(ctx context.Context, project, name string) (wire.Instructions, bool, error) {
	events, payloads, err := s.instructionEvents(ctx, project)
	if err != nil {
		return wire.Instructions{}, false, err
	}
	out := wire.Instructions{Name: name, Versions: []wire.InstructionVersion{}}
	index := map[string]int{}
	for i, p := range payloads {
		n, kind := p.name()
		if n != name {
			continue
		}
		out.Kind = kind
		j, ok := index[p.Version]
		if !ok {
			j = len(out.Versions)
			index[p.Version] = j
			v := wire.InstructionVersion{Version: p.Version, FirstSeen: events[i].OccurredAt, Files: []wire.FileLink{}, Uses: []wire.InstructionUse{}}
			if p.Version != "" {
				if v.Files, err = s.versionFiles(ctx, project, p.Version); err != nil {
					return wire.Instructions{}, false, err
				}
			}
			out.Versions = append(out.Versions, v)
		}
		out.Versions[j].Uses = append(out.Versions[j].Uses, wire.InstructionUse{
			Work: workOf(events[i].Stream), Stage: p.Stage, Visit: p.Visit, Load: p.Load, At: events[i].OccurredAt})
	}
	return out, out.Kind != "", nil
}

// workOf is the work an observations stream belongs to.
func workOf(stream string) string { return strings.TrimPrefix(stream, "observations-") }

func (s *Store) versionFiles(ctx context.Context, project, version string) ([]wire.FileLink, error) {
	prefix := instructionsDir + version + "/"
	return s.files(ctx, project, `substr(path, 1, ?) = ?`, len(prefix), prefix)
}

// Diff compares two kept versions; ok is false when the server holds the
// files of neither.
func (s *Store) Diff(ctx context.Context, project, from, to string) (wire.VersionDiff, bool, error) {
	read := func(version string) (map[string][]byte, error) {
		files, err := s.versionFiles(ctx, project, version)
		if err != nil {
			return nil, err
		}
		out := map[string][]byte{}
		for _, f := range files {
			content, _, err := s.Blob(ctx, project, f.SHA256)
			if err != nil {
				return nil, err
			}
			out[strings.TrimPrefix(f.Path, instructionsDir+version+"/")] = content
		}
		return out, nil
	}
	a, err := read(from)
	if err != nil {
		return wire.VersionDiff{}, false, err
	}
	b, err := read(to)
	if err != nil {
		return wire.VersionDiff{}, false, err
	}
	if len(a) == 0 && len(b) == 0 {
		return wire.VersionDiff{}, false, nil
	}
	paths := map[string]bool{}
	for p := range a {
		paths[p] = true
	}
	for p := range b {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	out := wire.VersionDiff{From: from, To: to, Files: []wire.FileDiff{}}
	for _, p := range sorted {
		before, inA := a[p]
		after, inB := b[p]
		d := wire.FileDiff{Path: p}
		switch {
		case !inA:
			d.Status = "added"
		case !inB:
			d.Status = "removed"
		case string(before) == string(after):
			d.Status = "same"
		default:
			d.Status = "changed"
		}
		if utf8.Valid(before) && utf8.Valid(after) && d.Status != "same" {
			d.Unified, err = difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				A: lines(before), B: lines(after),
				FromFile: from[:min(12, len(from))] + "/" + p, ToFile: to[:min(12, len(to))] + "/" + p, Context: 3,
			})
			if err != nil {
				return wire.VersionDiff{}, false, err
			}
		}
		out.Files = append(out.Files, d)
	}
	return out, true, nil
}

// lines splits text after each newline. (difflib.SplitLines adds a line
// that isn't there.)
func lines(text []byte) []string {
	out := strings.SplitAfter(string(text), "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
