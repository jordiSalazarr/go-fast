package skillimprovement_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	si "github.com/jordiSalazarr/go-fast/historyserver/skillimprovement"
)

var (
	at    = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	later = at.Add(time.Hour)
)

const (
	v1 si.InstructionVersion = "1111111111111111111111111111111111111111111111111111111111111111"
	v2 si.InstructionVersion = "2222222222222222222222222222222222222222222222222222222222222222"
)

// recorded is the project's history of aggregate-design: it loaded v1.
var recorded = si.RecordedVersionFact{Skill: "aggregate-design", Versions: []si.InstructionVersion{v1}}

// draft is jordi's improvement of aggregate-design v1, from a went-wrong
// assessment of a design visit.
func draft() si.Draft {
	return si.Draft{
		ID: "si-1a2b", Project: "wallets", Skill: "aggregate-design", Improves: v1,
		Text: "Name the business intent behind an update.", Author: "jordi",
		From: &si.AssessmentRef{Work: "w-1", Stage: "design", Visit: 1},
	}
}

func proposal(revision si.Revision, text si.Text) si.Proposal {
	return si.Proposal{ID: "si-1a2b", Project: "wallets", Skill: "aggregate-design", Improves: v1, Text: text, Revision: revision}
}

func proposed() si.ProposedImprovement {
	return si.ProposedImprovement{Proposal: proposal(1, "Name the business intent behind an update.")}
}

func approvedFor(scope si.Scope) si.ApprovedImprovement {
	return si.ApprovedImprovement{Proposal: proposal(1, "Name the business intent behind an update."), Scope: scope, ApprovedBy: "ana", ApprovedAt: at}
}

func withdrawn() si.WithdrawnImprovement {
	return si.WithdrawnImprovement{Proposal: proposal(1, "Name the business intent behind an update."), Scope: si.ThisProject,
		ApprovedBy: "ana", ApprovedAt: at, WithdrawnBy: "jordi", WithdrawnAt: later}
}

// gofast:scenario A1
// Rule: a new improvement is proposed, at revision 1, naming the instruction version it improves.
func TestProposeSkillImprovement_IsProposedAtRevisionOneForTheVersionItImproves(t *testing.T) {
	// Given
	none := si.NoImprovement{}

	// When
	events, err := si.ProposeSkillImprovement(none, draft(), recorded, at)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []si.Event{si.SkillImprovementProposed{
		ID: "si-1a2b", Project: "wallets", Skill: "aggregate-design", Improves: v1,
		Text: "Name the business intent behind an update.", Revision: 1, Author: "jordi",
		From: &si.AssessmentRef{Work: "w-1", Stage: "design", Visit: 1}, At: at,
	}}, events)
}

// gofast:scenario A1
// Rule: an improvement names an instruction version of its skill that its project has recorded.
func TestProposeSkillImprovement_RejectsAVersionTheProjectNeverRecorded(t *testing.T) {
	// Given
	d := draft()
	d.Improves = v2

	// When
	_, err := si.ProposeSkillImprovement(si.NoImprovement{}, d, recorded, at)

	// Then
	require.ErrorIs(t, err, si.ErrUnknownInstructionVersion)
}

// gofast:scenario A1
// Rule: a version recorded for another skill is not a version of this one.
func TestProposeSkillImprovement_RejectsAVersionRecordedForAnotherSkill(t *testing.T) {
	// Given
	otherSkill := si.RecordedVersionFact{Skill: "code-layout", Versions: []si.InstructionVersion{v1}}

	// When
	_, err := si.ProposeSkillImprovement(si.NoImprovement{}, draft(), otherSkill, at)

	// Then
	require.ErrorIs(t, err, si.ErrUnknownInstructionVersion)
}

// gofast:scenario A1
// Rule: a retried proposal of the same improvement records nothing new.
func TestProposeSkillImprovement_RepeatedWithTheSameDraftRecordsNothing(t *testing.T) {
	// Given
	already := proposed()

	// When
	events, err := si.ProposeSkillImprovement(already, draft(), recorded, later)

	// Then
	require.NoError(t, err)
	assert.Empty(t, events)
}

// gofast:scenario A1
// Rule: an improvement's text is trimmed and holds from 1 to 4000 characters.
func TestProposeSkillImprovement_TextHoldsFromOneTo4000Characters(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want si.Text
		err  error
	}{
		{"trimmed", "  Name the intent.  ", "Name the intent.", nil},
		{"one character", "x", "x", nil},
		{"exactly 4000 characters", strings.Repeat("é", 4000), si.Text(strings.Repeat("é", 4000)), nil},
		{"empty", "   ", "", si.ErrInvalidText},
		{"4001 characters", strings.Repeat("é", 4001), "", si.ErrInvalidText},
	} {
		t.Run(c.name, func(t *testing.T) {
			// When
			got, err := si.ParseText(c.in)

			// Then
			if c.err != nil {
				require.ErrorIs(t, err, c.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// gofast:scenario A1
// Rule: a proposed improvement's text can be edited, which counts a new revision.
func TestReviseSkillImprovement_ChangesTheTextWhileProposed(t *testing.T) {
	// Given
	p := proposed()

	// When
	events, err := si.ReviseSkillImprovement(p, "Name the business intent, never UpdateX.", later)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []si.Event{si.SkillImprovementRevised{ID: "si-1a2b", Text: "Name the business intent, never UpdateX.", Revision: 2, At: later}}, events)
}

// gofast:scenario A1
// Rule: revising to the current text records nothing.
func TestReviseSkillImprovement_WithTheCurrentTextRecordsNothing(t *testing.T) {
	// Given
	p := proposed()

	// When
	events, err := si.ReviseSkillImprovement(p, p.Text, later)

	// Then
	require.NoError(t, err)
	assert.Empty(t, events)
}

// gofast:scenario A2
// Rule: any member, the author included, approves a proposed improvement for its project.
func TestApproveSkillImprovement_ApprovesForItsProjectEvenByItsAuthor(t *testing.T) {
	// Given
	p := proposed() // written by jordi

	// When
	events, err := si.ApproveSkillImprovement(p, si.ThisProject, 1, "jordi", later)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []si.Event{si.SkillImprovementApproved{ID: "si-1a2b", Scope: si.ThisProject, Revision: 1, By: "jordi", At: later}}, events)
}

// gofast:scenario A2
// Rule: an approved improvement's text can no longer change.
func TestReviseSkillImprovement_RejectsWhenApproved(t *testing.T) {
	// Given
	a := approvedFor(si.ThisProject)

	// When
	_, err := si.ReviseSkillImprovement(a, "Something else.", later)

	// Then
	require.ErrorIs(t, err, si.ErrImprovementTextFixed)
}

// gofast:scenario A2
// Rule: an approval approves the text its approver read, the latest revision.
func TestApproveSkillImprovement_RejectsWhenRevisedSinceRead(t *testing.T) {
	// Given
	revised := si.ProposedImprovement{Proposal: proposal(2, "Name the business intent, never UpdateX.")}

	// When
	_, err := si.ApproveSkillImprovement(revised, si.ThisProject, 1, "ana", later)

	// Then
	require.ErrorIs(t, err, si.ErrImprovementRevisedSinceRead)
}

// gofast:scenario A2
// Rule: approving again with the same scope records nothing.
func TestApproveSkillImprovement_RepeatedWithTheSameScopeRecordsNothing(t *testing.T) {
	// Given
	a := approvedFor(si.ThisProject)

	// When
	events, err := si.ApproveSkillImprovement(a, si.ThisProject, 1, "jordi", later)

	// Then
	require.NoError(t, err)
	assert.Empty(t, events)
}

// gofast:scenario A2
// Rule: an improvement is approved once: another scope is refused.
func TestApproveSkillImprovement_RejectsAnotherScopeWhenAlreadyApproved(t *testing.T) {
	// Given
	a := approvedFor(si.ThisProject)

	// When
	_, err := si.ApproveSkillImprovement(a, si.AllProjects, 1, "jordi", later)

	// Then
	require.ErrorIs(t, err, si.ErrImprovementAlreadyApproved)
}

// gofast:scenario A2
// Rule: an approved improvement holds the revision its approver read, with its scope and approver.
func TestEvolve_AnApprovedImprovementHoldsTheTextItsApproverRead(t *testing.T) {
	// Given
	history := []si.Event{
		si.SkillImprovementProposed{ID: "si-1a2b", Project: "wallets", Skill: "aggregate-design", Improves: v1, Text: "First.", Revision: 1, Author: "jordi", At: at},
		si.SkillImprovementRevised{ID: "si-1a2b", Text: "Second.", Revision: 2, At: at.Add(time.Minute)},
		si.SkillImprovementApproved{ID: "si-1a2b", Scope: si.ThisProject, Revision: 2, By: "ana", At: later},
	}

	// When
	var state si.SkillImprovement = si.NoImprovement{}
	for _, e := range history {
		state = si.Evolve(state, e)
	}

	// Then
	assert.Equal(t, si.ApprovedImprovement{Proposal: proposal(2, "Second."), Scope: si.ThisProject, ApprovedBy: "ana", ApprovedAt: later}, state)
}

// gofast:scenario A3
// Rule: a member may approve a proposed improvement for every project of its organisation.
func TestApproveSkillImprovement_ApprovesForAllProjects(t *testing.T) {
	// Given
	p := proposed()

	// When
	events, err := si.ApproveSkillImprovement(p, si.AllProjects, 1, "ana", later)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []si.Event{si.SkillImprovementApproved{ID: "si-1a2b", Scope: si.AllProjects, Revision: 1, By: "ana", At: later}}, events)
}

// gofast:scenario A4
// Rule: any member withdraws an approved improvement.
func TestWithdrawSkillImprovement_WithdrawsAnApprovedImprovement(t *testing.T) {
	// Given
	a := approvedFor(si.AllProjects)

	// When
	events, err := si.WithdrawSkillImprovement(a, "jordi", later)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []si.Event{si.SkillImprovementWithdrawn{ID: "si-1a2b", By: "jordi", At: later}}, events)
}

// gofast:scenario A4
// Rule: only an approved improvement is withdrawn.
func TestWithdrawSkillImprovement_RejectsWhenOnlyProposed(t *testing.T) {
	// Given
	p := proposed()

	// When
	_, err := si.WithdrawSkillImprovement(p, "jordi", later)

	// Then
	require.ErrorIs(t, err, si.ErrImprovementNotApproved)
}

// gofast:scenario A4
// Rule: withdrawing again records nothing.
func TestWithdrawSkillImprovement_RepeatedRecordsNothing(t *testing.T) {
	// Given
	w := withdrawn()

	// When
	events, err := si.WithdrawSkillImprovement(w, "ana", later.Add(time.Hour))

	// Then
	require.NoError(t, err)
	assert.Empty(t, events)
}

// gofast:scenario A4
// Rule: a withdrawn improvement is never approved again.
func TestApproveSkillImprovement_RejectsWhenWithdrawn(t *testing.T) {
	// Given
	w := withdrawn()

	// When
	_, err := si.ApproveSkillImprovement(w, si.ThisProject, 1, "ana", later.Add(time.Hour))

	// Then
	require.ErrorIs(t, err, si.ErrImprovementWithdrawn)
}

// gofast:scenario A4
// Rule: a withdrawn improvement's text stays fixed.
func TestReviseSkillImprovement_RejectsWhenWithdrawn(t *testing.T) {
	// Given
	w := withdrawn()

	// When
	_, err := si.ReviseSkillImprovement(w, "Something else.", later.Add(time.Hour))

	// Then
	require.ErrorIs(t, err, si.ErrImprovementTextFixed)
}
