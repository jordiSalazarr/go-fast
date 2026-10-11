package progress_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// whenQueried returns the progress view of the given history.
func whenQueried(t *testing.T, s *eventlogtest.Scenario) progress.View {
	t.Helper()
	var view progress.View
	s.WhenQueried(func(log *eventlog.Snapshot) error {
		var err error
		view, err = progress.Read(log, Branch, progress.Facts{})
		return err
	})
	return view
}

func stageStatuses(v progress.View) map[string]string {
	m := map[string]string{}
	for _, s := range v.Work.Stages {
		m[s.Stage] = s.Status
	}
	return m
}

func TestGivenNoWork_WhenQueryingStatus_ThenItSaysHowToStart(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t))

	assert.Equal(t, progress.View{Schema: 1, Next: progress.NextView{
		Actor:    progress.NextAnyone,
		Message:  "No active work on branch main. Start one with `gf start --type <fix-bug|new-feature|modify-behaviour> \"<description>\"`.",
		Commands: []string{`gf start --type <fix-bug|new-feature|modify-behaviour> "<description>"`},
	}, OtherBranches: []progress.OtherBranchView{}}, v)
}

func TestGivenCompletedWork_WhenQueryingStatus_ThenThereIsNoActiveWork(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, Completed()...))

	assert.Nil(t, v.Work)
	assert.Nil(t, v.Current)
}

func TestGivenAFailedAttemptOnImplement_WhenQueryingStatus_ThenTheAgentIsToldToWorkAndSubmit(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, OpenOn(domain.StageImplement, Failed(domain.StageImplement, 1, "unit tests fail"))...))

	require.NotNil(t, v.Work)
	assert.Equal(t, "w1", v.Work.ID)
	assert.Equal(t, "fix-bug", v.Work.Type)
	assert.Equal(t, "login button does nothing", v.Work.Description)
	assert.Equal(t, map[string]string{
		"discovery": progress.StageDone, "specify": progress.StageDone, "implement": progress.StageCurrent,
		"review": progress.StagePending, "integration-testing": progress.StagePending, "stress-testing": progress.StagePending,
	}, stageStatuses(v))
	assert.Equal(t, &progress.CurrentView{
		Stage: "implement", Visit: 1, Gate: "auto", Assignment: progress.AssignmentOpen, AttemptsUsed: 1, Budget: 3,
		Artifact:    ".gofast/works/w1/implement-v1.md",
		LastProblem: &progress.ProblemView{Kind: progress.ProblemFailedCheck, Attempt: 1, Text: "unit tests fail"},
		Checks:      []string{"artifact-written", "build-passes", "formatted", "specs-pass", "specs-untouched", "suite-passes"},
	}, v.Current)
	assert.Equal(t, progress.NextView{
		Actor:    progress.NextAgent,
		Message:  "Work on 'implement', then run `gf submit --passed` or `gf submit --failed \"<reason>\"`.",
		Commands: []string{"gf submit --passed", `gf submit --failed "<reason>"`},
	}, v.Next)
}

func TestGivenASubmissionAwaitingApproval_WhenQueryingStatus_ThenTheOwnerIsAskedToApproveOrReject(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, OpenOn(domain.StageSpecify, SubmittedForApproval(domain.StageSpecify, 1))...))

	assert.Equal(t, progress.AssignmentAwaitingApproval, v.Current.Assignment)
	assert.Equal(t, "human", v.Current.Gate)
	assert.Nil(t, v.Current.LastProblem)
	assert.Equal(t, progress.NextView{
		Actor:    progress.NextOwner,
		Message:  "Waiting for the owner to approve 'specify': run `gf approve` or `gf reject \"<feedback>\"`.",
		Commands: []string{"gf approve", `gf reject "<feedback>"`},
	}, v.Next)
}

func TestGivenARejection_WhenQueryingStatus_ThenItShowsTheFeedback(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, OpenOn(domain.StageDiscovery,
		Failed(domain.StageDiscovery, 1, "no repro"),
		SubmittedForApproval(domain.StageDiscovery, 2), Rejected(domain.StageDiscovery, 2, "add console output"))...))

	assert.Equal(t, progress.AssignmentOpen, v.Current.Assignment)
	assert.Equal(t, 2, v.Current.AttemptsUsed)
	assert.Equal(t, &progress.ProblemView{Kind: progress.ProblemRejection, Attempt: 2, Text: "add console output"}, v.Current.LastProblem)
}

func TestGivenAnEscalation_WhenQueryingStatus_ThenTheOwnerIsAskedToExtendOrAbandon(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, EscalatedOn(domain.StageReview)...))

	assert.Equal(t, progress.AssignmentEscalated, v.Current.Assignment)
	assert.Equal(t, 3, v.Current.AttemptsUsed)
	assert.Equal(t, 3, v.Current.Budget)
	assert.Equal(t, progress.NextOwner, v.Next.Actor)
	assert.Equal(t, "Escalated: budget exhausted on 'review'; the owner can run `gf extend <n>` or `gf abandon \"<reason>\"`.", v.Next.Message)
}

func TestGivenAnExtendedBudget_WhenQueryingStatus_ThenItShowsTheNewBudget(t *testing.T) {
	extended := domain.BudgetExtended{AssignmentID: AssignmentOn(domain.StageReview), Owner: Owner, Additional: Budget(2), NewBudget: Budget(5)}

	v := whenQueried(t, eventlogtest.Given(t, append(EscalatedOn(domain.StageReview), extended)...))

	assert.Equal(t, progress.AssignmentOpen, v.Current.Assignment)
	assert.Equal(t, 3, v.Current.AttemptsUsed)
	assert.Equal(t, 5, v.Current.Budget)
}

// A crash between entering a stage and opening its assignment: status is
// read-only, so it shows the assignment as open with the path's budget until
// the next command's automations open it.
func TestGivenAStageWhoseAssignmentIsNotOpenedYet_WhenQueryingStatus_ThenItIsShownOpen(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, WorkOn(domain.StageSpecify)...))

	assert.Equal(t, &progress.CurrentView{
		Stage: "specify", Visit: 1, Gate: "human", Assignment: progress.AssignmentOpen, AttemptsUsed: 0, Budget: 3,
		Artifact: ".gofast/works/w1/specify-v1.md",
		Checks:   []string{"artifact-written", "specs-fail"},
	}, v.Current)
	assert.Equal(t, progress.NextAgent, v.Next.Actor)
}

func TestGivenWorkOnReview_WhenQueryingStatus_ThenItShowsTheArtifactPaths(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, OpenOn(domain.StageReview)...))

	assert.Equal(t, ".gofast/works/w1/", v.Work.ArtifactDir)
	artifacts := map[string]string{}
	for _, s := range v.Work.Stages {
		artifacts[s.Stage] = s.Artifact
	}
	assert.Equal(t, map[string]string{
		"discovery":           ".gofast/works/w1/discovery-v1.md",
		"specify":             ".gofast/works/w1/specify-v1.md",
		"implement":           ".gofast/works/w1/implement-v1.md",
		"review":              "", // current: see current.artifact
		"integration-testing": "",
		"stress-testing":      "",
	}, artifacts)
	assert.Equal(t, ".gofast/works/w1/review-v1.md", v.Current.Artifact)
}

func TestGivenWorkOnReview_WhenRenderingStatusText_ThenItShowsTheCurrentArtifact(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, OpenOn(domain.StageReview)...))
	var text strings.Builder

	progress.RenderText(&text, v)

	assert.Contains(t, text.String(), "Artifact: .gofast/works/w1/review-v1.md")
	assert.Contains(t, text.String(), "Exit checks (run by `gf submit --passed`): artifact-written, formatted, build-passes, suite-passes\n")
}

func TestGivenWork_WhenQueryingStatus_ThenItShowsItsBranch(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, OpenOn(domain.StageDiscovery)...))

	assert.Equal(t, "main", v.Work.Branch)
	assert.Empty(t, v.OtherBranches)
}

func TestGivenWorkOnlyOnAnotherBranch_WhenQueryingStatus_ThenItListsIt(t *testing.T) {
	otherBranch := Must(domain.NewBranch("fix/double-charge"))
	orders := domain.WorkStarted{WorkID: WorkID, WorkType: domain.WorkTypeFixBug, Description: Must(domain.NewDescription("orders double-charge")), Branch: otherBranch}
	s := eventlogtest.Given(t, orders, StageEntered(domain.StageDiscovery), StageEntered(domain.StageSpecify))

	v := whenQueried(t, s)

	assert.Nil(t, v.Work)
	assert.Equal(t, []progress.OtherBranchView{{
		Branch: "fix/double-charge", WorkID: "w1", Type: "fix-bug", Description: "orders double-charge", Stage: "specify",
	}}, v.OtherBranches)
	var text strings.Builder
	progress.RenderText(&text, v)
	assert.Contains(t, text.String(), "No active work on branch main.")
	assert.Contains(t, text.String(), "fix-bug 'orders double-charge' is in progress on branch fix/double-charge.")
}

func TestGivenWorkInTheFixer_WhenQueryingStatus_ThenItShowsWhatHandedItOver(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, InFixer(domain.StageReview)...))

	require.NotNil(t, v.Current)
	assert.Equal(t, "fixer", v.Current.Stage)
	assert.Equal(t, ".gofast/works/w1/fixer-v1.md", v.Current.Artifact)
	assert.Equal(t, []string{"artifact-written"}, v.Current.Checks)
	assert.Equal(t, &progress.FixerView{
		From: "review", FromVisit: 1, Mode: "correctness", Reason: "suite fails",
		Artifact: ".gofast/works/w1/review-v1.md", CheckLog: ".gofast/runtime/checks/w1-review-1-attempt1.log",
		Targets: []string{"specify", "implement"},
	}, v.Current.Fixer)
	assert.Nil(t, v.Current.ReworkOf)
	assert.Equal(t, 1, v.Work.Reworks)
	assert.Equal(t, 3, v.Work.ReworkBudget)
	assert.Equal(t, map[string]string{
		"discovery": progress.StageDone, "specify": progress.StageDone, "implement": progress.StageDone,
		"review": progress.StagePending, "integration-testing": progress.StagePending, "stress-testing": progress.StagePending, "fixer": progress.StageCurrent,
	}, stageStatuses(v))
	assert.Equal(t, progress.NextAgent, v.Next.Actor)
	assert.Equal(t, []string{`gf submit --passed --route <specify|implement> "<reason>"`, `gf submit --failed "<reason>"`}, v.Next.Commands)

	var text strings.Builder
	progress.RenderText(&text, v)
	assert.Contains(t, text.String(), "Handed over by: review (visit 1), correctness: suite fails\n")
	assert.Contains(t, text.String(), "Its artifact: .gofast/works/w1/review-v1.md\n")
	assert.Contains(t, text.String(), "Reworks: 1 of 3\n")
}

func TestGivenAReworkBackToImplement_WhenQueryingStatus_ThenEveryLaterVisitSeesTheFixersArtifact(t *testing.T) {
	route := Route(domain.StageImplement, "the fix ignores negative ports")
	given := append(InFixer(domain.StageReview),
		domain.AssignmentAccepted{AssignmentID: AssignmentOn(domain.StageFixer), WorkID: WorkID, Stage: domain.StageFixer, Visit: Visit1, Route: route},
		domain.ReworkStarted{WorkID: WorkID, Target: domain.StageImplement, Visit: Visit(2), Reason: Reason("the fix ignores negative ports")},
		EnteredVisit(domain.StageImplement, 2), OpenedVisit(domain.StageImplement, 2))

	v := whenQueried(t, eventlogtest.Given(t, given...))

	assert.Equal(t, "implement", v.Current.Stage)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, ".gofast/works/w1/implement-v2.md", v.Current.Artifact)
	want := &progress.ReworkOfView{FixerArtifact: ".gofast/works/w1/fixer-v1.md", Reason: "the fix ignores negative ports", Target: "implement", TargetVisit: 2}
	assert.Equal(t, want, v.Current.ReworkOf)
	assert.Nil(t, v.Current.Fixer)

	v = whenQueried(t, eventlogtest.Given(t, append(given, Accepted(domain.StageImplement), EnteredVisit(domain.StageReview, 2))...))

	assert.Equal(t, "review", v.Current.Stage)
	assert.Equal(t, want, v.Current.ReworkOf, "later stages run again with the fixer's findings")
}

func TestGivenTheReworkLimit_WhenQueryingStatus_ThenTheOwnerIsAskedToExtendOrAbandon(t *testing.T) {
	given := append(ReworkedOnReview(3), OpenedVisit(domain.StageReview, 4), HandedToFixerOnVisit(domain.StageReview, 4, "suite fails"),
		domain.ReworkLimitReached{WorkID: WorkID, Reworks: Must(domain.NewReworkCount(3))})

	v := whenQueried(t, eventlogtest.Given(t, given...))

	assert.Equal(t, "review", v.Current.Stage)
	assert.Equal(t, progress.AssignmentHandedToFixer, v.Current.Assignment)
	assert.Equal(t, 3, v.Work.Reworks)
	assert.Equal(t, progress.NextOwner, v.Next.Actor)
	assert.Equal(t, []string{"gf rework-extend <n>", `gf abandon "<reason>"`}, v.Next.Commands)
	assert.Contains(t, v.Next.Message, "Rework limit reached")
	assert.Contains(t, v.Next.Message, "3 of 3 times")
}
