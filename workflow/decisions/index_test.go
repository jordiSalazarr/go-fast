package decisions_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/decisions"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

var (
	at        = time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	modelSpec = Must(domain.NewWorkInput(domain.InputEventModel, Must(domain.ParseRepoPath("docs/board.pdf")), domain.HashOf([]byte("%PDF"))))
	docPath   = Must(domain.ParseRepoPath("docs/decisions/2026-10-12-w1-wallets.md"))
	owner     = eventlog.OwnerActor(Owner)
)

// feature is new-feature work w1 started from an event model, with its
// stages up to stage entered and the visits before it accepted.
func feature(stages ...domain.Stage) []domain.Event {
	path := Must(domain.PathFor(domain.WorkTypeNewFeature))
	events := []domain.Event{domain.WorkStarted{WorkID: WorkID, WorkType: domain.WorkTypeNewFeature, PathVersion: domain.PathV2,
		Description: Must(domain.NewDescription("Wallets")), Branch: Branch, Inputs: []domain.WorkInput{modelSpec}}}
	for _, stage := range stages {
		for _, s := range path.Steps() {
			if s.Stage() == stage {
				events = append(events, domain.StageEntered{WorkID: WorkID, Stage: stage, Visit: Visit1, Gate: s.Gate(), Budget: s.Budget()})
			}
		}
	}
	return events
}

func opened(stage domain.Stage) domain.AssignmentOpened {
	for _, s := range Must(domain.PathFor(domain.WorkTypeNewFeature)).Steps() {
		if s.Stage() == stage {
			return domain.AssignmentOpened{AssignmentID: AssignmentOn(stage), WorkID: WorkID, Stage: stage, Visit: Visit1, Gate: s.Gate(), Budget: s.Budget()}
		}
	}
	panic(stage.String())
}

func accepted(stage domain.Stage) domain.AssignmentAccepted {
	return domain.AssignmentAccepted{AssignmentID: AssignmentOn(stage), WorkID: WorkID, Stage: stage, Visit: Visit1}
}

// history appends the work's events and its assignments' streams.
func history(t *testing.T, work []domain.Event, assignments ...[]domain.Event) eventlog.History {
	t.Helper()
	store := Must(eventlog.Open(t.TempDir(), eventlog.WithClock(func() time.Time { return at })))
	var h eventlog.History
	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(WorkID), 0, owner, work...))
		for _, a := range assignments {
			id := a[0].(domain.AssignmentOpened).AssignmentID
			require.NoError(t, s.Append(eventlog.AssignmentStream(id), 0, owner, a...))
		}
		h = eventlog.NewHistory(Must(s.ReadAll()))
		return nil
	}))
	return h
}

func readGrill(p domain.RepoPath) (domain.Grill, error) {
	if p == domain.GrillFor(WorkID, Visit1) {
		return domain.NewGrill(domain.GrillValues{DocPath: docPath}), nil
	}
	return domain.Grill{}, errors.New("no such grill")
}

// onDiscovery: event modelling and grill accepted, discovery entered but
// not opened yet.
func onDiscovery(t *testing.T) eventlog.History {
	return history(t, feature(domain.StageEventModelling, domain.StageGrill, domain.StageDiscovery),
		[]domain.Event{opened(domain.StageEventModelling), accepted(domain.StageEventModelling)},
		[]domain.Event{opened(domain.StageGrill), SubmittedForApprovalOf(domain.StageGrill), accepted(domain.StageGrill)})
}

func SubmittedForApprovalOf(stage domain.Stage) domain.SubmittedForApproval {
	return domain.SubmittedForApproval{AssignmentID: AssignmentOn(stage), Attempt: Attempt(1)}
}

func TestRows_AreTheWorksWithAnAcceptedGrill(t *testing.T) {
	rows, err := decisions.Rows(onDiscovery(t), readGrill)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, []string{"2026-10-12", "Wallets", "new-feature", "w1", "main", decisions.InProgress},
		[]string{r.Date, r.Title, r.WorkType, r.Work, r.Branch, r.Status})
	assert.Equal(t, docPath, r.Document)
	assert.Equal(t, domain.EventModelFor(WorkID, Visit1), r.EventModel)

	onGrill := history(t, feature(domain.StageEventModelling, domain.StageGrill),
		[]domain.Event{opened(domain.StageEventModelling), accepted(domain.StageEventModelling)},
		[]domain.Event{opened(domain.StageGrill), SubmittedForApprovalOf(domain.StageGrill)})
	rows, err = decisions.Rows(onGrill, readGrill)
	require.NoError(t, err)
	assert.Empty(t, rows, "grill awaits approval")
}

func TestRender_LinksRelativeToTheDirectory(t *testing.T) {
	rows, err := decisions.Rows(onDiscovery(t), readGrill)
	require.NoError(t, err)
	assert.Equal(t, decisions.Header+"# Decisions\n\nThe plan of every work whose grill the owner approved.\n\n"+
		"| Date | Title | Work type | Work | Branch | Status | Decision document | Event model |\n"+
		"|---|---|---|---|---|---|---|---|\n"+
		"| 2026-10-12 | Wallets | new-feature | w1 | main | in progress | [2026-10-12-w1-wallets.md](2026-10-12-w1-wallets.md) | [event-model-v1.json](../../.gofast/works/w1/event-model-v1.json) |\n",
		decisions.Render(domain.DefaultDecisionsDir(), rows))
}

// The index changes only between stages: never while the branch's work has
// a stage visit in progress.
func TestAtBoundary(t *testing.T) {
	between, err := decisions.AtBoundary(onDiscovery(t), Branch)
	require.NoError(t, err)
	assert.True(t, between, "discovery not opened yet")

	open := history(t, feature(domain.StageEventModelling, domain.StageGrill, domain.StageDiscovery),
		[]domain.Event{opened(domain.StageEventModelling), accepted(domain.StageEventModelling)},
		[]domain.Event{opened(domain.StageGrill), SubmittedForApprovalOf(domain.StageGrill), accepted(domain.StageGrill)},
		[]domain.Event{opened(domain.StageDiscovery)})
	between, err = decisions.AtBoundary(open, Branch)
	require.NoError(t, err)
	assert.False(t, between, "discovery in progress")

	between, err = decisions.AtBoundary(open, Must(domain.NewBranch("other")))
	require.NoError(t, err)
	assert.True(t, between, "no work on the branch")
}

func TestRefresh_WritesOnlyBetweenStages_AndOnlyWhatChanged(t *testing.T) {
	root := t.TempDir()
	dir := domain.DefaultDecisionsDir()
	index := filepath.Join(root, "docs", "decisions", "README.md")

	// Nothing accepted yet: no index.
	onGrill := history(t, feature(domain.StageEventModelling, domain.StageGrill),
		[]domain.Event{opened(domain.StageEventModelling), accepted(domain.StageEventModelling)})
	wrote, err := decisions.Refresh(root, dir, onGrill, Branch, readGrill)
	require.NoError(t, err)
	assert.False(t, wrote)
	_, err = os.Stat(index)
	assert.ErrorIs(t, err, os.ErrNotExist)

	// While discovery is in progress, the update waits for the next
	// boundary.
	open := history(t, feature(domain.StageEventModelling, domain.StageGrill, domain.StageDiscovery),
		[]domain.Event{opened(domain.StageEventModelling), accepted(domain.StageEventModelling)},
		[]domain.Event{opened(domain.StageGrill), SubmittedForApprovalOf(domain.StageGrill), accepted(domain.StageGrill)},
		[]domain.Event{opened(domain.StageDiscovery)})
	wrote, err = decisions.Refresh(root, dir, open, Branch, readGrill)
	require.NoError(t, err)
	assert.False(t, wrote, "deferred")
	_, err = os.Stat(index)
	assert.ErrorIs(t, err, os.ErrNotExist)

	wrote, err = decisions.Refresh(root, dir, onDiscovery(t), Branch, readGrill)
	require.NoError(t, err)
	assert.True(t, wrote)
	content := string(Must(os.ReadFile(index)))
	assert.Contains(t, content, "| w1 | main | in progress |")

	wrote, err = decisions.Refresh(root, dir, onDiscovery(t), Branch, readGrill)
	require.NoError(t, err)
	assert.False(t, wrote, "unchanged")

	// Abandoned during discovery: the work is no longer active, so the
	// index is between stages.
	work := append(feature(domain.StageEventModelling, domain.StageGrill, domain.StageDiscovery),
		domain.WorkAbandoned{WorkID: WorkID, Owner: Owner, Reason: Reason("wrong idea")})
	abandoned := history(t, work,
		[]domain.Event{opened(domain.StageEventModelling), accepted(domain.StageEventModelling)},
		[]domain.Event{opened(domain.StageGrill), SubmittedForApprovalOf(domain.StageGrill), accepted(domain.StageGrill)},
		[]domain.Event{opened(domain.StageDiscovery), domain.AssignmentCancelled{AssignmentID: AssignmentOn(domain.StageDiscovery), Reason: Reason("work abandoned")}})
	wrote, err = decisions.Refresh(root, dir, abandoned, Branch, readGrill)
	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Contains(t, string(Must(os.ReadFile(index))), "| w1 | main | abandoned |")
}
