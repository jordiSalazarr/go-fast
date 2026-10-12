// Package progress is the read model of where work stands on a branch and
// what happens next. It builds the view from the event log and renders it as
// the text and JSON `gf status` prints; the SessionStart brief uses the same
// text.
package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// Reader is what the read model needs from the event log.
type Reader interface {
	ReadAll() ([]eventlog.Recorded, error)
}

// Facts are what the view needs beyond the event log. A nil func leaves its
// part of the view out.
type Facts struct {
	// Stress returns the repository's NFRs and the files the work changed
	// since it started.
	Stress func(work domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error)
	// StressResults returns the NFR results of a stage visit's latest
	// attempt that measured any.
	StressResults func(id domain.AssignmentID) (attempt int, results []exitchecks.StressResult, ok bool, err error)
	// NFRProposals returns what adopting a design visit's NFR proposals
	// would change; ok is false when it proposes none.
	NFRProposals func(proposals domain.RepoPath) (adoption domain.NFRAdoption, ok bool, err error)
	// Design reads a structured design, checked against the work's event
	// model (nil when it has none); an *exitchecks.DesignError says what is
	// wrong with it.
	Design func(path domain.RepoPath, eventModel *domain.EventModel) (domain.Design, error)
	// DecisionsDir is the repository's decisions directory.
	DecisionsDir func() (domain.DecisionsDir, error)
	// Grill reads a grill visit's structured outcome; an
	// *exitchecks.GrillError says why it can't.
	Grill func(path domain.RepoPath) (domain.Grill, error)
	// EventModel reads a canonical event model; an
	// *exitchecks.EventModelError says what is wrong with it.
	EventModel func(path domain.RepoPath) (domain.EventModel, error)
	// WrittenSpecs are the tests a stage visit wrote so far and the
	// scenario markers above them; ok is false before it has a baseline.
	WrittenSpecs func(id domain.AssignmentID) (tests []string, markers map[string][]string, ok bool, err error)
	// File reads a file of the working tree; ok is false when it doesn't
	// exist.
	File func(path domain.RepoPath) (content []byte, ok bool, err error)
}

// View is the progress document. Its JSON form is consumed by hooks: field
// names and values are part of gf's interface; change them only with a new
// schema number.
type View struct {
	Schema  int          `json:"schema"`
	Work    *WorkView    `json:"work"`    // null when the current branch has no active work
	Current *CurrentView `json:"current"` // null when the current branch has no active work
	Next    NextView     `json:"next"`
	// OtherBranches lists work in progress on other branches; never null.
	OtherBranches []OtherBranchView `json:"otherBranches"`
	// LogWarning is set when the event log was changed outside gf.
	LogWarning string `json:"logWarning,omitempty"`
}

// LogChangedWarning is the view's LogWarning for an event log changed
// outside gf since gf last wrote it.
const LogChangedWarning = "The event log was changed outside gf since its last write. Inspect .gofast/events.jsonl (git diff) and run `gf log accept` from your own terminal if the change is legitimate. Until then, gf refuses to change the workflow."

// WarnIfChanged sets the view's LogWarning when the snapshot it was read
// from is of a log changed outside gf.
func WarnIfChanged(v *View, s *eventlog.Snapshot) {
	if s.ChangedOutsideGf() {
		v.LogWarning = LogChangedWarning
	}
}

// OtherBranchView is work in progress on another branch.
type OtherBranchView struct {
	Branch      string `json:"branch"`
	WorkID      string `json:"workId"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Stage       string `json:"stage"`
}

type WorkView struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Branch      string `json:"branch"`
	ArtifactDir string `json:"artifactDir"` // relative to the repository root, ends in "/"
	// DecisionsDir is where grill writes the work's decision document,
	// relative to the repository root; absent on paths without grill.
	DecisionsDir string `json:"decisionsDir,omitempty"`
	// Inputs are the files the owner started the work from; never null.
	Inputs []InputView `json:"inputs"`
	// TargetNFRs are the NFRs improve-performance work must meet; absent on
	// other work.
	TargetNFRs []string    `json:"targetNfrs,omitempty"`
	Stages     []StageView `json:"stages"`
	// Reworks is how many times the work entered the fixer, of ReworkBudget.
	Reworks      int `json:"reworks"`
	ReworkBudget int `json:"reworkBudget"`
	// Design is the latest accepted design, on a path with design; absent
	// before one is accepted, and on paths without design.
	Design *DesignView `json:"design,omitempty"`
	// EventModel says whether the work enters event modelling and, once a
	// visit is accepted, where its canonical model is; absent on paths
	// without event modelling.
	EventModel *EventModelView `json:"eventModel,omitempty"`
	// Grill is the latest grill visit's outcome: its decision document and
	// what it settled; the latest accepted one's until a later visit writes
	// its own; absent before grill writes one.
	Grill *GrillView `json:"grill,omitempty"`
	// NFRProposals are the NFRs the latest design visit proposes that
	// .gofast/nfrs.json doesn't contain yet; absent when there are none.
	NFRProposals *NFRProposalsView `json:"nfrProposals,omitempty"`
}

// EventModelView is the work's event modelling.
type EventModelView struct {
	// Included is set when the work started from an event model, so it
	// enters event modelling; otherwise the stage is skipped.
	Included bool `json:"included"`
	// The latest accepted visit and its files; absent before one.
	Visit     int    `json:"visit,omitempty"`
	File      string `json:"file,omitempty"`
	Artifact  string `json:"artifact,omitempty"`
	Scenarios int    `json:"scenarios,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

// GrillView is a grill visit's outcome.
type GrillView struct {
	Visit int    `json:"visit"`
	File  string `json:"file"` // the structured outcome
	// DocPath is the decision document, the plan the owner approves.
	DocPath             string `json:"docPath,omitempty"`
	Decisions           int    `json:"decisions"`
	AcceptanceScenarios int    `json:"acceptanceScenarios"`
	// Accepted is set once the owner approved this visit.
	Accepted bool `json:"accepted"`
	// Problem says why gf couldn't read the outcome.
	Problem string `json:"problem,omitempty"`
}

// InputView is a file the owner started the work from.
type InputView struct {
	Kind   string `json:"kind"` // spec or event-model
	Path   string `json:"path"` // relative to the repository root
	SHA256 string `json:"sha256"`
	// Changed is set when the file's content is not what it was when the
	// work started, or the file is gone.
	Changed bool `json:"changed,omitempty"`
	// Problem says why gf couldn't read the file.
	Problem string `json:"problem,omitempty"`
}

// NFRProposalsView is NFR proposals pending adoption by the owner.
type NFRProposalsView struct {
	File     string   `json:"file"`
	Added    []string `json:"added"`    // ids of new NFRs; never null
	Replaced []string `json:"replaced"` // ids of NFRs they change; never null
}

// DesignView is the work's latest accepted design: its files, relative to
// the repository root, and what it holds.
type DesignView struct {
	Visit      int    `json:"visit"`
	Artifact   string `json:"artifact"` // the markdown design
	File       string `json:"file"`     // the structured design
	Aggregates int    `json:"aggregates"`
	Commands   int    `json:"commands"`
	// Placements counts the event model's commands the design places in
	// aggregates, on work with an event model.
	Placements int `json:"placements,omitempty"`
	// Slices counts the slices by change: added, changed, removed,
	// unchanged; never null.
	Slices map[string]int `json:"slices"`
	// Problem says why the structured design couldn't be read.
	Problem string `json:"problem,omitempty"`
}

// Stage status values.
const (
	StageDone    = "done"
	StageCurrent = "current"
	StagePending = "pending"
	// StageSkipped: an optional stage the work decided not to include.
	StageSkipped = "skipped"
)

type StageView struct {
	Stage  string `json:"stage"`
	Gate   string `json:"gate"`
	Budget int    `json:"budget"`
	Status string `json:"status"`
	// Optional is set for a stage entered only if the work includes it.
	Optional bool `json:"optional,omitempty"`
	// Artifact is set for done stages: the artifact of their last visit.
	Artifact string `json:"artifact,omitempty"`
}

// Assignment state values.
const (
	AssignmentOpen             = "open"
	AssignmentAwaitingApproval = "awaiting-approval"
	AssignmentEscalated        = "escalated"
	AssignmentHandedToFixer    = "handed-to-fixer"
)

type CurrentView struct {
	Stage        string       `json:"stage"`
	Visit        int          `json:"visit"`
	Gate         string       `json:"gate"`
	Assignment   string       `json:"assignment"`
	AttemptsUsed int          `json:"attemptsUsed"`
	Budget       int          `json:"budget"`
	Artifact     string       `json:"artifact"`    // the artifact this stage visit writes
	LastProblem  *ProblemView `json:"lastProblem"` // null when there is none
	// Checks are the exit checks `gf submit --passed` runs for this stage,
	// in order; never null.
	Checks []string `json:"checks"`
	// Fixer is set while the work is in the fixer: what handed it over.
	Fixer *FixerView `json:"fixer"`
	// ReworkOf is set after the fixer returned the work to an earlier stage:
	// every later stage visit reads the fixer's artifact first.
	ReworkOf *ReworkOfView `json:"reworkOf"`
	// Returned is set after the owner sent the work back to an earlier
	// stage with `gf return`: every later stage visit reads the reason.
	Returned *ReturnedView `json:"returned,omitempty"`
	// Stress is set on review, integration testing and stress testing.
	Stress *StressView `json:"stress,omitempty"`
	// DesignProblems are what is wrong with this design visit's structured
	// design, after an attempt failed; design only.
	DesignProblems []string `json:"designProblems,omitempty"`
	// EventModelProblems are what is wrong with this event-modelling
	// visit's canonical event model, after an attempt failed;
	// event-modelling only.
	EventModelProblems []string `json:"eventModelProblems,omitempty"`
	// ScenariosToCover are the scenarios specify covers, after grill: the
	// accepted event model's of the slices it adds or changes, or grill's
	// acceptance scenarios. UncoveredScenarios are those no test written
	// in this visit marks yet; specs-cover-scenarios also requires a marked
	// test to fail, which only its run tells. Specify on path v2 only.
	ScenariosToCover   []string `json:"scenariosToCover,omitempty"`
	UncoveredScenarios []string `json:"uncoveredScenarios,omitempty"`
	// SlicesNeedingSpecs are the slices of the latest accepted design that
	// specify must cover with Test<SliceName> specs; specify on paths with
	// design only.
	SlicesNeedingSpecs []string `json:"slicesNeedingSpecs,omitempty"`
}

// StressView is what the current stage needs to know about stress testing.
type StressView struct {
	// Mandatory are the NFRs the work targets and those covering what it
	// changed: they make stress testing mandatory, and each needs a script;
	// never null.
	Mandatory []MandatoryNFRView `json:"mandatory"`
	// Decision is this pass's stress decision; null until one is recorded.
	Decision *StressDecisionView `json:"decision"`
	// Results are the latest stress run of this stage visit (stress testing
	// only); null before one measured anything.
	Results *StressResultsView `json:"results"`
	// Problem says why the NFRs or the work's changes couldn't be read.
	Problem string `json:"problem,omitempty"`
}

type MandatoryNFRView struct {
	ID        string   `json:"id"`
	Areas     []string `json:"areas"`
	Operation string   `json:"operation"`
	Load      string   `json:"load"`
	Targets   string   `json:"targets"`
	// Script is where the stress-testing agent writes its k6 script.
	Script string `json:"script"`
}

// Who decided stress testing.
const (
	DecidedByReview = "review"
	DecidedByOwner  = "owner"
)

type StressDecisionView struct {
	Decision  string   `json:"decision"` // include or skip
	Reason    string   `json:"reason"`
	ForcedBy  []string `json:"forcedBy"` // never null
	DecidedBy string   `json:"decidedBy"`
}

type StressResultsView struct {
	Attempt int    `json:"attempt"`
	RunDir  string `json:"runDir"`
	// NFRs are each scripted NFR's measurement against its targets.
	NFRs []exitchecks.StressResult `json:"nfrs"`
}

// FixerView is the stage visit that handed the work to the fixer.
type FixerView struct {
	From      string `json:"from"`
	FromVisit int    `json:"fromVisit"`
	Mode      string `json:"mode"`
	Reason    string `json:"reason"`
	// Artifact is the failing stage visit's artifact.
	Artifact string `json:"artifact"`
	// CheckLog is the full output of the failing attempt's exit checks. It
	// exists only if they ran: not when the agent itself claimed the failure.
	CheckLog string `json:"checkLog"`
	// Targets are the stages the fixer may return the work to, in path order.
	Targets []string `json:"targets"`
	// StressRunDir holds the failing stress run's raw k6 output and logs,
	// when stress testing handed over after measuring.
	StressRunDir string `json:"stressRunDir,omitempty"`
}

// ReworkOfView is the fixer visit that sent the work back.
type ReworkOfView struct {
	FixerArtifact string `json:"fixerArtifact"`
	Reason        string `json:"reason"`
	Target        string `json:"target"`
	TargetVisit   int    `json:"targetVisit"`
}

// ReturnedView is the owner sending the work back to an earlier stage.
type ReturnedView struct {
	From        string `json:"from"`
	FromVisit   int    `json:"fromVisit"`
	Target      string `json:"target"`
	TargetVisit int    `json:"targetVisit"`
	Reason      string `json:"reason"`
}

// Problem kinds.
const (
	ProblemFailedCheck = "failed-check"
	ProblemRejection   = "rejection"
	// ProblemCodeFailure: the stage failed on the code and was handed to the fixer.
	ProblemCodeFailure = "code-failure"
)

type ProblemView struct {
	Kind    string `json:"kind"`
	Attempt int    `json:"attempt"`
	Text    string `json:"text"`
}

// Who acts next.
const (
	NextAgent  = "agent"
	NextOwner  = "owner"
	NextAnyone = "anyone"
)

type NextView struct {
	Actor    string   `json:"actor"`
	Message  string   `json:"message"`
	Commands []string `json:"commands"`
}

// FactsAt reads the facts from the repository at root: its NFRs, what the
// work changed since its first stage visit's baseline, and the stress runs'
// results.
func FactsAt(root string) Facts {
	return Facts{
		Stress: func(work domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error) {
			nfrs, err := exitchecks.LoadNFRs(root)
			if err != nil {
				return nil, domain.ChangedFiles{}, err
			}
			changed, err := worktree.OpenBaselines(root).WorkChanges(work.FirstAssignment())
			return nfrs, changed, err
		},
		StressResults: func(id domain.AssignmentID) (int, []exitchecks.StressResult, bool, error) {
			return exitchecks.ReadStressResults(root, id)
		},
		NFRProposals: func(proposals domain.RepoPath) (domain.NFRAdoption, bool, error) {
			plan, err := exitchecks.PlanNFRAdoption(root, proposals)
			if errors.Is(err, exitchecks.ErrNoNFRProposals) {
				return domain.NFRAdoption{}, false, nil
			}
			return plan.Adoption, err == nil, err
		},
		Design: func(path domain.RepoPath, eventModel *domain.EventModel) (domain.Design, error) {
			return exitchecks.LoadDesign(root, path, eventModel)
		},
		EventModel: func(path domain.RepoPath) (domain.EventModel, error) {
			return exitchecks.LoadEventModel(root, path)
		},
		Grill: func(path domain.RepoPath) (domain.Grill, error) { return exitchecks.LoadGrill(root, path) },
		WrittenSpecs: func(id domain.AssignmentID) ([]string, map[string][]string, bool, error) {
			return exitchecks.WrittenSpecs(root, id)
		},
		DecisionsDir: func() (domain.DecisionsDir, error) {
			config, err := exitchecks.LoadConfig(root)
			return config.Decisions, err
		},
		File: func(path domain.RepoPath) ([]byte, bool, error) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path.String())))
			if errors.Is(err, os.ErrNotExist) {
				return nil, false, nil
			}
			return data, err == nil, err
		},
	}
}

// Read projects the event log, and the facts, into the View of a branch's
// active work.
func Read(log Reader, branch domain.Branch, facts Facts) (View, error) {
	records, err := log.ReadAll()
	if err != nil {
		return View{}, fmt.Errorf("progress: %w", err)
	}
	history := eventlog.NewHistory(records)
	works, err := history.Works()
	if err != nil {
		return View{}, fmt.Errorf("progress: %w", err)
	}
	others := otherBranches(works, branch)
	fact, err := domain.ActiveWorkOn(branch, works)
	if err != nil {
		return View{}, fmt.Errorf("progress: %w", err)
	}
	work, err := fact.Work()
	if errors.Is(err, domain.ErrNoActiveWork) {
		return View{Schema: 1, OtherBranches: others, Next: NextView{
			Actor:    NextAnyone,
			Message:  fmt.Sprintf("No active work on branch %s. Start one with `gf start --type <fix-bug|new-feature|modify-behaviour|refactor|improve-performance> \"<description>\"`.", branch),
			Commands: []string{`gf start --type <fix-bug|new-feature|modify-behaviour|refactor|improve-performance> "<description>"`},
		}}, nil
	}
	if err != nil {
		return View{}, fmt.Errorf("progress: %w", err)
	}

	view := View{Schema: 1, OtherBranches: others, Work: &WorkView{
		ID: work.ID().String(), Type: work.Type().String(), Description: work.Description().String(),
		Branch:      work.Branch().String(),
		ArtifactDir: domain.ArtifactDir(work.ID()).String() + "/",
		Reworks:     work.Reworks().Int(), ReworkBudget: work.ReworkBudget().Int(),
		Inputs: inputViews(work, facts),
	}}
	for _, id := range work.TargetNFRs() {
		view.Work.TargetNFRs = append(view.Work.TargetNFRs, id.String())
	}
	lastVisit := map[domain.Stage]domain.Visit{}
	var reworkOf *ReworkOfView
	var returned *ReturnedView
	for _, r := range history.Records(eventlog.WorkStream(work.ID())) {
		switch e := r.Event.(type) {
		case domain.StageEntered:
			lastVisit[e.Stage] = e.Visit
			if e.Stage != domain.StageDesign {
				continue
			}
			if state, _, err := history.Assignment(domain.AssignmentIDFor(work.ID(), e.Stage, e.Visit)); err == nil {
				if _, accepted := state.(domain.Accepted); accepted {
					view.Work.Design = &DesignView{
						Visit: e.Visit.Int(), Artifact: domain.ArtifactFor(work.ID(), e.Stage, e.Visit).String(),
						File: domain.DesignFor(work.ID(), e.Visit).String(), Slices: map[string]int{},
					}
				}
			}
		case domain.WorkReturned:
			returned = &ReturnedView{
				From: e.From.String(), FromVisit: e.FromVisit.Int(), Target: e.Target.String(), TargetVisit: e.Visit.Int(), Reason: e.Reason.String(),
			}
		case domain.ReworkStarted:
			reworkOf = &ReworkOfView{
				FixerArtifact: domain.ArtifactFor(work.ID(), domain.StageFixer, lastVisit[domain.StageFixer]).String(),
				Reason:        e.Reason.String(), Target: e.Target.String(), TargetVisit: e.Visit.Int(),
			}
		}
	}
	if facts.Design != nil {
		eventModel := acceptedEventModel(history, work, facts)
		design := facts.Design
		facts.Design = func(path domain.RepoPath, _ *domain.EventModel) (domain.Design, error) {
			return design(path, eventModel)
		}
	}
	if d := view.Work.Design; d != nil && facts.Design != nil {
		describeDesign(d, work.ID(), facts)
	}
	if work.Path().Includes(domain.StageEventModelling) {
		view.Work.EventModel = eventModelView(history, work, facts)
	}
	if work.Path().Includes(domain.StageGrill) && facts.DecisionsDir != nil {
		if dir, err := facts.DecisionsDir(); err == nil {
			view.Work.DecisionsDir = dir.Path().String()
		}
	}
	if visit, ok := lastVisit[domain.StageGrill]; ok && facts.Grill != nil {
		view.Work.Grill = grillView(history, work.ID(), visit, facts)
		if accepted, ok := latestAccepted(history, work.ID(), domain.StageGrill); ok && view.Work.Grill == nil {
			view.Work.Grill = grillView(history, work.ID(), accepted, facts) // until the later visit writes its own
		}
	}
	if visit, ok := lastVisit[domain.StageDesign]; ok && facts.NFRProposals != nil {
		view.Work.NFRProposals = nfrProposalsView(domain.NFRProposalsFor(work.ID(), visit), facts)
	}
	fixer, inFixer := work.(domain.InFixerWork)
	// In the fixer, the path is done up to the stage that handed over.
	onPath := work.CurrentStage()
	if inFixer {
		onPath = fixer.From()
	}
	stageStatus := StageDone
	for _, step := range work.Path().Steps() {
		sv := StageView{Stage: step.Stage().String(), Gate: step.Gate().String(), Budget: step.Budget().Int(), Status: stageStatus, Optional: step.Optional()}
		if step.Stage() == onPath {
			sv.Status, stageStatus = StageCurrent, StagePending
			if inFixer {
				sv.Status = StagePending
			}
		}
		if step.Optional() && sv.Status != StageCurrent && work.Skips(step.Stage()) {
			sv.Status = StageSkipped
		}
		if sv.Status == StageDone {
			sv.Artifact = domain.ArtifactFor(work.ID(), step.Stage(), lastVisit[step.Stage()]).String()
		}
		view.Work.Stages = append(view.Work.Stages, sv)
	}
	currentStep := work.CurrentStep()
	if inFixer {
		view.Work.Stages = append(view.Work.Stages, StageView{
			Stage: currentStep.Stage().String(), Gate: currentStep.Gate().String(), Budget: currentStep.Budget().Int(), Status: StageCurrent,
		})
	}

	current := &CurrentView{
		Stage: work.CurrentStage().String(), Visit: work.CurrentVisit().Int(),
		Gate: currentStep.Gate().String(), Assignment: AssignmentOpen, Budget: currentStep.Budget().Int(),
		Artifact: domain.ArtifactFor(work.ID(), work.CurrentStage(), work.CurrentVisit()).String(),
		Checks:   checkNames(work.CurrentStage(), work.Path()),
	}
	if inFixer {
		current.Fixer, err = fixerView(history, fixer, facts)
		if err != nil {
			return View{}, fmt.Errorf("progress: %w", err)
		}
	} else {
		current.ReworkOf = reworkOf
	}
	current.Returned = returned
	switch work.CurrentStage() {
	case domain.StressDecider(), domain.StageIntegrationTesting, domain.StageStressTesting:
		current.Stress = stressView(work, facts)
	}
	view.Current = current
	stage := work.CurrentStage()
	assignmentID := work.CurrentAssignment()

	state, _, err := history.Assignment(assignmentID)
	switch {
	case errors.Is(err, eventlog.ErrStreamNotFound):
		// Opened by the automations on the next command; until then it is open.
	case err != nil:
		return View{}, fmt.Errorf("progress: %w", err)
	default:
		current.Assignment, current.AttemptsUsed, current.Budget = describeAssignment(state)
	}
	current.LastProblem = lastProblem(history.Records(eventlog.AssignmentStream(assignmentID)))
	if stage == domain.StageSpecify && work.Path().Includes(domain.StageGrill) {
		current.ScenariosToCover, current.UncoveredScenarios = scenarioCoverage(history, work, facts)
	}
	if stage == domain.StageEventModelling && current.LastProblem != nil && facts.EventModel != nil {
		current.EventModelProblems = eventModelProblems(domain.EventModelFor(work.ID(), work.CurrentVisit()), facts)
	}
	if facts.Design != nil {
		switch {
		case stage == domain.StageDesign && current.LastProblem != nil:
			current.DesignProblems = designProblems(domain.DesignFor(work.ID(), work.CurrentVisit()), facts)
		case stage == domain.StageSpecify && view.Work.Design != nil:
			current.SlicesNeedingSpecs = slicesNeedingSpecs(view.Work.Design, work.ID(), facts)
		}
	}

	_, atReworkLimit := work.(domain.ReworkLimitReachedWork)
	switch {
	case atReworkLimit:
		view.Next = NextView{
			Actor: NextOwner,
			Message: fmt.Sprintf("Rework limit reached: '%s' found a problem in the code, and the work already went to the fixer %d of %d times. "+
				"The owner can run `gf rework-extend <n>` to allow more rework, or `gf abandon \"<reason>\"`.",
				stage, view.Work.Reworks, view.Work.ReworkBudget),
			Commands: []string{"gf rework-extend <n>", `gf abandon "<reason>"`},
		}
	case current.Assignment == AssignmentAwaitingApproval && stage == domain.StageDesign:
		view.Next = designAwaitsApproval(current, view.Work.NFRProposals)
	case current.Assignment == AssignmentAwaitingApproval && stage == domain.StageGrill:
		view.Next = grillAwaitsApproval(current, view.Work.Grill)
	case current.Assignment == AssignmentAwaitingApproval:
		view.Next = NextView{
			Actor:    NextOwner,
			Message:  fmt.Sprintf("Waiting for the owner to approve '%s': run `gf approve` or `gf reject \"<feedback>\"`.", stage),
			Commands: []string{"gf approve", `gf reject "<feedback>"`},
		}
	case current.Assignment == AssignmentEscalated:
		view.Next = NextView{
			Actor:    NextOwner,
			Message:  fmt.Sprintf("Escalated: budget exhausted on '%s'; the owner can run `gf extend <n>` or `gf abandon \"<reason>\"`.", stage),
			Commands: []string{"gf extend <n>", `gf abandon "<reason>"`},
		}
	case stage == domain.StressDecider() && work.Path().StressMandatory():
		view.Next = NextView{
			Actor:    NextAgent,
			Message:  "Work on 'review', then run `gf submit --passed` or `gf submit --failed \"<reason>\"`. Stress testing always runs on improve-performance work: there is no stress decision to make.",
			Commands: []string{"gf submit --passed", `gf submit --failed "<reason>"`},
		}
	case stage == domain.StressDecider():
		view.Next = reviewNext(current.Stress)
	case stage == domain.StageGrill:
		view.Next = NextView{
			Actor: NextAgent,
			Message: "Grill with the owner in this session, not in a subagent: load the gofast:grill skill, ask in rounds until no question is open, " +
				"write the decision document and " + strings.TrimSuffix(current.Artifact, ".md") + ".json, then run `gf submit --passed` or `gf submit --failed \"<reason>\"`.",
			Commands: []string{"gf submit --passed", `gf submit --failed "<reason>"`},
		}
	case inFixer:
		submit := fmt.Sprintf(`gf submit --passed --route <%s> "<reason>"`, strings.Join(current.Fixer.Targets, "|"))
		view.Next = NextView{
			Actor: NextAgent,
			Message: fmt.Sprintf("Find the root cause of the '%s' failure, then run `%s` to send the work to the stage that can fix it, or `gf submit --failed \"<reason>\"`.",
				fixer.From(), submit),
			Commands: []string{submit, `gf submit --failed "<reason>"`},
		}
	default:
		view.Next = NextView{
			Actor:    NextAgent,
			Message:  fmt.Sprintf("Work on '%s', then run `gf submit --passed` or `gf submit --failed \"<reason>\"`.", stage),
			Commands: []string{"gf submit --passed", `gf submit --failed "<reason>"`},
		}
	}
	return view, nil
}

// inputViews are the work's inputs, each checked against its content at
// start.
func inputViews(work domain.ActiveWork, facts Facts) []InputView {
	views := []InputView{}
	for _, in := range work.Inputs() {
		v := InputView{Kind: in.Kind().String(), Path: in.Path().String(), SHA256: in.Hash().String()}
		if facts.File != nil {
			content, ok, err := facts.File(in.Path())
			switch {
			case err != nil:
				v.Problem = "gf could not read it: " + err.Error()
			case !ok:
				v.Changed, v.Problem = true, "it no longer exists"
			default:
				v.Changed = in.ChangedSince(content)
			}
		}
		views = append(views, v)
	}
	return views
}

// grillView is a grill visit's outcome, as its structured file says.
func grillView(history eventlog.History, work domain.WorkID, visit domain.Visit, facts Facts) *GrillView {
	file := domain.GrillFor(work, visit)
	v := &GrillView{Visit: visit.Int(), File: file.String()}
	if state, _, err := history.Assignment(domain.AssignmentIDFor(work, domain.StageGrill, visit)); err == nil {
		_, v.Accepted = state.(domain.Accepted)
	}
	grill, err := facts.Grill(file)
	var invalid *exitchecks.GrillError
	switch {
	case errors.As(err, &invalid) && invalid.Missing:
		return nil
	case err != nil:
		v.Problem = "gf could not read it: " + err.Error()
		return v
	}
	v.DocPath, v.Decisions, v.AcceptanceScenarios = grill.DocPath().String(), len(grill.Decisions()), len(grill.AcceptanceScenarios())
	return v
}

// grillAwaitsApproval tells the owner to read the decision document: its
// approval is the owner's approval of the plan.
func grillAwaitsApproval(current *CurrentView, grill *GrillView) NextView {
	doc := current.Artifact
	if grill != nil && grill.DocPath != "" {
		doc = grill.DocPath
	}
	return NextView{
		Actor: NextOwner,
		Message: fmt.Sprintf("Waiting for the owner to approve 'grill': read the decision document %s. Approving it approves the plan: run `gf approve`, or `gf reject \"<feedback>\"` for another round.",
			doc),
		Commands: []string{"gf approve", `gf reject "<feedback>"`},
	}
}

// designAwaitsApproval tells the owner what to read in the design, and to
// adopt its NFR proposals, if any, before approving.
func designAwaitsApproval(current *CurrentView, proposals *NFRProposalsView) NextView {
	read := fmt.Sprintf("Waiting for the owner to approve 'design': read %s (its `## Decisions for the owner` section, if any) and %s.",
		current.Artifact, strings.TrimSuffix(current.Artifact, ".md")+".json")
	if proposals == nil {
		return NextView{
			Actor:    NextOwner,
			Message:  read + " Then run `gf approve` or `gf reject \"<feedback>\"`.",
			Commands: []string{"gf approve", `gf reject "<feedback>"`},
		}
	}
	return NextView{
		Actor: NextOwner,
		Message: fmt.Sprintf("%s It proposes NFRs (%s): adopt them with `gf nfrs adopt`, commit %s, then run `gf approve`; or `gf reject \"<feedback>\"`.",
			read, proposals.File, exitchecks.NFRsPath),
		Commands: []string{"gf nfrs adopt", "gf approve", `gf reject "<feedback>"`},
	}
}

// eventModelView is whether the work enters event modelling, and its latest
// accepted canonical model.
func eventModelView(history eventlog.History, work domain.ActiveWork, facts Facts) *EventModelView {
	v := &EventModelView{Included: !work.Skips(domain.StageEventModelling)}
	visit, ok := latestAccepted(history, work.ID(), domain.StageEventModelling)
	if !ok {
		return v
	}
	file := domain.EventModelFor(work.ID(), visit)
	v.Visit, v.File, v.Artifact = visit.Int(), file.String(), domain.ArtifactFor(work.ID(), domain.StageEventModelling, visit).String()
	if facts.EventModel != nil {
		model, err := facts.EventModel(file)
		if err != nil {
			v.Problem = "gf could not read the event model: " + err.Error()
		} else {
			v.Scenarios = len(model.Scenarios())
		}
	}
	return v
}

// scenarioCoverage are the scenarios specify covers, and those no test
// written in its current visit marks yet.
func scenarioCoverage(history eventlog.History, work domain.ActiveWork, facts Facts) (toCover, uncovered []string) {
	if _, ok := work.Input(domain.InputEventModel); ok {
		model := acceptedEventModel(history, work, facts)
		if model == nil {
			return nil, nil
		}
		toCover = domain.ScenarioIDs(model.ScenariosToCover())
	} else {
		visit, ok := latestAccepted(history, work.ID(), domain.StageGrill)
		if !ok || facts.Grill == nil {
			return nil, nil
		}
		grill, err := facts.Grill(domain.GrillFor(work.ID(), visit))
		if err != nil {
			return nil, nil
		}
		toCover = grill.AcceptanceIDs()
	}
	if facts.WrittenSpecs == nil {
		return toCover, nil
	}
	tests, markers, ok, err := facts.WrittenSpecs(work.CurrentAssignment())
	if err != nil || !ok {
		return toCover, nil
	}
	return toCover, domain.UncoveredScenarios(toCover, tests, markers)
}

// acceptedEventModel is the work's latest accepted canonical event model,
// as the working tree has it; nil when there is none, or gf can't read it.
func acceptedEventModel(history eventlog.History, work domain.ActiveWork, facts Facts) *domain.EventModel {
	if facts.EventModel == nil {
		return nil
	}
	visit, ok := latestAccepted(history, work.ID(), domain.StageEventModelling)
	if !ok {
		return nil
	}
	model, err := facts.EventModel(domain.EventModelFor(work.ID(), visit))
	if err != nil {
		return nil
	}
	return &model
}

// latestAccepted is the latest accepted visit of a stage of the work.
func latestAccepted(history eventlog.History, work domain.WorkID, stage domain.Stage) (domain.Visit, bool) {
	var visit domain.Visit
	found := false
	for _, r := range history.Records(eventlog.WorkStream(work)) {
		entered, ok := r.Event.(domain.StageEntered)
		if !ok || entered.Stage != stage {
			continue
		}
		if state, _, err := history.Assignment(domain.AssignmentIDFor(work, stage, entered.Visit)); err == nil {
			if _, accepted := state.(domain.Accepted); accepted {
				visit, found = entered.Visit, true
			}
		}
	}
	return visit, found
}

// describeDesign counts what the latest accepted design holds.
func describeDesign(d *DesignView, work domain.WorkID, facts Facts) {
	design, err := facts.Design(domain.DesignFor(work, mustVisit(d.Visit)), nil)
	if err != nil {
		d.Problem = "gf could not read the design: " + err.Error()
		return
	}
	d.Aggregates, d.Commands, d.Placements = len(design.Aggregates()), len(design.Commands()), len(design.Placements())
	for _, s := range design.Slices() {
		d.Slices[s.Change().String()]++
	}
}

// designProblems are what is wrong with a design visit's structured design;
// none when it is valid.
func designProblems(path domain.RepoPath, facts Facts) []string {
	_, err := facts.Design(path, nil)
	var invalid *exitchecks.DesignError
	switch {
	case errors.As(err, &invalid) && invalid.Missing:
		return []string{path.String() + " is missing"}
	case errors.As(err, &invalid):
		return invalid.Problems
	case err != nil:
		return []string{"gf could not read " + path.String() + ": " + err.Error()}
	}
	return nil
}

// eventModelProblems are what is wrong with an event-modelling visit's
// canonical event model; none when it is valid.
func eventModelProblems(path domain.RepoPath, facts Facts) []string {
	_, err := facts.EventModel(path)
	var invalid *exitchecks.EventModelError
	switch {
	case errors.As(err, &invalid) && invalid.Missing:
		return []string{path.String() + " is missing"}
	case errors.As(err, &invalid):
		return invalid.Problems
	case err != nil:
		return []string{"gf could not read " + path.String() + ": " + err.Error()}
	}
	return nil
}

// slicesNeedingSpecs names the latest accepted design's slices that need
// specs.
func slicesNeedingSpecs(d *DesignView, work domain.WorkID, facts Facts) []string {
	design, err := facts.Design(domain.DesignFor(work, mustVisit(d.Visit)), nil)
	if err != nil {
		return nil // DesignView.Problem says why
	}
	var names []string
	for _, s := range design.SlicesNeedingSpecs() {
		names = append(names, s.Name())
	}
	return names
}

func mustVisit(n int) domain.Visit {
	v, _ := domain.NewVisit(n)
	return v
}

// reviewNext tells review to decide stress testing as it passes; skip only
// if no NFR makes it mandatory.
func reviewNext(stress *StressView) NextView {
	failed := `gf submit --failed "<reason>"`
	if stress != nil && len(stress.Mandatory) > 0 {
		include := `gf submit --passed --stress include "<reason>"`
		return NextView{
			Actor: NextAgent,
			Message: fmt.Sprintf("Work on 'review', then run `%s` (stress testing is mandatory: the change touches the areas of %s) or `%s`.",
				include, mandatoryIDs(stress), failed),
			Commands: []string{include, failed},
		}
	}
	decide := `gf submit --passed --stress <include|skip> "<reason>"`
	return NextView{
		Actor: NextAgent,
		Message: fmt.Sprintf("Work on 'review', then run `%s` (include stress testing if the change can affect behaviour under load) or `%s`.",
			decide, failed),
		Commands: []string{decide, failed},
	}
}

func mandatoryIDs(stress *StressView) string {
	ids := make([]string, len(stress.Mandatory))
	for i, m := range stress.Mandatory {
		ids[i] = m.ID
	}
	return strings.Join(ids, ", ")
}

// stressView is the work's stress decision, the NFRs its changes make
// mandatory and, on stress testing, the latest run's results.
func stressView(work domain.ActiveWork, facts Facts) *StressView {
	v := &StressView{Mandatory: []MandatoryNFRView{}}
	if choice, owner, ok := work.StressDecision(); ok {
		d := &StressDecisionView{Decision: choice.Decision().String(), Reason: choice.Reason().String(), ForcedBy: []string{}, DecidedBy: DecidedByReview}
		if owner != (domain.Owner{}) {
			d.DecidedBy = DecidedByOwner
		}
		for _, id := range choice.ForcedBy() {
			d.ForcedBy = append(d.ForcedBy, id.String())
		}
		v.Decision = d
	}
	if facts.Stress != nil {
		nfrs, changed, err := facts.Stress(work)
		if err != nil {
			v.Problem = "gf could not tell which NFRs this change makes mandatory: " + err.Error()
		}
		for _, n := range domain.MandatoryNFRs(nfrs, changed, work.TargetNFRs()) {
			areas := []string{}
			for _, a := range n.Areas() {
				areas = append(areas, a.String())
			}
			v.Mandatory = append(v.Mandatory, MandatoryNFRView{
				ID: n.ID().String(), Areas: areas, Operation: n.Operation().String(),
				Load:    fmt.Sprintf("%d vus for %s", n.Load().VUs(), n.Load().Duration()),
				Targets: n.Targets().Describe(), Script: domain.StressScriptFor(work.ID(), n.ID()).String(),
			})
		}
	}
	if work.CurrentStage() == domain.StageStressTesting && facts.StressResults != nil {
		if r, err := stressResults(work.CurrentAssignment(), facts); err != nil {
			v.Problem = "gf could not read the last stress run: " + err.Error()
		} else {
			v.Results = r
		}
	}
	return v
}

func stressResults(id domain.AssignmentID, facts Facts) (*StressResultsView, error) {
	attempt, results, ok, err := facts.StressResults(id)
	if err != nil || !ok {
		return nil, err
	}
	a, err := domain.NewAttempt(attempt)
	if err != nil {
		return nil, err
	}
	return &StressResultsView{Attempt: attempt, RunDir: exitchecks.StressRunDir(id, a) + "/", NFRs: results}, nil
}

// fixerView describes the stage visit that handed the work to the fixer.
func fixerView(history eventlog.History, fixer domain.InFixerWork, facts Facts) (*FixerView, error) {
	from := domain.AssignmentIDFor(fixer.ID(), fixer.From(), fixer.FromVisit())
	v := &FixerView{
		From: fixer.From().String(), FromVisit: fixer.FromVisit().Int(), Mode: fixer.Mode().String(), Reason: fixer.Reason().String(),
		Artifact: domain.ArtifactFor(fixer.ID(), fixer.From(), fixer.FromVisit()).String(),
		Targets:  []string{},
	}
	for _, t := range fixer.RouteTargets() {
		v.Targets = append(v.Targets, t.String())
	}
	state, _, err := history.Assignment(from)
	if err != nil {
		return nil, err
	}
	if handed, ok := state.(domain.HandedToFixer); ok {
		v.CheckLog = exitchecks.LogPath(from, handed.Attempt())
		if fixer.From() == domain.StageStressTesting && facts.StressResults != nil {
			if r, err := stressResults(from, facts); err == nil && r != nil && r.Attempt == handed.Attempt().Int() {
				v.StressRunDir = r.RunDir
			}
		}
	}
	return v, nil
}

// nfrProposalsView is the design visit's NFR proposals, if any are pending
// adoption. Proposals gf can't read are design-valid's concern, not shown.
func nfrProposalsView(path domain.RepoPath, facts Facts) *NFRProposalsView {
	adoption, ok, err := facts.NFRProposals(path)
	if err != nil || !ok || !adoption.Pending() {
		return nil
	}
	v := &NFRProposalsView{File: path.String(), Added: []string{}, Replaced: []string{}}
	for _, n := range adoption.Added() {
		v.Added = append(v.Added, n.ID().String())
	}
	for _, r := range adoption.Replaced() {
		v.Replaced = append(v.Replaced, r.New.ID().String())
	}
	return v
}

func checkNames(stage domain.Stage, path domain.Path) []string {
	names := []string{}
	for _, c := range domain.RequiredChecks(stage, path) {
		names = append(names, c.String())
	}
	return names
}

func otherBranches(works []domain.WorkState, branch domain.Branch) []OtherBranchView {
	others := []OtherBranchView{}
	for _, w := range works {
		if p, ok := w.(domain.ActiveWork); ok && p.Branch() != branch {
			others = append(others, OtherBranchView{
				Branch: p.Branch().String(), WorkID: p.ID().String(), Type: p.Type().String(),
				Description: p.Description().String(), Stage: p.CurrentStage().String(),
			})
		}
	}
	return others
}

func describeAssignment(state domain.AssignmentState) (string, int, int) {
	switch s := state.(type) {
	case domain.Open:
		return AssignmentOpen, s.AttemptsUsed().Int(), s.Budget().Int()
	case domain.AwaitingApproval:
		return AssignmentAwaitingApproval, s.AttemptsUsed().Int(), s.Budget().Int()
	case domain.Escalated:
		return AssignmentEscalated, s.AttemptsUsed().Int(), s.Budget().Int()
	case domain.HandedToFixer:
		return AssignmentHandedToFixer, s.AttemptsUsed().Int(), s.Budget().Int()
	case domain.Accepted:
		return "accepted", s.AttemptsUsed().Int(), s.Budget().Int()
	case domain.Cancelled:
		return "cancelled", s.AttemptsUsed().Int(), s.Budget().Int()
	}
	return "unknown", 0, 0
}

func lastProblem(records []eventlog.Recorded) *ProblemView {
	var last *ProblemView
	for _, r := range records {
		switch e := r.Event.(type) {
		case domain.AttemptFailed:
			last = &ProblemView{Kind: ProblemFailedCheck, Attempt: e.Attempt.Int(), Text: e.Reason.String()}
		case domain.AssignmentRejected:
			last = &ProblemView{Kind: ProblemRejection, Attempt: e.Attempt.Int(), Text: e.Feedback.String()}
		case domain.AssignmentHandedToFixer:
			last = &ProblemView{Kind: ProblemCodeFailure, Attempt: e.Attempt.Int(), Text: e.Reason.String()}
		}
	}
	return last
}

// RenderJSON writes the view as the JSON `gf status --json` prints.
func RenderJSON(w io.Writer, v View) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// RenderText writes the view as the text `gf status` prints.
func RenderText(w io.Writer, v View) {
	if v.LogWarning != "" {
		fmt.Fprintf(w, "Warning: %s\n\n", v.LogWarning)
	}
	if v.Work == nil {
		fmt.Fprintln(w, v.Next.Message)
		if len(v.OtherBranches) > 0 {
			fmt.Fprintln(w)
		}
		for _, o := range v.OtherBranches {
			fmt.Fprintf(w, "%s '%s' is in progress on branch %s.\n", o.Type, o.Description, o.Branch)
		}
		return
	}
	fmt.Fprintf(w, "%s: %s (%s) on branch %s\n", v.Work.Type, v.Work.Description, v.Work.ID, v.Work.Branch)
	for _, in := range v.Work.Inputs {
		fmt.Fprintf(w, "Input (%s): %s\n", in.Kind, in.Path)
		switch {
		case in.Changed && in.Problem != "":
			fmt.Fprintf(w, "  Warning: %s, so stages can't read what the work started from. Restore it from git.\n", in.Problem)
		case in.Changed:
			fmt.Fprintln(w, "  Warning: its content changed since the work started, so stages now read something else than what the work started from. Restore it, or have the owner abandon the work and start it again.")
		case in.Problem != "":
			fmt.Fprintf(w, "  %s\n", in.Problem)
		}
	}
	fmt.Fprintln(w)
	for _, s := range v.Work.Stages {
		optional := ""
		if s.Optional {
			optional = ", optional"
		}
		fmt.Fprintf(w, "  %-9s %-20s %s gate%s\n", "["+s.Status+"]", s.Stage, s.Gate, optional)
	}
	c := v.Current
	fmt.Fprintf(w, "\nCurrent: %s (%s gate, visit %d), %s, attempts used %d of %d\n", c.Stage, c.Gate, c.Visit, strings.ReplaceAll(c.Assignment, "-", " "), c.AttemptsUsed, c.Budget)
	fmt.Fprintf(w, "Reworks: %d of %d\n", v.Work.Reworks, v.Work.ReworkBudget)
	if f := c.Fixer; f != nil {
		fmt.Fprintf(w, "Handed over by: %s (visit %d), %s: %s\n", f.From, f.FromVisit, f.Mode, f.Reason)
		fmt.Fprintf(w, "Its artifact: %s\n", f.Artifact)
		fmt.Fprintf(w, "Its check log: %s (if its checks ran)\n", f.CheckLog)
		fmt.Fprintf(w, "May return the work to: %s\n", strings.Join(f.Targets, ", "))
		if f.StressRunDir != "" {
			fmt.Fprintf(w, "Its stress run (k6 output and logs): %s\n", f.StressRunDir)
		}
	}
	if r := c.ReworkOf; r != nil {
		fmt.Fprintf(w, "Rework: the fixer sent the work back to %s (visit %d): %s\n", r.Target, r.TargetVisit, r.Reason)
		fmt.Fprintf(w, "Fixer's artifact: %s\n", r.FixerArtifact)
	}
	if r := c.Returned; r != nil {
		fmt.Fprintf(w, "Returned: the owner sent the work back from %s (visit %d) to %s (visit %d): %s\n", r.From, r.FromVisit, r.Target, r.TargetVisit, r.Reason)
	}
	if e := v.Work.EventModel; e != nil {
		switch {
		case !e.Included:
			fmt.Fprintln(w, "Event modelling: skipped (the work started without an event model)")
		case e.Visit == 0:
			fmt.Fprintln(w, "Event modelling: on the path (the work started from an event model)")
		case e.Problem != "":
			fmt.Fprintf(w, "Event model (visit %d): %s: %s\n", e.Visit, e.File, e.Problem)
		default:
			fmt.Fprintf(w, "Event model (visit %d): %s, %d scenarios (%s)\n", e.Visit, e.File, e.Scenarios, e.Artifact)
		}
	}
	if g := v.Work.Grill; g != nil {
		switch {
		case g.Problem != "":
			fmt.Fprintf(w, "Grill (visit %d): %s: %s\n", g.Visit, g.File, g.Problem)
		default:
			fmt.Fprintf(w, "Grill (visit %d): %s, %d decisions, %d acceptance scenarios (%s)\n", g.Visit, g.DocPath, g.Decisions, g.AcceptanceScenarios, g.File)
		}
	}
	if d := v.Work.Design; d != nil {
		renderDesign(w, d)
	}
	fmt.Fprintf(w, "Artifact: %s\n", c.Artifact)
	fmt.Fprintf(w, "Exit checks (run by `gf submit --passed`): %s\n", strings.Join(c.Checks, ", "))
	if len(c.DesignProblems) > 0 {
		fmt.Fprintln(w, "Design problems (design-valid):")
		for _, p := range c.DesignProblems {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	}
	if len(c.EventModelProblems) > 0 {
		fmt.Fprintln(w, "Event model problems (event-model-valid):")
		for _, p := range c.EventModelProblems {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	}
	if len(c.ScenariosToCover) > 0 {
		fmt.Fprintf(w, "Scenarios to cover (a `// gofast:scenario <ID>` line above a new test each): %s\n", strings.Join(c.ScenariosToCover, ", "))
		if len(c.UncoveredScenarios) > 0 {
			fmt.Fprintf(w, "Not covered yet: %s\n", strings.Join(c.UncoveredScenarios, ", "))
		}
	}
	if len(c.SlicesNeedingSpecs) > 0 {
		fmt.Fprintf(w, "Slices needing specs (a new Test<SliceName>… test each): %s\n", strings.Join(c.SlicesNeedingSpecs, ", "))
	}
	if st := c.Stress; st != nil {
		renderStress(w, c.Stage, st)
	}
	if p := v.Work.NFRProposals; p != nil {
		fmt.Fprintf(w, "NFR proposals pending adoption: %s (%s). The owner adopts them with `gf nfrs adopt` while design waits for approval, then commits %s.\n",
			p.File, describeProposals(p), exitchecks.NFRsPath)
	}
	if p := c.LastProblem; p != nil {
		label := "Last failure"
		if p.Kind == ProblemRejection {
			label = "Last rejection"
		}
		fmt.Fprintf(w, "%s (attempt %d): %s\n", label, p.Attempt, p.Text)
	}
	fmt.Fprintf(w, "\nNext: %s\n", v.Next.Message)
}

func renderDesign(w io.Writer, d *DesignView) {
	fmt.Fprintf(w, "Design (visit %d): %s, %s\n", d.Visit, d.Artifact, d.File)
	if d.Problem != "" {
		fmt.Fprintf(w, "  %s\n", d.Problem)
		return
	}
	var slices []string
	for _, c := range domain.Changes() {
		if n := d.Slices[c.String()]; n > 0 {
			slices = append(slices, fmt.Sprintf("%d %s", n, c))
		}
	}
	if d.Placements > 0 {
		fmt.Fprintf(w, "  %d aggregates, %d of the event model's commands placed\n", d.Aggregates, d.Placements)
		return
	}
	fmt.Fprintf(w, "  %d aggregates, %d commands, slices: %s\n", d.Aggregates, d.Commands, strings.Join(slices, ", "))
}

func describeProposals(p *NFRProposalsView) string {
	var parts []string
	if len(p.Added) > 0 {
		parts = append(parts, "adds "+strings.Join(p.Added, ", "))
	}
	if len(p.Replaced) > 0 {
		parts = append(parts, "replaces "+strings.Join(p.Replaced, ", "))
	}
	return strings.Join(parts, "; ")
}

func renderStress(w io.Writer, stage string, st *StressView) {
	if st.Problem != "" {
		fmt.Fprintf(w, "Stress testing: %s\n", st.Problem)
	}
	switch {
	case len(st.Mandatory) > 0:
		fmt.Fprintln(w, "Stress testing is mandatory: the change touches the areas of these NFRs:")
		for _, m := range st.Mandatory {
			fmt.Fprintf(w, "  %s (%s): %s, %s; %s\n", m.ID, strings.Join(m.Areas, ", "), m.Operation, m.Load, m.Targets)
			if stage == domain.StageStressTesting.String() {
				fmt.Fprintf(w, "    script: %s\n", m.Script)
			}
		}
	case stage == domain.StressDecider().String() && st.Problem == "":
		fmt.Fprintln(w, "Stress testing: no NFR covers this change; include it if the change can affect behaviour under load.")
	}
	if d := st.Decision; d != nil {
		forced := ""
		if len(d.ForcedBy) > 0 {
			forced = " (mandatory: " + strings.Join(d.ForcedBy, ", ") + ")"
		}
		fmt.Fprintf(w, "Stress decision: %s%s, by %s: %s\n", d.Decision, forced, d.DecidedBy, d.Reason)
	}
	if r := st.Results; r != nil {
		fmt.Fprintf(w, "Last stress run (attempt %d, %s):\n", r.Attempt, r.RunDir)
		for _, n := range r.NFRs {
			switch {
			case n.Problem != "":
				fmt.Fprintf(w, "  %s: not measured: %s\n", n.NFR, n.Problem)
			case n.Met:
				fmt.Fprintf(w, "  %s: met (%s; targets %s)\n", n.NFR, n.Measured, n.Targets)
			default:
				fmt.Fprintf(w, "  %s: %s (%s; targets %s)\n", n.NFR, strings.Join(n.Missed, ", "), n.Measured, n.Targets)
			}
		}
	}
}
