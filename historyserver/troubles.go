package historyserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// The events that say a stage visit or a work went wrong on its own
// record, and those that start a stage visit; the event log's stable names
// (workflow/eventlog/codec.go).
const (
	typeAttemptFailed       = "AttemptFailed"
	typeAssignmentRejected  = "AssignmentRejected"
	typeAssignmentEscalated = "AssignmentEscalated"
	typeReworkStarted       = "ReworkStarted"
	typeWorkReturned        = "WorkReturned"
	typeStageEntered        = "StageEntered"
	typeAssignmentOpened    = "AssignmentOpened"
	typeFixerEntered        = "FixerEntered"
)

// fixer is the stage the fixer's visits are recorded under.
const fixer = "fixer"

// visitPayload holds the fields of every event this file reads.
type visitPayload struct {
	AssignmentID string `json:"assignmentId"`
	WorkID       string `json:"workId"`
	Stage        string `json:"stage"`
	Target       string `json:"target"`
	Visit        int    `json:"visit"`
	From         string `json:"from"`
	FromVisit    int    `json:"fromVisit"`
	Reason       string `json:"reason"`
	Feedback     string `json:"feedback"`
	AttemptsUsed int    `json:"attemptsUsed"`
	Budget       int    `json:"budget"`
}

// visitEvent is an event of a work, read for the stage visit it is in.
type visitEvent struct {
	wire.Envelope
	p visitPayload
}

// readVisits reads a work's events, and each of its assignments' stage
// visit from the AssignmentOpened that opened it.
func readVisits(work string, events []wire.Envelope) ([]visitEvent, map[string]Target) {
	out := make([]visitEvent, 0, len(events))
	assignments := map[string]Target{}
	for _, e := range events {
		var p visitPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		if e.Type == typeAssignmentOpened {
			assignments[p.AssignmentID] = Target{Work: work, Stage: p.Stage, Visit: p.Visit}
		}
		out = append(out, visitEvent{e, p})
	}
	return out, assignments
}

// troublesOf are the troubles among a work's events, in their order, each
// in the stage visit that went wrong: an attempt's, rejection's or
// escalation's own; for a rework, the visit of the target stage it
// reworks; for a return, the visit the owner sent the work back from.
func troublesOf(work string, events []wire.Envelope) []wire.Trouble {
	read, assignments := readVisits(work, events)
	latest := map[string]int{} // each stage's latest visit so far
	out := []wire.Trouble{}
	for _, e := range read {
		t := wire.Trouble{Work: work, Type: e.Type, Event: e.ID, OccurredAt: e.OccurredAt, Reason: e.p.Reason}
		switch e.Type {
		case typeAttemptFailed:
			t.Stage, t.Visit = assignments[e.p.AssignmentID].Stage, assignments[e.p.AssignmentID].Visit
		case typeAssignmentRejected:
			t.Stage, t.Visit, t.Reason = assignments[e.p.AssignmentID].Stage, assignments[e.p.AssignmentID].Visit, e.p.Feedback
		case typeAssignmentEscalated:
			t.Stage, t.Visit = assignments[e.p.AssignmentID].Stage, assignments[e.p.AssignmentID].Visit
			t.Reason = fmt.Sprintf("%d of %d attempts used", e.p.AttemptsUsed, e.p.Budget)
		case typeReworkStarted:
			t.Stage, t.Visit = e.p.Target, latest[e.p.Target]
			if t.Visit == 0 {
				t.Visit = e.p.Visit
			}
		case typeWorkReturned:
			t.Stage, t.Visit = e.p.From, e.p.FromVisit
		case typeWorkAbandoned:
		default:
			if v, ok := entered(work, e, assignments); ok && v.Visit > latest[v.Stage] {
				latest[v.Stage] = v.Visit
			}
			continue
		}
		if e.Type != typeWorkAbandoned && (t.Stage == "" || t.Visit == 0) {
			continue // an assignment the log never opened
		}
		out = append(out, t)
	}
	return out
}

// entered is the stage visit an event starts or opens, if any.
func entered(work string, e visitEvent, assignments map[string]Target) (Target, bool) {
	v := Target{Work: work}
	switch e.Type {
	case typeStageEntered:
		v.Stage, v.Visit = e.p.Stage, e.p.Visit
	case typeAssignmentOpened:
		v = assignments[e.p.AssignmentID]
	case typeFixerEntered:
		v.Stage, v.Visit = fixer, e.p.Visit
	}
	return v, v.Stage != "" && v.Visit > 0
}

// visitsOf are the stage visits a work's events record: those entered or
// opened, those a rework or a return starts, and those a return leaves.
func visitsOf(work string, events []wire.Envelope) map[Target]bool {
	read, assignments := readVisits(work, events)
	out := map[Target]bool{}
	for _, e := range read {
		if v, ok := entered(work, e, assignments); ok {
			out[v] = true
		}
		if (e.Type == typeReworkStarted || e.Type == typeWorkReturned) && e.p.Target != "" && e.p.Visit > 0 {
			out[Target{Work: work, Stage: e.p.Target, Visit: e.p.Visit}] = true
		}
		if e.Type == typeWorkReturned && e.p.From != "" && e.p.FromVisit > 0 {
			out[Target{Work: work, Stage: e.p.From, Visit: e.p.FromVisit}] = true
		}
	}
	return out
}

// TroubleFilter narrows the troubles; zero fields match everything.
type TroubleFilter struct{ Type, Stage string }

// Troubles are the project's troubles, newest first.
func (s *Store) Troubles(ctx context.Context, project string, f TroubleFilter) (wire.Troubles, error) {
	works, err := s.Works(ctx, project)
	if err != nil {
		return wire.Troubles{}, err
	}
	var all []wire.Trouble
	for _, w := range works.Works {
		events, err := s.workEvents(ctx, project, w.ID)
		if err != nil {
			return wire.Troubles{}, err
		}
		for _, t := range troublesOf(w.ID, events) {
			if (f.Type == "" || t.Type == f.Type) && (f.Stage == "" || t.Stage == f.Stage) {
				all = append(all, t)
			}
		}
	}
	// Reversed, then sorted stably: of troubles at the same instant, the
	// later in the log comes first too.
	out := wire.Troubles{Troubles: []wire.Trouble{}}
	for i := len(all) - 1; i >= 0; i-- {
		out.Troubles = append(out.Troubles, all[i])
	}
	sortNewestFirst(out.Troubles)
	return out, nil
}

func (h *handler) troubles(w http.ResponseWriter, r *http.Request, project, _ string) {
	q := r.URL.Query()
	out, err := h.store.Troubles(r.Context(), project, TroubleFilter{Type: q.Get("type"), Stage: q.Get("stage")})
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, out)
}

func sortNewestFirst(ts []wire.Trouble) {
	sort.SliceStable(ts, func(a, b int) bool { return ts[a].OccurredAt.After(ts[b].OccurredAt) })
}
