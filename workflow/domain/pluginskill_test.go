package domain_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
)

// version is the instruction version named n, as a hash.
func version(n string) domain.InstructionsVersion {
	sum := sha256.Sum256([]byte(n))
	return domain.HashOf(sum[:])
}

var (
	v0 = version("aggregate-design as shipped")
	v1 = version("aggregate-design with si-1")
	v2 = version("aggregate-design with si-1 and si-2")

	approvedAt = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
)

const (
	session domain.SessionID = "session-a"
	other   domain.SessionID = "session-b"
)

func approved(id domain.SkillImprovementID, skill string, minutes int) domain.ApprovedImprovementFact {
	return domain.ApprovedImprovementFact{ID: id, Skill: skill, Text: "improve " + skill, ApprovedAt: approvedAt.Add(time.Duration(minutes) * time.Minute)}
}

func entry(id domain.SkillImprovementID, outcome domain.ImprovementOutcome, before, after domain.InstructionsVersion) domain.RecordEntry {
	return domain.RecordEntry{Improvement: id, Outcome: outcome, Before: before, After: after, Session: session}
}

func idle(record ...domain.RecordEntry) domain.IdleSkill {
	return domain.IdleSkill{Skill: "aggregate-design", Record: record}
}

// improving is aggregate-design with si-1 in progress for session, from v0.
func improving(record ...domain.RecordEntry) domain.ImprovingSkill {
	return domain.ImprovingSkill{Skill: "aggregate-design", Record: record,
		InProgress: domain.RecordEntry{Improvement: "si-1", Outcome: domain.OutcomeInProgress, Before: v0, Session: session}}
}

// gofast:scenario A5
// gofast:scenario A7
// Rule: an approved improvement missing from the skill's record is started, from the skill's current version.
func TestStartSkillImprovement_StartsAnImprovementTheRecordDoesNotList(t *testing.T) {
	// Given: si-1's line was deleted by the owner, or never written
	skill := idle(entry("si-0", domain.OutcomeApplied, version("older"), v0))

	// When
	events, err := domain.StartSkillImprovement(skill, approved("si-1", "aggregate-design", 0), session, v0, false)

	// Then
	require.NoError(t, err)
	assert.Equal(t, []domain.Event{domain.SkillImprovementStarted{Improvement: "si-1", Skill: "aggregate-design", Session: session, Before: v0}}, events)
}

// gofast:scenario A6
// Rule: an improvement is recorded at most once in a skill's record.
func TestStartSkillImprovement_RejectsAnImprovementAlreadyApplied(t *testing.T) {
	// Given
	skill := idle(entry("si-1", domain.OutcomeApplied, v0, v1))

	// When
	_, err := domain.StartSkillImprovement(skill, approved("si-1", "aggregate-design", 0), other, v1, false)

	// Then
	require.ErrorIs(t, err, domain.ErrImprovementAlreadyRecorded)
}

// gofast:scenario A7
// Rule: a declined improvement stays recorded, so it is not applied again.
func TestStartSkillImprovement_RejectsADeclinedImprovement(t *testing.T) {
	// Given
	skill := idle(entry("si-1", domain.OutcomeDeclined, v0, v1))

	// When
	_, err := domain.StartSkillImprovement(skill, approved("si-1", "aggregate-design", 0), other, v0, false)

	// Then
	require.ErrorIs(t, err, domain.ErrImprovementAlreadyRecorded)
}

// gofast:scenario A9
// Rule: an improvement that could not be applied is not retried while its line stays.
func TestStartSkillImprovement_RejectsAnImprovementRecordedAsNotApplied(t *testing.T) {
	// Given
	skill := idle(domain.RecordEntry{Improvement: "si-1", Outcome: domain.OutcomeNotApplied, Before: v0, Session: session, Reason: "contradicts the skill"})

	// When
	_, err := domain.StartSkillImprovement(skill, approved("si-1", "aggregate-design", 0), other, v0, false)

	// Then
	require.ErrorIs(t, err, domain.ErrImprovementAlreadyRecorded)
}

// gofast:scenario A6
// Rule: a record line gf cannot read counts as recorded, so a hand edit never causes a second application.
func TestStartSkillImprovement_RejectsAnImprovementOnAnUnreadableLine(t *testing.T) {
	// Given
	skill := idle(domain.RecordEntry{Improvement: "si-1", Outcome: domain.OutcomeUnreadable})

	// When
	_, err := domain.StartSkillImprovement(skill, approved("si-1", "aggregate-design", 0), session, v0, false)

	// Then
	require.ErrorIs(t, err, domain.ErrImprovementAlreadyRecorded)
}

// gofast:scenario A5
// Rule: a skill has at most one improvement in progress, held by one session.
func TestStartSkillImprovement_RejectsWhileAnotherSessionHoldsTheSkill(t *testing.T) {
	// Given
	skill := improving()

	// When
	_, err := domain.StartSkillImprovement(skill, approved("si-1", "aggregate-design", 0), other, v0, false)

	// Then
	require.ErrorIs(t, err, domain.ErrSkillImprovementInProgress)
}

// gofast:scenario A11
// Rule: one session applies one improvement of a skill at a time.
func TestStartSkillImprovement_RejectsAnotherImprovementWhileOneIsInProgress(t *testing.T) {
	// Given
	skill := improving()

	// When
	_, err := domain.StartSkillImprovement(skill, approved("si-2", "aggregate-design", 1), session, v0, false)

	// Then
	require.ErrorIs(t, err, domain.ErrSkillImprovementInProgress)
}

// gofast:scenario A5
// Rule: the holding session starting its improvement again records nothing.
func TestStartSkillImprovement_RepeatedByTheHoldingSessionRecordsNothing(t *testing.T) {
	// Given
	skill := improving()

	// When
	events, err := domain.StartSkillImprovement(skill, approved("si-1", "aggregate-design", 0), session, v0, false)

	// Then
	require.NoError(t, err)
	assert.Empty(t, events)
}

// gofast:scenario A11
// Rule: only gofast's own skills are improved, never a vendored one.
func TestStartSkillImprovement_RejectsAVendoredSkill(t *testing.T) {
	// Given
	grilling := domain.IdleSkill{Skill: "grilling"}

	// When
	_, err := domain.StartSkillImprovement(grilling, approved("si-1", "grilling", 0), session, v0, true)

	// Then
	require.ErrorIs(t, err, domain.ErrSkillOutOfScope)
}

// gofast:scenario A5
// Rule: applying the improvement in progress records the versions before and after.
func TestImproveSkill_RecordsTheVersionsBeforeAndAfter(t *testing.T) {
	// Given
	skill := improving()

	// When
	events, err := domain.ImproveSkill(skill, "si-1", session, v1, "names the business intent")

	// Then
	require.NoError(t, err)
	assert.Equal(t, []domain.Event{domain.SkillImproved{Improvement: "si-1", Skill: "aggregate-design", Before: v0, After: v1, Summary: "names the business intent"}}, events)
}

// gofast:scenario A8
// Rule: an improvement the skill already holds is recorded as applied with no change.
func TestImproveSkill_RecordsANoChangeApplicationWhenTheSkillAlreadyHeldIt(t *testing.T) {
	// Given
	skill := improving()

	// When
	events, err := domain.ImproveSkill(skill, "si-1", session, v0, "")

	// Then
	require.NoError(t, err)
	assert.Equal(t, []domain.Event{domain.SkillImproved{Improvement: "si-1", Skill: "aggregate-design", Before: v0, After: v0}}, events)
}

// gofast:scenario A5
// Rule: an outcome is recorded only by the session holding the improvement.
func TestImproveSkill_RejectsAnotherSessionsImprovement(t *testing.T) {
	// Given
	skill := improving()

	// When
	_, err := domain.ImproveSkill(skill, "si-1", other, v1, "")

	// Then
	require.ErrorIs(t, err, domain.ErrImprovementNotInProgress)
}

// gofast:scenario A5
// Rule: an outcome is recorded only for the improvement in progress.
func TestImproveSkill_RejectsAnImprovementNotInProgress(t *testing.T) {
	// Given
	skill := idle()

	// When
	_, err := domain.ImproveSkill(skill, "si-1", session, v1, "")

	// Then
	require.ErrorIs(t, err, domain.ErrImprovementNotInProgress)
}

// gofast:scenario A5
// Rule: the holding session repeating the outcome it recorded records nothing.
func TestImproveSkill_RepeatedAfterItsOutcomeIsRecordedRecordsNothing(t *testing.T) {
	// Given
	skill := idle(entry("si-1", domain.OutcomeApplied, v0, v1))

	// When
	events, err := domain.ImproveSkill(skill, "si-1", session, v1, "")

	// Then
	require.NoError(t, err)
	assert.Empty(t, events)
}

// gofast:scenario A9
// Rule: an improvement that contradicts the skill is recorded as not applied, with the reason, and the skill is unchanged.
func TestReportImprovementNotApplied_RecordsAConflictWithItsReason(t *testing.T) {
	// Given
	skill := improving()

	// When
	events, err := domain.ReportImprovementNotApplied(skill, "si-1", session, domain.CauseConflict, Reason("the skill now says the opposite"))

	// Then
	require.NoError(t, err)
	assert.Equal(t, []domain.Event{domain.SkillImprovementNotApplied{Improvement: "si-1", Skill: "aggregate-design", Version: v0,
		Cause: domain.CauseConflict, Reason: Reason("the skill now says the opposite")}}, events)
}

// gofast:scenario A10
// Rule: an improvement asking for anything but an edit to the skill is refused and recorded as not applied.
func TestReportImprovementNotApplied_RecordsARefusal(t *testing.T) {
	// Given
	skill := improving()

	// When
	events, err := domain.ReportImprovementNotApplied(skill, "si-1", session, domain.CauseRefused, Reason("asks to run a command"))

	// Then
	require.NoError(t, err)
	assert.Equal(t, []domain.Event{domain.SkillImprovementNotApplied{Improvement: "si-1", Skill: "aggregate-design", Version: v0,
		Cause: domain.CauseRefused, Reason: Reason("asks to run a command")}}, events)
}

// gofast:scenario A9
// Rule: another session can't record an outcome for the improvement in progress.
func TestReportImprovementNotApplied_RejectsAnotherSessionsImprovement(t *testing.T) {
	// Given
	skill := improving()

	// When
	_, err := domain.ReportImprovementNotApplied(skill, "si-1", other, domain.CauseConflict, Reason("conflict"))

	// Then
	require.ErrorIs(t, err, domain.ErrImprovementNotInProgress)
}

// gofast:scenario A7
// Rule: an applied improvement that changed the skill counts as declined once the skill is back at its before-version.
func TestDeclineSkillImprovement_DeclinesAnAppliedImprovementTheOwnerReverted(t *testing.T) {
	// Given
	skill := idle(entry("si-1", domain.OutcomeApplied, v0, v1))

	// When
	events := domain.DeclineSkillImprovement(skill, v0)

	// Then
	assert.Equal(t, []domain.Event{domain.SkillImprovementDeclined{Improvement: "si-1", Skill: "aggregate-design", Version: v0}}, events)
}

// gofast:scenario A7
// Rule: reverting the latest improvement declines only that one.
func TestDeclineSkillImprovement_RevertingTheLatestDeclinesOnlyIt(t *testing.T) {
	// Given
	skill := idle(entry("si-1", domain.OutcomeApplied, v0, v1), entry("si-2", domain.OutcomeApplied, v1, v2))

	// When
	events := domain.DeclineSkillImprovement(skill, v1)

	// Then
	assert.Equal(t, []domain.Event{domain.SkillImprovementDeclined{Improvement: "si-2", Skill: "aggregate-design", Version: v1}}, events)
}

// gofast:scenario A7
// Rule: reverting to before the first improvement declines every later one.
func TestDeclineSkillImprovement_RevertingToBeforeTheFirstDeclinesAll(t *testing.T) {
	// Given
	skill := idle(entry("si-1", domain.OutcomeApplied, v0, v1), entry("si-2", domain.OutcomeApplied, v1, v2))

	// When
	events := domain.DeclineSkillImprovement(skill, v0)

	// Then
	assert.ElementsMatch(t, []domain.Event{
		domain.SkillImprovementDeclined{Improvement: "si-1", Skill: "aggregate-design", Version: v0},
		domain.SkillImprovementDeclined{Improvement: "si-2", Skill: "aggregate-design", Version: v0},
	}, events)
}

// gofast:scenario A8
// Rule: an application that changed nothing is never declined.
func TestDeclineSkillImprovement_NeverDeclinesANoChangeApplication(t *testing.T) {
	// Given: si-2 was already in the skill; si-1 changed it
	skill := idle(entry("si-1", domain.OutcomeApplied, v0, v1), entry("si-2", domain.OutcomeApplied, v1, v1))

	// When
	events := domain.DeclineSkillImprovement(skill, v0)

	// Then
	assert.Equal(t, []domain.Event{domain.SkillImprovementDeclined{Improvement: "si-1", Skill: "aggregate-design", Version: v0}}, events)
}

// gofast:scenario A7
// Rule: a decline is recorded once: the entry becomes declined.
func TestDeclineSkillImprovement_AnEntryDeclinedIsNotDeclinedAgain(t *testing.T) {
	// Given
	skill := idle(entry("si-1", domain.OutcomeApplied, v0, v1))
	declined := domain.EvolvePluginSkill(skill, domain.SkillImprovementDeclined{Improvement: "si-1", Skill: "aggregate-design", Version: v0})

	// When
	events := domain.DeclineSkillImprovement(declined, v0)

	// Then
	assert.Equal(t, idle(domain.RecordEntry{Improvement: "si-1", Outcome: domain.OutcomeDeclined, Before: v0, After: v1, Session: session}), declined)
	assert.Empty(t, events)
}

// gofast:scenario A5
// Rule: applying the improvement in progress records it as applied in the record, and the skill is idle again.
func TestEvolvePluginSkill_AnAppliedImprovementIsRecordedAndTheSkillIsIdle(t *testing.T) {
	// Given
	skill := improving()

	// When
	next := domain.EvolvePluginSkill(skill, domain.SkillImproved{Improvement: "si-1", Skill: "aggregate-design", Before: v0, After: v1})

	// Then
	assert.Equal(t, idle(domain.RecordEntry{Improvement: "si-1", Outcome: domain.OutcomeApplied, Before: v0, After: v1, Session: session}), next)
}

// gofast:scenario A11
// Rule: pending improvements are grouped by skill, skills by their earliest approval, each skill's in approval order.
func TestPendingSkillImprovements_GroupsBySkillInApprovalOrder(t *testing.T) {
	// Given
	fetched := []domain.ApprovedImprovementFact{
		approved("si-a", "code-layout", 0),
		approved("si-b", "aggregate-design", 1),
		approved("si-c", "code-layout", 2),
		approved("si-d", "aggregate-design", 3),
	}
	local := []domain.LocalSkill{{Name: "aggregate-design"}, {Name: "code-layout"}}

	// When
	pending := domain.PendingSkillImprovements(fetched, local)

	// Then
	assert.Equal(t, []domain.PendingSkill{
		{Skill: "code-layout", Improvements: []domain.ApprovedImprovementFact{fetched[0], fetched[2]}},
		{Skill: "aggregate-design", Improvements: []domain.ApprovedImprovementFact{fetched[1], fetched[3]}},
	}, pending)
}

// gofast:scenario A6
// gofast:scenario A7
// gofast:scenario A9
// Rule: an improvement the skill's record lists, whatever its outcome, is not pending.
func TestPendingSkillImprovements_SkipsWhatTheRecordLists(t *testing.T) {
	// Given
	fetched := []domain.ApprovedImprovementFact{
		approved("si-1", "aggregate-design", 0),
		approved("si-2", "aggregate-design", 1),
		approved("si-3", "aggregate-design", 2),
		approved("si-4", "aggregate-design", 3),
		approved("si-5", "aggregate-design", 4),
	}
	local := []domain.LocalSkill{{Name: "aggregate-design", Record: []domain.RecordEntry{
		entry("si-1", domain.OutcomeApplied, v0, v1),
		entry("si-2", domain.OutcomeDeclined, v1, v2),
		entry("si-3", domain.OutcomeNotApplied, v0, domain.InstructionsVersion{}),
		{Improvement: "si-4", Outcome: domain.OutcomeUnreadable},
	}}}

	// When
	pending := domain.PendingSkillImprovements(fetched, local)

	// Then
	assert.Equal(t, []domain.PendingSkill{{Skill: "aggregate-design", Improvements: []domain.ApprovedImprovementFact{fetched[4]}}}, pending)
}

// gofast:scenario A11
// Rule: improvements of vendored skills, or of skills the plugin directory lacks, are skipped.
func TestPendingSkillImprovements_SkipsVendoredAndMissingSkills(t *testing.T) {
	// Given
	fetched := []domain.ApprovedImprovementFact{
		approved("si-1", "grilling", 0),
		approved("si-2", "postgres", 1),
		approved("si-3", "aggregate-design", 2),
	}
	local := []domain.LocalSkill{{Name: "grilling", Vendored: true}, {Name: "aggregate-design"}}

	// When
	pending := domain.PendingSkillImprovements(fetched, local)

	// Then
	assert.Equal(t, []domain.PendingSkill{{Skill: "aggregate-design", Improvements: []domain.ApprovedImprovementFact{fetched[2]}}}, pending)
}
