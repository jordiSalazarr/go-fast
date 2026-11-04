// Package skillimprovement decides a Skill improvement: a member's proposed
// change to one skill's wording, written against the instruction version it
// improves; proposed, then approved for its project or for every project of
// its organisation, and possibly withdrawn. It does no I/O: the server loads
// an improvement's events, folds them with Evolve, runs a command and
// appends what it returns.
package skillimprovement

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// maxTextLength is the most characters an improvement's text holds, as
// many as an assessment's reason.
const maxTextLength = 4000

// ID is an improvement's id, si-<hex>, made by the viewer's form once so a
// retried proposal lands on the same improvement.
type ID string

// ProjectID is the project an improvement was written in.
type ProjectID string

// SkillName is a skill directory's name, e.g. aggregate-design.
type SkillName string

// InstructionVersion is a skill's instruction version: the SHA-256 of its
// manifest, in lowercase hex.
type InstructionVersion string

// Member is a member of the project's organisation, by name.
type Member string

// Revision counts an improvement's texts: 1 when proposed, one more per
// revise.
type Revision int

// Text is an improvement's text: trimmed, from 1 to 4000 characters.
type Text string

// ParseText trims s and refuses an empty or too long text with
// ErrInvalidText.
func ParseText(s string) (Text, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n == 0 || n > maxTextLength {
		return "", fmt.Errorf("text of %d characters: %w", n, ErrInvalidText)
	}
	return Text(s), nil
}

// Scope is whom an approval reaches. The zero value is "not set", which
// the edge refuses.
type Scope int

const (
	ScopeUnset Scope = iota
	// ThisProject: the improvement's project's clones fetch it.
	ThisProject
	// AllProjects: the clones of every current and future project of the
	// improvement's organisation fetch it.
	AllProjects
)

// AssessmentRef is the went-wrong assessment an improvement was started
// from: a work, or one of its stage visits.
type AssessmentRef struct {
	Work  string
	Stage string
	Visit int
}

// RecordedVersionFact is what the project's synced history says about a
// skill: every instruction version of it a stage visit loaded.
type RecordedVersionFact struct {
	Skill    SkillName
	Versions []InstructionVersion
}

// Draft is what a member writes to propose an improvement.
type Draft struct {
	ID       ID
	Project  ProjectID
	Skill    SkillName
	Improves InstructionVersion
	Text     Text
	Author   Member
	From     *AssessmentRef
}

// Rejection is a command a rule refuses.
type Rejection string

func (r Rejection) Error() string {
	return string(r)
}

const (
	ErrInvalidText                 Rejection = "an improvement's text holds from 1 to 4000 characters"
	ErrUnknownInstructionVersion   Rejection = "the project has recorded no such instruction version of the skill"
	ErrImprovementTextFixed        Rejection = "an improvement's text changes only while it is proposed"
	ErrImprovementAlreadyApproved  Rejection = "the improvement is already approved with another scope"
	ErrImprovementWithdrawn        Rejection = "a withdrawn improvement is never approved again"
	ErrImprovementRevisedSinceRead Rejection = "the improvement was revised since its approver read it"
	ErrImprovementNotApproved      Rejection = "only an approved improvement is withdrawn"
	ErrNoImprovement               Rejection = "no such improvement"
)

// SkillImprovement is any state of an improvement.
type SkillImprovement interface{ isSkillImprovement() }

// Proposal is what every state of an existing improvement holds.
type Proposal struct {
	ID       ID
	Project  ProjectID
	Skill    SkillName
	Improves InstructionVersion
	Text     Text
	Revision Revision
}

// NoImprovement is the initial state: no event of the id.
type NoImprovement struct{}

// ProposedImprovement may be revised and approved.
type ProposedImprovement struct {
	Proposal
}

// ApprovedImprovement may be withdrawn; its text is fixed.
type ApprovedImprovement struct {
	Proposal
	Scope      Scope
	ApprovedBy Member
	ApprovedAt time.Time
}

// WithdrawnImprovement is final.
type WithdrawnImprovement struct {
	Proposal
	Scope       Scope
	ApprovedBy  Member
	ApprovedAt  time.Time
	WithdrawnBy Member
	WithdrawnAt time.Time
}

func (NoImprovement) isSkillImprovement()        {}
func (ProposedImprovement) isSkillImprovement()  {}
func (ApprovedImprovement) isSkillImprovement()  {}
func (WithdrawnImprovement) isSkillImprovement() {}

// Event is a fact of an improvement.
type Event interface{ isEvent() }

type SkillImprovementProposed struct {
	ID       ID
	Project  ProjectID
	Skill    SkillName
	Improves InstructionVersion
	Text     Text
	Revision Revision
	Author   Member
	From     *AssessmentRef
	At       time.Time
}

type SkillImprovementRevised struct {
	ID       ID
	Text     Text
	Revision Revision
	At       time.Time
}

type SkillImprovementApproved struct {
	ID       ID
	Scope    Scope
	Revision Revision
	By       Member
	At       time.Time
}

type SkillImprovementWithdrawn struct {
	ID ID
	By Member
	At time.Time
}

func (SkillImprovementProposed) isEvent()  {}
func (SkillImprovementRevised) isEvent()   {}
func (SkillImprovementApproved) isEvent()  {}
func (SkillImprovementWithdrawn) isEvent() {}

// Propose writes a new improvement against a version its project recorded.
func (NoImprovement) Propose(d Draft, recorded RecordedVersionFact, at time.Time) (SkillImprovementProposed, error) {
	if recorded.Skill != d.Skill || !slices.Contains(recorded.Versions, d.Improves) {
		return SkillImprovementProposed{}, fmt.Errorf("propose %s for %s at %s: %w", d.ID, d.Skill, d.Improves, ErrUnknownInstructionVersion)
	}
	var from *AssessmentRef
	if d.From != nil {
		ref := *d.From
		from = &ref
	}
	return SkillImprovementProposed{
		ID: d.ID, Project: d.Project, Skill: d.Skill, Improves: d.Improves,
		Text: d.Text, Revision: 1, Author: d.Author, From: from, At: at,
	}, nil
}

// Revise replaces a proposed improvement's text.
func (p ProposedImprovement) Revise(text Text, at time.Time) (SkillImprovementRevised, error) {
	return SkillImprovementRevised{ID: p.ID, Text: text, Revision: p.Revision + 1, At: at}, nil
}

// Approve approves the revision the approver read, for scope.
func (p ProposedImprovement) Approve(scope Scope, read Revision, by Member, at time.Time) (SkillImprovementApproved, error) {
	if read != p.Revision {
		return SkillImprovementApproved{}, fmt.Errorf("approve %s read at revision %d, now %d: %w", p.ID, read, p.Revision, ErrImprovementRevisedSinceRead)
	}
	return SkillImprovementApproved{ID: p.ID, Scope: scope, Revision: p.Revision, By: by, At: at}, nil
}

// Withdraw stops clones that have not applied the improvement.
func (a ApprovedImprovement) Withdraw(by Member, at time.Time) (SkillImprovementWithdrawn, error) {
	return SkillImprovementWithdrawn{ID: a.ID, By: by, At: at}, nil
}

// ProposeSkillImprovement runs Propose on any state; a repeated proposal
// of the same draft records nothing.
func ProposeSkillImprovement(s SkillImprovement, d Draft, recorded RecordedVersionFact, at time.Time) ([]Event, error) {
	switch s.(type) {
	case nil, NoImprovement:
		e, err := NoImprovement{}.Propose(d, recorded, at)
		if err != nil {
			return nil, err
		}
		return []Event{e}, nil
	default:
		// The id already names an improvement: a retried proposal.
		return nil, nil
	}
}

// ReviseSkillImprovement runs Revise on any state; the current text again
// records nothing.
func ReviseSkillImprovement(s SkillImprovement, text Text, at time.Time) ([]Event, error) {
	switch s := s.(type) {
	case ProposedImprovement:
		if s.Text == text {
			return nil, nil
		}
		e, err := s.Revise(text, at)
		if err != nil {
			return nil, err
		}
		return []Event{e}, nil
	case ApprovedImprovement:
		return nil, fmt.Errorf("revise %s, approved: %w", s.ID, ErrImprovementTextFixed)
	case WithdrawnImprovement:
		return nil, fmt.Errorf("revise %s, withdrawn: %w", s.ID, ErrImprovementTextFixed)
	default:
		return nil, fmt.Errorf("revise: %w", ErrNoImprovement)
	}
}

// ApproveSkillImprovement runs Approve on any state; approving again with
// the same scope records nothing.
func ApproveSkillImprovement(s SkillImprovement, scope Scope, read Revision, by Member, at time.Time) ([]Event, error) {
	switch s := s.(type) {
	case ProposedImprovement:
		e, err := s.Approve(scope, read, by, at)
		if err != nil {
			return nil, err
		}
		return []Event{e}, nil
	case ApprovedImprovement:
		if s.Scope == scope {
			return nil, nil
		}
		return nil, fmt.Errorf("approve %s again with another scope: %w", s.ID, ErrImprovementAlreadyApproved)
	case WithdrawnImprovement:
		return nil, fmt.Errorf("approve %s: %w", s.ID, ErrImprovementWithdrawn)
	default:
		return nil, fmt.Errorf("approve: %w", ErrNoImprovement)
	}
}

// WithdrawSkillImprovement runs Withdraw on any state; withdrawing again
// records nothing.
func WithdrawSkillImprovement(s SkillImprovement, by Member, at time.Time) ([]Event, error) {
	switch s := s.(type) {
	case ApprovedImprovement:
		e, err := s.Withdraw(by, at)
		if err != nil {
			return nil, err
		}
		return []Event{e}, nil
	case WithdrawnImprovement:
		return nil, nil
	case ProposedImprovement:
		return nil, fmt.Errorf("withdraw %s, only proposed: %w", s.ID, ErrImprovementNotApproved)
	default:
		return nil, fmt.Errorf("withdraw: %w", ErrNoImprovement)
	}
}

// Evolve applies one fact; it never fails.
func Evolve(s SkillImprovement, e Event) SkillImprovement {
	switch e := e.(type) {
	case SkillImprovementProposed:
		return ProposedImprovement{Proposal: Proposal{
			ID: e.ID, Project: e.Project, Skill: e.Skill, Improves: e.Improves, Text: e.Text, Revision: e.Revision,
		}}
	case SkillImprovementRevised:
		if p, ok := s.(ProposedImprovement); ok {
			p.Text, p.Revision = e.Text, e.Revision
			return p
		}
	case SkillImprovementApproved:
		if p, ok := s.(ProposedImprovement); ok {
			return ApprovedImprovement{Proposal: p.Proposal, Scope: e.Scope, ApprovedBy: e.By, ApprovedAt: e.At}
		}
	case SkillImprovementWithdrawn:
		if a, ok := s.(ApprovedImprovement); ok {
			return WithdrawnImprovement{Proposal: a.Proposal, Scope: a.Scope, ApprovedBy: a.ApprovedBy,
				ApprovedAt: a.ApprovedAt, WithdrawnBy: e.By, WithdrawnAt: e.At}
		}
	}
	return s
}
