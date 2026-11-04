package wire

import "time"

// Skill improvements, with a member's token:
//
//	POST /v1/projects/{project}/skill-improvements               ← ProposeSkillImprovement → 201 SkillImprovement
//	POST /v1/projects/{project}/skill-improvements/{id}/revise   ← ReviseSkillImprovement → SkillImprovement
//	POST /v1/projects/{project}/skill-improvements/{id}/approve  ← ApproveSkillImprovement → SkillImprovement
//	POST /v1/projects/{project}/skill-improvements/{id}/withdraw ← {} → SkillImprovement
//	GET  /v1/projects/{project}/skill-improvements[?skill=&state=] → SkillImprovements
//	GET  /v1/projects/{project}/skill-improvements/{id}          → SkillImprovement, with its landings
//
// and with the project secret, the one read it allows:
//
//	GET  /v1/projects/{project}/skill-improvements/approved      → ApprovedSkillImprovements

// Approval scopes.
const (
	ScopeProject     = "project"
	ScopeAllProjects = "all-projects"
)

// Skill improvement states.
const (
	ImprovementProposed  = "proposed"
	ImprovementApproved  = "approved"
	ImprovementWithdrawn = "withdrawn"
)

// Landing outcomes.
const (
	LandingApplied    = "applied"
	LandingDeclined   = "declined"
	LandingNotApplied = "not-applied"
)

// Problem kinds of skill improvements.
const (
	ProblemUnknownInstructionVersion   = "unknown-instruction-version"
	ProblemImprovementTextFixed        = "improvement-text-fixed"
	ProblemImprovementAlreadyApproved  = "improvement-already-approved"
	ProblemImprovementWithdrawn        = "improvement-withdrawn"
	ProblemImprovementRevisedSinceRead = "improvement-revised-since-read"
	ProblemImprovementNotApproved      = "improvement-not-approved"
	ProblemConflict                    = "conflict"
)

// AssessmentRef is the assessment an improvement was started from.
type AssessmentRef struct {
	Work  string `json:"work"`
	Stage string `json:"stage,omitempty"`
	Visit int    `json:"visit,omitempty"`
}

type ProposeSkillImprovement struct {
	ID             string         `json:"id"`
	Skill          string         `json:"skill"`
	Improves       string         `json:"improves"`
	Text           string         `json:"text"`
	FromAssessment *AssessmentRef `json:"fromAssessment,omitempty"`
}

type ReviseSkillImprovement struct {
	Text string `json:"text"`
}

type ApproveSkillImprovement struct {
	Scope    string `json:"scope"`
	Revision int    `json:"revision"`
}

// SkillImprovement is one improvement as members see it.
type SkillImprovement struct {
	ID             string         `json:"id"`
	Project        string         `json:"project"`
	Skill          string         `json:"skill"`
	Improves       string         `json:"improves"`
	Text           string         `json:"text"`
	Revision       int            `json:"revision"`
	State          string         `json:"state"`
	Author         string         `json:"author"`
	FromAssessment *AssessmentRef `json:"fromAssessment,omitempty"`
	Scope          string         `json:"scope,omitempty"`
	ApprovedBy     string         `json:"approvedBy,omitempty"`
	ApprovedAt     *time.Time     `json:"approvedAt,omitempty"`
	WithdrawnBy    string         `json:"withdrawnBy,omitempty"`
	WithdrawnAt    *time.Time     `json:"withdrawnAt,omitempty"`
	// Landings are where the improvement was applied, declined or not
	// applied, as the projects' clones synced it.
	Landings []Landing `json:"landings,omitempty"`
}

type SkillImprovements struct {
	Improvements []SkillImprovement `json:"improvements"`
}

// Landing is one outcome of an improvement in a project's history.
type Landing struct {
	Project string    `json:"project"`
	Owner   string    `json:"owner"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason,omitempty"`
	Before  string    `json:"before,omitempty"`
	After   string    `json:"after,omitempty"`
	At      time.Time `json:"at"`
}

// ApprovedSkillImprovements are what a project's clones may apply, in
// approval order.
type ApprovedSkillImprovements struct {
	Improvements []ApprovedSkillImprovement `json:"improvements"`
}

type ApprovedSkillImprovement struct {
	ID         string    `json:"id"`
	Skill      string    `json:"skill"`
	Improves   string    `json:"improves"`
	Text       string    `json:"text"`
	Revision   int       `json:"revision"`
	Scope      string    `json:"scope"`
	ApprovedAt time.Time `json:"approvedAt"`
}
