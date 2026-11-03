package historyserver

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Target is what an assessment judges: a stage visit, or the whole work
// when Stage is empty.
type Target struct {
	Work  string
	Stage string
	Visit int
}

// maxReason is the most an assessment's reason may hold, in characters.
const maxReason = 4000

// Assess records member's judgement of target, which replaces theirs
// before it; the one before is kept. The target must be a work the
// server holds, or one of its stage visits.
func (s *Store) Assess(ctx context.Context, project, member string, target Target, a wire.Assess) (wire.Assessment, error) {
	a.Reason = strings.TrimSpace(a.Reason)
	switch {
	case a.Verdict == wire.VerdictWentWrong && a.Reason == "":
		return wire.Assessment{}, refuse(http.StatusBadRequest, wire.ProblemInvalid, "say why it went wrong: an assessment that it went wrong carries the reason")
	case a.Verdict == wire.VerdictGood && a.Reason != "":
		return wire.Assessment{}, refuse(http.StatusBadRequest, wire.ProblemInvalid, "a good assessment carries no reason: only went-wrong does")
	case a.Verdict != wire.VerdictGood && a.Verdict != wire.VerdictWentWrong:
		return wire.Assessment{}, refuse(http.StatusBadRequest, wire.ProblemInvalid, "the verdict is %q or %q, not %q", wire.VerdictGood, wire.VerdictWentWrong, a.Verdict)
	case utf8.RuneCountInString(a.Reason) > maxReason:
		return wire.Assessment{}, refuse(http.StatusBadRequest, wire.ProblemInvalid, "a reason holds at most %d characters", maxReason)
	}
	if err := s.holds(ctx, project, target); err != nil {
		return wire.Assessment{}, err
	}
	at := s.now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO assessments (project, work, stage, visit, member, verdict, reason, assessed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		project, target.Work, target.Stage, target.Visit, member, a.Verdict, a.Reason, at.Format(time.RFC3339Nano)); err != nil {
		return wire.Assessment{}, err
	}
	return wire.Assessment{Work: target.Work, Stage: target.Stage, Visit: target.Visit, Member: member, Verdict: a.Verdict, Reason: a.Reason, AssessedAt: at}, nil
}

// holds refuses a target the server doesn't hold.
func (s *Store) holds(ctx context.Context, project string, target Target) error {
	works, err := s.Works(ctx, project)
	if err != nil {
		return err
	}
	held := false
	for _, w := range works.Works {
		held = held || w.ID == target.Work
	}
	if !held {
		return refuse(http.StatusNotFound, wire.ProblemNotFound, "no work %s", target.Work)
	}
	if target.Stage == "" {
		return nil
	}
	events, err := s.workEvents(ctx, project, target.Work)
	if err != nil {
		return err
	}
	if !visitsOf(target.Work, events)[target] {
		return refuse(http.StatusNotFound, wire.ProblemNotFound, "work %s has no visit %d of %s", target.Work, target.Visit, target.Stage)
	}
	return nil
}

// AssessmentFilter narrows the assessments; zero fields match everything.
type AssessmentFilter struct{ Work, Stage, Member, Verdict string }

// Assessments are the project's current assessments, newest first: each
// member's last of each work and stage visit.
func (s *Store) Assessments(ctx context.Context, project string, f AssessmentFilter) (wire.Assessments, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT work, stage, visit, member, verdict, reason, assessed_at FROM assessments
		WHERE seq IN (SELECT MAX(seq) FROM assessments WHERE project = ? GROUP BY work, stage, visit, member)
		AND (? = '' OR work = ?) AND (? = '' OR stage = ?) AND (? = '' OR member = ?) AND (? = '' OR verdict = ?)
		ORDER BY seq DESC`, project, f.Work, f.Work, f.Stage, f.Stage, f.Member, f.Member, f.Verdict, f.Verdict)
	if err != nil {
		return wire.Assessments{}, err
	}
	defer rows.Close()
	out := wire.Assessments{Assessments: []wire.Assessment{}}
	for rows.Next() {
		var a wire.Assessment
		var at string
		if err := rows.Scan(&a.Work, &a.Stage, &a.Visit, &a.Member, &a.Verdict, &a.Reason, &at); err != nil {
			return wire.Assessments{}, err
		}
		if a.AssessedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return wire.Assessments{}, err
		}
		out.Assessments = append(out.Assessments, a)
	}
	return out, rows.Err()
}

// assess is a member assessing a work, or one of its stage visits.
func (h *handler) assess(w http.ResponseWriter, r *http.Request, project, member string) {
	target := Target{Work: r.PathValue("id"), Stage: r.PathValue("stage")}
	if target.Stage != "" {
		visit, err := strconv.Atoi(r.PathValue("visit"))
		if err != nil || visit < 1 {
			h.problem(w, r, refuse(http.StatusBadRequest, wire.ProblemInvalid, "a visit is a number from 1, not %q", r.PathValue("visit")))
			return
		}
		target.Visit = visit
	}
	var a wire.Assess
	if !h.decode(w, r, &a) {
		return
	}
	out, err := h.store.Assess(r.Context(), project, member, target, a)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	h.logger.Info("assessed", "project", project, "member", member, "work", target.Work, "stage", target.Stage, "visit", target.Visit, "verdict", out.Verdict)
	reply(w, http.StatusOK, out)
}

func (h *handler) assessments(w http.ResponseWriter, r *http.Request, project, _ string) {
	q := r.URL.Query()
	out, err := h.store.Assessments(r.Context(), project, AssessmentFilter{Work: q.Get("work"), Stage: q.Get("stage"), Member: q.Get("member"), Verdict: q.Get("verdict")})
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, out)
}
