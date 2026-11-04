package historyserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jordiSalazarr/go-fast/historyserver/skillimprovement"
	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// Skill improvements are written, revised, approved and withdrawn here by
// members (docs/adr/0004). Each improvement is the fold of its rows of
// skill_improvement_events; the rules are skillimprovement's. Where each
// one landed comes from the skill-improvements streams the projects' gf
// syncs.

// The stored names of an improvement's facts: never renamed.
const (
	typeImprovementProposed  = "skill-improvement.proposed"
	typeImprovementRevised   = "skill-improvement.revised"
	typeImprovementApproved  = "skill-improvement.approved"
	typeImprovementWithdrawn = "skill-improvement.withdrawn"
)

// The synced events of a clone's skill-improvements stream the server
// reads for landings (workflow/eventlog/codec.go).
const (
	improvementsStream      = "skill-improvements"
	typeLandingApplied      = "skill-improvement.applied"
	typeLandingNotApplied   = "skill-improvement.not-applied"
	typeLandingDeclined     = "skill-improvement.declined"
	improvementCommandTries = 3
)

// improvementIDPattern is an improvement's id: si- and lowercase letters,
// digits and dashes, as the viewer's form makes it.
var improvementIDPattern = regexp.MustCompile(`^si-[a-z0-9][a-z0-9-]{0,60}$`)

// The stored payloads, one shape per fact: the adapter's, not the
// domain's.
type (
	proposedRow struct {
		Project  string              `json:"project"`
		Skill    string              `json:"skill"`
		Improves string              `json:"improves"`
		Text     string              `json:"text"`
		Revision int                 `json:"revision"`
		Author   string              `json:"author"`
		From     *wire.AssessmentRef `json:"fromAssessment,omitempty"`
	}
	revisedRow struct {
		Text     string `json:"text"`
		Revision int    `json:"revision"`
	}
	approvedRow struct {
		Scope    string `json:"scope"`
		Revision int    `json:"revision"`
	}
	withdrawnRow struct{}
)

// improvementRow is one stored fact of an improvement.
type improvementRow struct {
	seq         int64
	project     string
	improvement string
	version     int
	kind        string
	payload     []byte
	member      string
	at          time.Time
}

func scopeName(s skillimprovement.Scope) string {
	if s == skillimprovement.AllProjects {
		return wire.ScopeAllProjects
	}
	return wire.ScopeProject
}

func parseScope(s string) (skillimprovement.Scope, error) {
	switch s {
	case wire.ScopeProject:
		return skillimprovement.ThisProject, nil
	case wire.ScopeAllProjects:
		return skillimprovement.AllProjects, nil
	}
	return skillimprovement.ScopeUnset, refuse(http.StatusBadRequest, wire.ProblemInvalid, "the scope is %q or %q, not %q", wire.ScopeProject, wire.ScopeAllProjects, s)
}

// eventOf is the domain fact a row holds.
func eventOf(r improvementRow) (skillimprovement.Event, error) {
	id := skillimprovement.ID(r.improvement)
	switch r.kind {
	case typeImprovementProposed:
		var p proposedRow
		if err := json.Unmarshal(r.payload, &p); err != nil {
			return nil, err
		}
		e := skillimprovement.SkillImprovementProposed{ID: id, Project: skillimprovement.ProjectID(p.Project), Skill: skillimprovement.SkillName(p.Skill),
			Improves: skillimprovement.InstructionVersion(p.Improves), Text: skillimprovement.Text(p.Text), Revision: skillimprovement.Revision(p.Revision),
			Author: skillimprovement.Member(p.Author), At: r.at}
		if p.From != nil {
			e.From = &skillimprovement.AssessmentRef{Work: p.From.Work, Stage: p.From.Stage, Visit: p.From.Visit}
		}
		return e, nil
	case typeImprovementRevised:
		var p revisedRow
		if err := json.Unmarshal(r.payload, &p); err != nil {
			return nil, err
		}
		return skillimprovement.SkillImprovementRevised{ID: id, Text: skillimprovement.Text(p.Text), Revision: skillimprovement.Revision(p.Revision), At: r.at}, nil
	case typeImprovementApproved:
		var p approvedRow
		if err := json.Unmarshal(r.payload, &p); err != nil {
			return nil, err
		}
		scope, err := parseScope(p.Scope)
		if err != nil {
			return nil, err
		}
		return skillimprovement.SkillImprovementApproved{ID: id, Scope: scope, Revision: skillimprovement.Revision(p.Revision), By: skillimprovement.Member(r.member), At: r.at}, nil
	case typeImprovementWithdrawn:
		return skillimprovement.SkillImprovementWithdrawn{ID: id, By: skillimprovement.Member(r.member), At: r.at}, nil
	}
	return nil, fmt.Errorf("improvement %s: unknown fact %q", r.improvement, r.kind)
}

// rowOf is the row that stores a domain fact.
func rowOf(e skillimprovement.Event) (kind string, payload any, member string, at time.Time) {
	switch e := e.(type) {
	case skillimprovement.SkillImprovementProposed:
		p := proposedRow{Project: string(e.Project), Skill: string(e.Skill), Improves: string(e.Improves), Text: string(e.Text),
			Revision: int(e.Revision), Author: string(e.Author)}
		if e.From != nil {
			p.From = &wire.AssessmentRef{Work: e.From.Work, Stage: e.From.Stage, Visit: e.From.Visit}
		}
		return typeImprovementProposed, p, string(e.Author), e.At
	case skillimprovement.SkillImprovementRevised:
		return typeImprovementRevised, revisedRow{Text: string(e.Text), Revision: int(e.Revision)}, "", e.At
	case skillimprovement.SkillImprovementApproved:
		return typeImprovementApproved, approvedRow{Scope: scopeName(e.Scope), Revision: int(e.Revision)}, string(e.By), e.At
	case skillimprovement.SkillImprovementWithdrawn:
		return typeImprovementWithdrawn, withdrawnRow{}, string(e.By), e.At
	}
	return "", nil, "", time.Time{}
}

// improvementRows are the stored facts matching where, in the order they
// were stored.
func (s *Store) improvementRows(ctx context.Context, where string, args ...any) ([]improvementRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, project, improvement, version, type, payload, member, at FROM skill_improvement_events
		WHERE `+where+` ORDER BY seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []improvementRow
	for rows.Next() {
		var r improvementRow
		var payload, at string
		if err := rows.Scan(&r.seq, &r.project, &r.improvement, &r.version, &r.kind, &payload, &r.member, &at); err != nil {
			return nil, err
		}
		r.payload = []byte(payload)
		if r.at, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// organisationRows are the facts of every improvement written in org's
// projects.
func (s *Store) organisationRows(ctx context.Context, org string) ([]improvementRow, error) {
	return s.improvementRows(ctx, `project IN (SELECT id FROM projects WHERE organisation = ?)`, org)
}

// loadImprovement is id's state and version, for a member of org; an
// improvement of another organisation is no improvement.
func (s *Store) loadImprovement(ctx context.Context, org, id string) (skillimprovement.SkillImprovement, int, string, error) {
	rows, err := s.improvementRows(ctx, `improvement = ?`, id)
	if err != nil {
		return nil, 0, "", err
	}
	var state skillimprovement.SkillImprovement = skillimprovement.NoImprovement{}
	if len(rows) == 0 {
		return state, 0, "", nil
	}
	owner, err := s.organisationOf(ctx, rows[0].project)
	if err != nil {
		return nil, 0, "", err
	}
	if owner != org {
		return nil, 0, rows[0].project, refuse(http.StatusNotFound, wire.ProblemNotFound, "no skill improvement %s", id)
	}
	version := 0
	for _, r := range rows {
		e, err := eventOf(r)
		if err != nil {
			return nil, 0, "", err
		}
		state = skillimprovement.Evolve(state, e)
		version = r.version
	}
	return state, version, rows[0].project, nil
}

// errVersionTaken is another command storing at the same version first.
var errVersionTaken = errors.New("the improvement changed while deciding")

// appendImprovement stores events at the versions after expected, all or
// nothing.
func (s *Store) appendImprovement(ctx context.Context, project, id string, expected int, events []skillimprovement.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, e := range events {
		kind, payload, member, at := rowOf(e)
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO skill_improvement_events (project, improvement, version, type, payload, member, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			project, id, expected+i+1, kind, string(data), member, at.UTC().Format(time.RFC3339Nano))
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return errVersionTaken
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// improvementCommand runs decide on id's state, for a member of org, and
// stores what it returns at the next version; on a conflict it runs again,
// a few times, then refuses with conflict.
func (s *Store) improvementCommand(ctx context.Context, org, project, id string,
	decide func(skillimprovement.SkillImprovement) ([]skillimprovement.Event, error)) (created bool, err error) {
	for range improvementCommandTries {
		state, version, owner, err := s.loadImprovement(ctx, org, id)
		if err != nil {
			return false, err
		}
		if owner == "" {
			owner = project
		}
		events, err := decide(state)
		if err != nil {
			return false, improvementProblem(err)
		}
		if len(events) == 0 {
			return false, nil
		}
		switch err := s.appendImprovement(ctx, owner, id, version, events); {
		case errors.Is(err, errVersionTaken):
			continue
		case err != nil:
			return false, err
		}
		return version == 0, nil
	}
	return false, refuse(http.StatusConflict, wire.ProblemConflict, "skill improvement %s kept changing while it was decided: read it again and retry", id)
}

// improvementProblem maps a rule's refusal to the protocol's problem.
func improvementProblem(err error) error {
	codes := []struct {
		rule   skillimprovement.Rejection
		status int
		kind   string
	}{
		{skillimprovement.ErrInvalidText, http.StatusBadRequest, wire.ProblemInvalid},
		{skillimprovement.ErrUnknownInstructionVersion, http.StatusUnprocessableEntity, wire.ProblemUnknownInstructionVersion},
		{skillimprovement.ErrImprovementTextFixed, http.StatusConflict, wire.ProblemImprovementTextFixed},
		{skillimprovement.ErrImprovementAlreadyApproved, http.StatusConflict, wire.ProblemImprovementAlreadyApproved},
		{skillimprovement.ErrImprovementWithdrawn, http.StatusConflict, wire.ProblemImprovementWithdrawn},
		{skillimprovement.ErrImprovementRevisedSinceRead, http.StatusConflict, wire.ProblemImprovementRevisedSinceRead},
		{skillimprovement.ErrImprovementNotApproved, http.StatusConflict, wire.ProblemImprovementNotApproved},
		{skillimprovement.ErrNoImprovement, http.StatusNotFound, wire.ProblemNotFound},
	}
	for _, c := range codes {
		if errors.Is(err, c.rule) {
			return refuse(c.status, c.kind, "%s", c.rule.Error())
		}
	}
	return err
}

// recordedVersions is what project's synced history says of skill: every
// version of it a stage visit loaded.
func (s *Store) recordedVersions(ctx context.Context, project string, skill skillimprovement.SkillName) (skillimprovement.RecordedVersionFact, error) {
	_, payloads, err := s.instructionEvents(ctx, project)
	if err != nil {
		return skillimprovement.RecordedVersionFact{}, err
	}
	fact := skillimprovement.RecordedVersionFact{Skill: skill}
	for _, p := range payloads {
		if p.Skill == string(skill) && p.Version != "" {
			fact.Versions = append(fact.Versions, skillimprovement.InstructionVersion(p.Version))
		}
	}
	return fact, nil
}

// ProposeSkillImprovement writes member's improvement in project, of org.
// A retried proposal of the same id returns the improvement as it is.
func (s *Store) ProposeSkillImprovement(ctx context.Context, org, project, member string, in wire.ProposeSkillImprovement) (wire.SkillImprovement, bool, error) {
	var problems []string
	if !improvementIDPattern.MatchString(in.ID) {
		problems = append(problems, fmt.Sprintf("an improvement's id is si- and lowercase letters, digits and dashes, not %q", in.ID))
	}
	if !idPattern.MatchString(in.Skill) {
		problems = append(problems, fmt.Sprintf("a skill is its directory's name, such as \"aggregate-design\", not %q", in.Skill))
	}
	if !sha256Hex.MatchString(in.Improves) {
		problems = append(problems, fmt.Sprintf("improves is an instruction version, a SHA-256 in lowercase hex, not %q", in.Improves))
	}
	text, err := skillimprovement.ParseText(in.Text)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if in.FromAssessment != nil && in.FromAssessment.Work == "" {
		problems = append(problems, "an assessment an improvement starts from names its work")
	}
	if len(problems) > 0 {
		return wire.SkillImprovement{}, false, refuse(http.StatusBadRequest, wire.ProblemInvalid, "%s", strings.Join(problems, "; "))
	}
	skill := skillimprovement.SkillName(in.Skill)
	recorded, err := s.recordedVersions(ctx, project, skill)
	if err != nil {
		return wire.SkillImprovement{}, false, err
	}
	d := skillimprovement.Draft{ID: skillimprovement.ID(in.ID), Project: skillimprovement.ProjectID(project), Skill: skill,
		Improves: skillimprovement.InstructionVersion(in.Improves), Text: text, Author: skillimprovement.Member(member)}
	if f := in.FromAssessment; f != nil {
		d.From = &skillimprovement.AssessmentRef{Work: f.Work, Stage: f.Stage, Visit: f.Visit}
	}
	at := s.now().UTC()
	created, err := s.improvementCommand(ctx, org, project, in.ID, func(state skillimprovement.SkillImprovement) ([]skillimprovement.Event, error) {
		return skillimprovement.ProposeSkillImprovement(state, d, recorded, at)
	})
	if err != nil {
		return wire.SkillImprovement{}, false, err
	}
	out, err := s.SkillImprovement(ctx, org, in.ID)
	return out, created, err
}

// ReviseSkillImprovement replaces a proposed improvement's text.
func (s *Store) ReviseSkillImprovement(ctx context.Context, org, project, id string, in wire.ReviseSkillImprovement) (wire.SkillImprovement, error) {
	text, err := skillimprovement.ParseText(in.Text)
	if err != nil {
		return wire.SkillImprovement{}, improvementProblem(err)
	}
	at := s.now().UTC()
	return s.commandThen(ctx, org, project, id, func(state skillimprovement.SkillImprovement) ([]skillimprovement.Event, error) {
		return skillimprovement.ReviseSkillImprovement(state, text, at)
	})
}

// ApproveSkillImprovement approves the revision member read, for scope.
func (s *Store) ApproveSkillImprovement(ctx context.Context, org, project, member, id string, in wire.ApproveSkillImprovement) (wire.SkillImprovement, error) {
	scope, err := parseScope(in.Scope)
	if err != nil {
		return wire.SkillImprovement{}, err
	}
	if in.Revision < 1 {
		return wire.SkillImprovement{}, refuse(http.StatusBadRequest, wire.ProblemInvalid, "approve the revision you read: a number from 1, not %d", in.Revision)
	}
	at := s.now().UTC()
	return s.commandThen(ctx, org, project, id, func(state skillimprovement.SkillImprovement) ([]skillimprovement.Event, error) {
		return skillimprovement.ApproveSkillImprovement(state, scope, skillimprovement.Revision(in.Revision), skillimprovement.Member(member), at)
	})
}

// WithdrawSkillImprovement stops the clones that have not applied it.
func (s *Store) WithdrawSkillImprovement(ctx context.Context, org, project, member, id string) (wire.SkillImprovement, error) {
	at := s.now().UTC()
	return s.commandThen(ctx, org, project, id, func(state skillimprovement.SkillImprovement) ([]skillimprovement.Event, error) {
		return skillimprovement.WithdrawSkillImprovement(state, skillimprovement.Member(member), at)
	})
}

// commandThen runs a command on an existing improvement and returns it.
func (s *Store) commandThen(ctx context.Context, org, project, id string,
	decide func(skillimprovement.SkillImprovement) ([]skillimprovement.Event, error)) (wire.SkillImprovement, error) {
	if _, err := s.improvementCommand(ctx, org, project, id, decide); err != nil {
		return wire.SkillImprovement{}, err
	}
	return s.SkillImprovement(ctx, org, id)
}

// improvementView is the members' read model of the organisation's
// improvements, folded from the stored rows: each one with its approval's
// position, for the approved list's order.
type improvementView struct {
	wire.SkillImprovement
	approvedSeq int64
}

func improvementViews(rows []improvementRow) ([]*improvementView, error) {
	var out []*improvementView
	index := map[string]*improvementView{}
	for _, r := range rows {
		v := index[r.improvement]
		switch r.kind {
		case typeImprovementProposed:
			var p proposedRow
			if err := json.Unmarshal(r.payload, &p); err != nil {
				return nil, err
			}
			v = &improvementView{SkillImprovement: wire.SkillImprovement{ID: r.improvement, Project: p.Project, Skill: p.Skill,
				Improves: p.Improves, Text: p.Text, Revision: p.Revision, State: wire.ImprovementProposed, Author: p.Author, FromAssessment: p.From}}
			index[r.improvement] = v
			out = append(out, v)
		case typeImprovementRevised:
			var p revisedRow
			if v == nil || json.Unmarshal(r.payload, &p) != nil {
				continue
			}
			v.Text, v.Revision = p.Text, p.Revision
		case typeImprovementApproved:
			var p approvedRow
			if v == nil || json.Unmarshal(r.payload, &p) != nil {
				continue
			}
			at := r.at
			v.State, v.Scope, v.ApprovedBy, v.ApprovedAt, v.approvedSeq = wire.ImprovementApproved, p.Scope, r.member, &at, r.seq
		case typeImprovementWithdrawn:
			if v == nil {
				continue
			}
			at := r.at
			v.State, v.WithdrawnBy, v.WithdrawnAt = wire.ImprovementWithdrawn, r.member, &at
		}
	}
	return out, nil
}

// SkillImprovement is one improvement of org, with where it landed.
func (s *Store) SkillImprovement(ctx context.Context, org, id string) (wire.SkillImprovement, error) {
	rows, err := s.organisationRows(ctx, org)
	if err != nil {
		return wire.SkillImprovement{}, err
	}
	views, err := improvementViews(rows)
	if err != nil {
		return wire.SkillImprovement{}, err
	}
	for _, v := range views {
		if v.ID == id {
			out := v.SkillImprovement
			if out.Landings, err = s.landings(ctx, org, id); err != nil {
				return wire.SkillImprovement{}, err
			}
			return out, nil
		}
	}
	return wire.SkillImprovement{}, refuse(http.StatusNotFound, wire.ProblemNotFound, "no skill improvement %s", id)
}

// ImprovementFilter narrows the improvements; zero fields match
// everything.
type ImprovementFilter struct{ Skill, State string }

// SkillImprovements are org's improvements, oldest first, without their
// landings.
func (s *Store) SkillImprovements(ctx context.Context, org string, f ImprovementFilter) (wire.SkillImprovements, error) {
	rows, err := s.organisationRows(ctx, org)
	if err != nil {
		return wire.SkillImprovements{}, err
	}
	views, err := improvementViews(rows)
	if err != nil {
		return wire.SkillImprovements{}, err
	}
	out := wire.SkillImprovements{Improvements: []wire.SkillImprovement{}}
	for _, v := range views {
		if (f.Skill == "" || v.Skill == f.Skill) && (f.State == "" || v.State == f.State) {
			out.Improvements = append(out.Improvements, v.SkillImprovement)
		}
	}
	return out, nil
}

// ApprovedSkillImprovements are what project's clones may apply: approved
// for it, or for all projects of its organisation, and not withdrawn, in
// approval order.
func (s *Store) ApprovedSkillImprovements(ctx context.Context, project string) (wire.ApprovedSkillImprovements, error) {
	out := wire.ApprovedSkillImprovements{Improvements: []wire.ApprovedSkillImprovement{}}
	org, err := s.organisationOf(ctx, project)
	if err != nil || org == "" {
		return out, err
	}
	rows, err := s.organisationRows(ctx, org)
	if err != nil {
		return out, err
	}
	views, err := improvementViews(rows)
	if err != nil {
		return out, err
	}
	var approved []*improvementView
	for _, v := range views {
		if v.State == wire.ImprovementApproved && (v.Scope == wire.ScopeAllProjects || v.Project == project) {
			approved = append(approved, v)
		}
	}
	sortByApproval(approved)
	for _, v := range approved {
		out.Improvements = append(out.Improvements, wire.ApprovedSkillImprovement{ID: v.ID, Skill: v.Skill, Improves: v.Improves,
			Text: v.Text, Revision: v.Revision, Scope: v.Scope, ApprovedAt: *v.ApprovedAt})
	}
	return out, nil
}

func sortByApproval(views []*improvementView) {
	for i := 1; i < len(views); i++ {
		for j := i; j > 0 && views[j].approvedSeq < views[j-1].approvedSeq; j-- {
			views[j], views[j-1] = views[j-1], views[j]
		}
	}
}

// landingPayload is what the server reads of a synced outcome.
type landingPayload struct {
	ImprovementID string `json:"improvementId"`
	Before        string `json:"before"`
	After         string `json:"after"`
	Reason        string `json:"reason"`
}

// landings are where id was applied, declined or not applied, as the
// organisation's projects synced it.
func (s *Store) landings(ctx context.Context, org, id string) ([]wire.Landing, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, line FROM events
		WHERE project IN (SELECT id FROM projects WHERE organisation = ?) AND stream = ? AND type IN (?, ?, ?)
		ORDER BY occurred_at, seq`, org, improvementsStream, typeLandingApplied, typeLandingNotApplied, typeLandingDeclined)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wire.Landing
	for rows.Next() {
		var project, line string
		if err := rows.Scan(&project, &line); err != nil {
			return nil, err
		}
		var env wire.Envelope
		var p landingPayload
		if json.Unmarshal([]byte(line), &env) != nil || json.Unmarshal(env.Payload, &p) != nil || p.ImprovementID != id {
			continue
		}
		l := wire.Landing{Project: project, Owner: env.Actor.Name, At: env.OccurredAt}
		switch env.Type {
		case typeLandingApplied:
			l.Outcome, l.Before, l.After = wire.LandingApplied, p.Before, p.After
		case typeLandingNotApplied:
			l.Outcome, l.Reason = wire.LandingNotApplied, p.Reason
		case typeLandingDeclined:
			l.Outcome = wire.LandingDeclined
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// memberOrganisation is the organisation of the project a member's call
// names; inspects has already checked it is the member's.
func (h *handler) memberOrganisation(r *http.Request, project string) (string, error) {
	return h.store.organisationOf(r.Context(), project)
}

func (h *handler) proposeSkillImprovement(w http.ResponseWriter, r *http.Request, project, member string) {
	var in wire.ProposeSkillImprovement
	if !h.decode(w, r, &in) {
		return
	}
	org, err := h.memberOrganisation(r, project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	out, created, err := h.store.ProposeSkillImprovement(r.Context(), org, project, member, in)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		h.logger.Info("skill improvement proposed", "project", project, "member", member, "improvement", out.ID, "skill", out.Skill)
	}
	reply(w, status, out)
}

func (h *handler) reviseSkillImprovement(w http.ResponseWriter, r *http.Request, project, member string) {
	var in wire.ReviseSkillImprovement
	if !h.decode(w, r, &in) {
		return
	}
	h.improvementCommand(w, r, project, member, "revised", func(org string) (wire.SkillImprovement, error) {
		return h.store.ReviseSkillImprovement(r.Context(), org, project, r.PathValue("id"), in)
	})
}

func (h *handler) approveSkillImprovement(w http.ResponseWriter, r *http.Request, project, member string) {
	var in wire.ApproveSkillImprovement
	if !h.decode(w, r, &in) {
		return
	}
	h.improvementCommand(w, r, project, member, "approved", func(org string) (wire.SkillImprovement, error) {
		return h.store.ApproveSkillImprovement(r.Context(), org, project, member, r.PathValue("id"), in)
	})
}

func (h *handler) withdrawSkillImprovement(w http.ResponseWriter, r *http.Request, project, member string) {
	// The body is {} or nothing: a withdrawal carries no data.
	h.improvementCommand(w, r, project, member, "withdrawn", func(org string) (wire.SkillImprovement, error) {
		return h.store.WithdrawSkillImprovement(r.Context(), org, project, member, r.PathValue("id"))
	})
}

func (h *handler) improvementCommand(w http.ResponseWriter, r *http.Request, project, member, done string, run func(org string) (wire.SkillImprovement, error)) {
	org, err := h.memberOrganisation(r, project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	out, err := run(org)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	h.logger.Info("skill improvement "+done, "project", project, "member", member, "improvement", out.ID, "state", out.State)
	reply(w, http.StatusOK, out)
}

func (h *handler) skillImprovements(w http.ResponseWriter, r *http.Request, project, _ string) {
	org, err := h.memberOrganisation(r, project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	q := r.URL.Query()
	out, err := h.store.SkillImprovements(r.Context(), org, ImprovementFilter{Skill: q.Get("skill"), State: q.Get("state")})
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, out)
}

func (h *handler) skillImprovement(w http.ResponseWriter, r *http.Request, project, _ string) {
	org, err := h.memberOrganisation(r, project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	out, err := h.store.SkillImprovement(r.Context(), org, r.PathValue("id"))
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// approvedSkillImprovements is the one read the project secret may make.
func (h *handler) approvedSkillImprovements(w http.ResponseWriter, r *http.Request, project string) {
	out, err := h.store.ApprovedSkillImprovements(r.Context(), project)
	if err != nil {
		h.problem(w, r, err)
		return
	}
	reply(w, http.StatusOK, out)
}
