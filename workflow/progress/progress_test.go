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
		view, err = progress.Read(log, Branch)
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
		Message:  "No active work on branch main. Start one with `gf start --type fix-bug \"<description>\"`.",
		Commands: []string{`gf start --type fix-bug "<description>"`},
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
		"review": progress.StagePending, "integration-testing": progress.StagePending,
	}, stageStatuses(v))
	assert.Equal(t, &progress.CurrentView{
		Stage: "implement", Visit: 1, Gate: "auto", Assignment: progress.AssignmentOpen, AttemptsUsed: 1, Budget: 3,
		Artifact:    ".gofast/works/w1/implement-v1.md",
		LastProblem: &progress.ProblemView{Kind: progress.ProblemFailedCheck, Attempt: 1, Text: "unit tests fail"},
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
	}, artifacts)
	assert.Equal(t, ".gofast/works/w1/review-v1.md", v.Current.Artifact)
}

func TestGivenWorkOnReview_WhenRenderingStatusText_ThenItShowsTheCurrentArtifact(t *testing.T) {
	v := whenQueried(t, eventlogtest.Given(t, OpenOn(domain.StageReview)...))
	var text strings.Builder

	progress.RenderText(&text, v)

	assert.Contains(t, text.String(), "Artifact: .gofast/works/w1/review-v1.md")
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
