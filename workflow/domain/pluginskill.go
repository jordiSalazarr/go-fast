package domain

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// A PluginSkill is one gofast skill directory of the plugin directory the
// session loaded, $CLAUDE_PLUGIN_ROOT/skills/<name>/. Its state is its
// record, the .applied-improvements dotfile next to SKILL.md, which lists
// one entry per Skill improvement gf started, applied, could not apply, or
// noticed the owner declined. The owner may delete any line to retry it.

// ImproveSkillsAgent is the agent that applies approved skill
// improvements, as Claude Code names it. It is no stage's agent.
const ImproveSkillsAgent = "gofast:improve-skills"

// SkillImprovementID is a Skill improvement's id on the history server,
// si-<hex>.
type SkillImprovementID string

// ParseSkillImprovementID restores an improvement's id: si- and lowercase
// letters, digits and dashes.
func ParseSkillImprovementID(s string) (SkillImprovementID, error) {
	rest, ok := strings.CutPrefix(s, "si-")
	if !ok || rest == "" || len(rest) > 61 || strings.Trim(rest, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || rest[0] == '-' {
		return "", fmt.Errorf("skill improvement id %q (si- and lowercase letters, digits and dashes): %w", s, ErrInvalidInput)
	}
	return SkillImprovementID(s), nil
}

// SessionID is the Claude Code session applying improvements: the
// SessionStart input's session_id, exported as GOFAST_SESSION.
type SessionID string

// ImprovementOutcome is what a record entry says happened. The zero value
// is a line gf could not read, which counts as recorded so a hand-edited
// line never causes a second application.
type ImprovementOutcome int

const (
	OutcomeUnreadable ImprovementOutcome = iota
	OutcomeInProgress
	OutcomeApplied
	OutcomeNotApplied
	OutcomeDeclined
)

// NotAppliedCause is why an improvement was not applied.
type NotAppliedCause int

const (
	// CauseConflict: the improvement contradicts the skill's current text.
	CauseConflict NotAppliedCause = iota + 1
	// CauseRefused: the improvement asked for anything but an edit to the
	// skill's wording.
	CauseRefused
)

// RecordEntry is one line of a skill's record:
// `<improvementId> <outcome> <before> <after|-> [<session>] [# reason]`.
type RecordEntry struct {
	Improvement SkillImprovementID
	Outcome     ImprovementOutcome
	Before      InstructionsVersion
	// After is zero ("-") unless the improvement was applied.
	After   InstructionsVersion
	Session SessionID
	Reason  string
}

// ApprovedImprovementFact is an improvement the history server approved
// for this project, as the startup fetch listed it.
type ApprovedImprovementFact struct {
	ID         SkillImprovementID
	Skill      string
	Text       string
	ApprovedAt time.Time
}

// PluginSkillRejection is a command a PluginSkill rule refuses.
type PluginSkillRejection string

func (r PluginSkillRejection) Error() string {
	return string(r)
}

const (
	ErrImprovementAlreadyRecorded PluginSkillRejection = "the skill's record already lists the improvement"
	ErrSkillImprovementInProgress PluginSkillRejection = "another improvement of the skill is in progress"
	ErrSkillOutOfScope            PluginSkillRejection = "only gofast's own skills are improved, never a vendored one"
	ErrImprovementNotInProgress   PluginSkillRejection = "the improvement is not in progress on the skill for this session"
)

// PluginSkill is any state of a skill directory.
type PluginSkill interface{ isPluginSkill() }

// IdleSkill has no improvement in progress.
type IdleSkill struct {
	Skill  string
	Record []RecordEntry
}

// ImprovingSkill has one improvement in progress, held by one session;
// Record holds the other entries.
type ImprovingSkill struct {
	Skill      string
	Record     []RecordEntry
	InProgress RecordEntry
}

func (IdleSkill) isPluginSkill()      {}
func (ImprovingSkill) isPluginSkill() {}

// PluginSkillOf is the state a skill's record describes.
func PluginSkillOf(skill string, record []RecordEntry) PluginSkill {
	others := make([]RecordEntry, 0, len(record))
	var inProgress *RecordEntry
	for _, e := range record {
		if e.Outcome == OutcomeInProgress && inProgress == nil {
			e := e
			inProgress = &e
			continue
		}
		others = append(others, e)
	}
	if len(others) == 0 {
		others = nil
	}
	if inProgress != nil {
		return ImprovingSkill{Skill: skill, Record: others, InProgress: *inProgress}
	}
	return IdleSkill{Skill: skill, Record: others}
}

// Entries is the skill's record, the entry in progress last.
func Entries(s PluginSkill) []RecordEntry {
	switch s := s.(type) {
	case IdleSkill:
		return slices.Clone(s.Record)
	case ImprovingSkill:
		return append(slices.Clone(s.Record), s.InProgress)
	}
	return nil
}

// recorded is the record's entry for id, if any.
func recorded(record []RecordEntry, id SkillImprovementID) (RecordEntry, bool) {
	for _, e := range record {
		if e.Improvement == id {
			return e, true
		}
	}
	return RecordEntry{}, false
}

// AppliedImprovements are the ids the record lists as applied and not
// declined, in record order: the improvements the skill holds now.
func AppliedImprovements(record []RecordEntry) []SkillImprovementID {
	var ids []SkillImprovementID
	for _, e := range record {
		if e.Outcome == OutcomeApplied {
			ids = append(ids, e.Improvement)
		}
	}
	return ids
}

// SkillImprovementStarted: a session took an improvement of the skill.
type SkillImprovementStarted struct {
	Improvement SkillImprovementID
	Skill       string
	Session     SessionID
	Before      InstructionsVersion
}

// SkillImproved: the improvement was applied; After equals Before when
// the skill already held it.
type SkillImproved struct {
	Improvement SkillImprovementID
	Skill       string
	Before      InstructionsVersion
	After       InstructionsVersion
	Summary     string
}

// SkillImprovementNotApplied: the improvement conflicted or was refused;
// the skill is unchanged at Version.
type SkillImprovementNotApplied struct {
	Improvement SkillImprovementID
	Skill       string
	Version     InstructionsVersion
	Cause       NotAppliedCause
	Reason      Reason
}

// SkillImprovementDeclined: the owner reverted an applied improvement; the
// skill is back at Version.
type SkillImprovementDeclined struct {
	Improvement SkillImprovementID
	Skill       string
	Version     InstructionsVersion
}

func (SkillImprovementStarted) isEvent()    {}
func (SkillImproved) isEvent()              {}
func (SkillImprovementNotApplied) isEvent() {}
func (SkillImprovementDeclined) isEvent()   {}

// StartSkillImprovement takes improvement for session on an idle skill at
// version current.
func (s IdleSkill) StartSkillImprovement(improvement ApprovedImprovementFact, session SessionID, current InstructionsVersion, vendored bool) (SkillImprovementStarted, error) {
	if vendored {
		return SkillImprovementStarted{}, fmt.Errorf("start %s on %s: %w", improvement.ID, s.Skill, ErrSkillOutOfScope)
	}
	if _, ok := recorded(s.Record, improvement.ID); ok {
		return SkillImprovementStarted{}, fmt.Errorf("start %s on %s: %w", improvement.ID, s.Skill, ErrImprovementAlreadyRecorded)
	}
	return SkillImprovementStarted{Improvement: improvement.ID, Skill: s.Skill, Session: session, Before: current}, nil
}

// ImproveSkill records the improvement in progress as applied, the skill
// now at after.
func (s ImprovingSkill) ImproveSkill(id SkillImprovementID, session SessionID, after InstructionsVersion, summary string) (SkillImproved, error) {
	if err := s.holds(id, session); err != nil {
		return SkillImproved{}, err
	}
	return SkillImproved{Improvement: id, Skill: s.Skill, Before: s.InProgress.Before, After: after, Summary: summary}, nil
}

// holds refuses an outcome for anything but the improvement in progress,
// by the session holding it.
func (s ImprovingSkill) holds(id SkillImprovementID, session SessionID) error {
	if s.InProgress.Improvement != id || s.InProgress.Session != session {
		return fmt.Errorf("%s by %s on %s, where %s holds %s: %w", id, session, s.Skill,
			s.InProgress.Session, s.InProgress.Improvement, ErrImprovementNotInProgress)
	}
	return nil
}

// ReportImprovementNotApplied records the improvement in progress as not
// applied, for cause.
func (s ImprovingSkill) ReportImprovementNotApplied(id SkillImprovementID, session SessionID, cause NotAppliedCause, reason Reason) (SkillImprovementNotApplied, error) {
	if err := s.holds(id, session); err != nil {
		return SkillImprovementNotApplied{}, err
	}
	return SkillImprovementNotApplied{Improvement: id, Skill: s.Skill, Version: s.InProgress.Before, Cause: cause, Reason: reason}, nil
}

// StartSkillImprovement runs Start on any state; the holding session
// starting again records nothing.
func StartSkillImprovement(s PluginSkill, improvement ApprovedImprovementFact, session SessionID, current InstructionsVersion, vendored bool) ([]Event, error) {
	switch s := s.(type) {
	case IdleSkill:
		e, err := s.StartSkillImprovement(improvement, session, current, vendored)
		if err != nil {
			return nil, err
		}
		return []Event{e}, nil
	case ImprovingSkill:
		if vendored {
			return nil, fmt.Errorf("start %s on %s: %w", improvement.ID, s.Skill, ErrSkillOutOfScope)
		}
		if s.InProgress.Improvement == improvement.ID && s.InProgress.Session == session {
			return nil, nil
		}
		return nil, fmt.Errorf("start %s on %s, where %s holds %s: %w", improvement.ID, s.Skill,
			s.InProgress.Session, s.InProgress.Improvement, ErrSkillImprovementInProgress)
	}
	return nil, fmt.Errorf("start %s: %w", improvement.ID, ErrSkillOutOfScope)
}

// ImproveSkill runs ImproveSkill on any state; the holding session
// repeating a recorded outcome records nothing.
func ImproveSkill(s PluginSkill, id SkillImprovementID, session SessionID, after InstructionsVersion, summary string) ([]Event, error) {
	switch s := s.(type) {
	case ImprovingSkill:
		if s.InProgress.Improvement != id {
			if repeatedOutcome(s.Record, id, session, OutcomeApplied) {
				return nil, nil
			}
		}
		e, err := s.ImproveSkill(id, session, after, summary)
		if err != nil {
			return nil, err
		}
		return []Event{e}, nil
	case IdleSkill:
		if repeatedOutcome(s.Record, id, session, OutcomeApplied) {
			return nil, nil
		}
		return nil, fmt.Errorf("apply %s on %s: %w", id, s.Skill, ErrImprovementNotInProgress)
	}
	return nil, fmt.Errorf("apply %s: %w", id, ErrImprovementNotInProgress)
}

// repeatedOutcome reports whether session already recorded outcome for id.
func repeatedOutcome(record []RecordEntry, id SkillImprovementID, session SessionID, outcome ImprovementOutcome) bool {
	e, ok := recorded(record, id)
	return ok && e.Outcome == outcome && e.Session == session
}

// ReportImprovementNotApplied runs ReportImprovementNotApplied on any
// state; the holding session repeating a recorded outcome records nothing.
func ReportImprovementNotApplied(s PluginSkill, id SkillImprovementID, session SessionID, cause NotAppliedCause, reason Reason) ([]Event, error) {
	switch s := s.(type) {
	case ImprovingSkill:
		if s.InProgress.Improvement != id {
			if repeatedOutcome(s.Record, id, session, OutcomeNotApplied) {
				return nil, nil
			}
		}
		e, err := s.ReportImprovementNotApplied(id, session, cause, reason)
		if err != nil {
			return nil, err
		}
		return []Event{e}, nil
	case IdleSkill:
		if repeatedOutcome(s.Record, id, session, OutcomeNotApplied) {
			return nil, nil
		}
		return nil, fmt.Errorf("report %s not applied on %s: %w", id, s.Skill, ErrImprovementNotInProgress)
	}
	return nil, fmt.Errorf("report %s not applied: %w", id, ErrImprovementNotInProgress)
}

// DeclineSkillImprovement notices the applied improvements that changed
// the skill and that the skill, now at current, is back from: each counts
// as declined, once.
func DeclineSkillImprovement(s PluginSkill, current InstructionsVersion) []Event {
	var skill string
	var record []RecordEntry
	switch s := s.(type) {
	case IdleSkill:
		skill, record = s.Skill, s.Record
	case ImprovingSkill:
		skill, record = s.Skill, s.Record
	default:
		return nil
	}
	if current.IsZero() {
		return nil
	}
	// Walking the applied entries in record order, an entry counts as
	// declined once current is its before-version or the before-version of
	// an applied entry recorded before it.
	var events []Event
	backBefore := false
	for _, e := range record {
		if e.Outcome != OutcomeApplied && e.Outcome != OutcomeDeclined {
			continue
		}
		if e.Before == current {
			backBefore = true
		}
		if backBefore && e.Outcome == OutcomeApplied && e.Before != e.After {
			events = append(events, SkillImprovementDeclined{Improvement: e.Improvement, Skill: skill, Version: current})
		}
	}
	return events
}

// EvolvePluginSkill applies one fact; it never fails.
func EvolvePluginSkill(s PluginSkill, e Event) PluginSkill {
	switch e := e.(type) {
	case SkillImprovementStarted:
		if idle, ok := s.(IdleSkill); ok {
			return ImprovingSkill{Skill: idle.Skill, Record: slices.Clone(idle.Record), InProgress: RecordEntry{
				Improvement: e.Improvement, Outcome: OutcomeInProgress, Before: e.Before, Session: e.Session,
			}}
		}
	case SkillImproved:
		if imp, ok := s.(ImprovingSkill); ok && imp.InProgress.Improvement == e.Improvement {
			done := imp.InProgress
			done.Outcome, done.Before, done.After = OutcomeApplied, e.Before, e.After
			return IdleSkill{Skill: imp.Skill, Record: append(slices.Clone(imp.Record), done)}
		}
	case SkillImprovementNotApplied:
		if imp, ok := s.(ImprovingSkill); ok && imp.InProgress.Improvement == e.Improvement {
			done := imp.InProgress
			done.Outcome, done.Before, done.After, done.Reason = OutcomeNotApplied, e.Version, InstructionsVersion{}, e.Reason.String()
			return IdleSkill{Skill: imp.Skill, Record: append(slices.Clone(imp.Record), done)}
		}
	case SkillImprovementDeclined:
		switch st := s.(type) {
		case IdleSkill:
			st.Record = declined(st.Record, e.Improvement)
			return st
		case ImprovingSkill:
			st.Record = declined(st.Record, e.Improvement)
			return st
		}
	}
	return s
}

// declined is record with id's applied entry marked declined.
func declined(record []RecordEntry, id SkillImprovementID) []RecordEntry {
	out := slices.Clone(record)
	for i, e := range out {
		if e.Improvement == id && e.Outcome == OutcomeApplied {
			out[i].Outcome = OutcomeDeclined
		}
	}
	return out
}

// recordFile is a skill's record, which only gf writes.
const recordFile = ".applied-improvements"

// ImproverMayWrite reports whether session's improvement agent may write
// path (absolute, cleaned): only inside the directory, under skillsDir, of
// the skill whose record holds session's improvement in progress, and
// never that record. held is the skill, if any.
func ImproverMayWrite(skillsDir string, local []LocalSkill, session SessionID, path string) (held string, allowed bool) {
	skill, ok := SkillHeldBy(local, session)
	if !ok {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Join(skillsDir, skill), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return skill, false
	}
	return skill, rel != recordFile
}

// LocalSkill is what the startup check knows about one skill of the loaded
// plugin directory.
type LocalSkill struct {
	Name     string
	Vendored bool
	Record   []RecordEntry
}

// PendingSkill is a skill's improvements to apply now, in approval order.
type PendingSkill struct {
	Skill        string
	Improvements []ApprovedImprovementFact
}

// PendingSkillImprovements are the approved improvements no skill's record
// lists, for gofast skills the plugin directory has, grouped by skill:
// skills by their earliest approval, improvements in approval order.
func PendingSkillImprovements(approved []ApprovedImprovementFact, local []LocalSkill) []PendingSkill {
	skills := make(map[string]LocalSkill, len(local))
	for _, l := range local {
		skills[l.Name] = l
	}
	ordered := slices.Clone(approved)
	slices.SortStableFunc(ordered, func(a, b ApprovedImprovementFact) int { return a.ApprovedAt.Compare(b.ApprovedAt) })
	var pending []PendingSkill
	index := map[string]int{}
	for _, a := range ordered {
		l, ok := skills[a.Skill]
		if !ok || l.Vendored {
			continue
		}
		if _, listed := recorded(l.Record, a.ID); listed {
			continue
		}
		i, seen := index[a.Skill]
		if !seen {
			i = len(pending)
			index[a.Skill] = i
			pending = append(pending, PendingSkill{Skill: a.Skill})
		}
		pending[i].Improvements = append(pending[i].Improvements, a)
	}
	return pending
}

// SkillHeldBy is the skill whose record holds session's improvement in
// progress, if any: the one skill the improvement agent may write.
func SkillHeldBy(local []LocalSkill, session SessionID) (string, bool) {
	if session == "" {
		return "", false
	}
	for _, l := range local {
		if l.Vendored {
			continue
		}
		if imp, ok := PluginSkillOf(l.Name, l.Record).(ImprovingSkill); ok && imp.InProgress.Session == session {
			return l.Name, true
		}
	}
	return "", false
}

// LeftInProgress are the improvements in progress held by another session
// than session: left by a session that ended before recording an outcome.
// Their lines block their skills until the owner deletes them.
func LeftInProgress(local []LocalSkill, session SessionID) []ImprovingSkill {
	var out []ImprovingSkill
	for _, l := range local {
		if imp, ok := PluginSkillOf(l.Name, l.Record).(ImprovingSkill); ok && imp.InProgress.Session != session {
			out = append(out, imp)
		}
	}
	return out
}
