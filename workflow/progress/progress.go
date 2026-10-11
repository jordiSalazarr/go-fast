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
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

// Reader is what the read model needs from the event log.
type Reader interface {
	ReadAll() ([]eventlog.Recorded, error)
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
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Branch      string      `json:"branch"`
	ArtifactDir string      `json:"artifactDir"` // relative to the repository root, ends in "/"
	Stages      []StageView `json:"stages"`
	// Reworks is how many times the work entered the fixer, of ReworkBudget.
	Reworks      int `json:"reworks"`
	ReworkBudget int `json:"reworkBudget"`
}

// Stage status values.
const (
	StageDone    = "done"
	StageCurrent = "current"
	StagePending = "pending"
)

type StageView struct {
	Stage  string `json:"stage"`
	Gate   string `json:"gate"`
	Budget int    `json:"budget"`
	Status string `json:"status"`
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
}

// ReworkOfView is the fixer visit that sent the work back.
type ReworkOfView struct {
	FixerArtifact string `json:"fixerArtifact"`
	Reason        string `json:"reason"`
	Target        string `json:"target"`
	TargetVisit   int    `json:"targetVisit"`
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

// Read projects the event log into the View of a branch's active work.
func Read(log Reader, branch domain.Branch) (View, error) {
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
			Message:  fmt.Sprintf("No active work on branch %s. Start one with `gf start --type fix-bug \"<description>\"`.", branch),
			Commands: []string{`gf start --type fix-bug "<description>"`},
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
	}}
	lastVisit := map[domain.Stage]domain.Visit{}
	var reworkOf *ReworkOfView
	for _, r := range history.Records(eventlog.WorkStream(work.ID())) {
		switch e := r.Event.(type) {
		case domain.StageEntered:
			lastVisit[e.Stage] = e.Visit
		case domain.ReworkStarted:
			reworkOf = &ReworkOfView{
				FixerArtifact: domain.ArtifactFor(work.ID(), domain.StageFixer, lastVisit[domain.StageFixer]).String(),
				Reason:        e.Reason.String(), Target: e.Target.String(), TargetVisit: e.Visit.Int(),
			}
		}
	}
	fixer, inFixer := work.(domain.InFixerWork)
	// In the fixer, the path is done up to the stage that handed over.
	onPath := work.CurrentStage()
	if inFixer {
		onPath = fixer.From()
	}
	stageStatus := StageDone
	for _, step := range work.Path().Steps() {
		sv := StageView{Stage: step.Stage().String(), Gate: step.Gate().String(), Budget: step.Budget().Int(), Status: stageStatus}
		if step.Stage() == onPath {
			sv.Status, stageStatus = StageCurrent, StagePending
			if inFixer {
				sv.Status = StagePending
			}
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
		Checks:   checkNames(work.CurrentStage()),
	}
	if inFixer {
		current.Fixer, err = fixerView(history, fixer)
		if err != nil {
			return View{}, fmt.Errorf("progress: %w", err)
		}
	} else {
		current.ReworkOf = reworkOf
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

// fixerView describes the stage visit that handed the work to the fixer.
func fixerView(history eventlog.History, fixer domain.InFixerWork) (*FixerView, error) {
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
	}
	return v, nil
}

func checkNames(stage domain.Stage) []string {
	names := []string{}
	for _, c := range stage.RequiredChecks() {
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
	fmt.Fprintf(w, "%s: %s (%s) on branch %s\n\n", v.Work.Type, v.Work.Description, v.Work.ID, v.Work.Branch)
	for _, s := range v.Work.Stages {
		fmt.Fprintf(w, "  %-9s %-20s %s gate\n", "["+s.Status+"]", s.Stage, s.Gate)
	}
	c := v.Current
	fmt.Fprintf(w, "\nCurrent: %s (%s gate, visit %d), %s, attempts used %d of %d\n", c.Stage, c.Gate, c.Visit, strings.ReplaceAll(c.Assignment, "-", " "), c.AttemptsUsed, c.Budget)
	fmt.Fprintf(w, "Reworks: %d of %d\n", v.Work.Reworks, v.Work.ReworkBudget)
	if f := c.Fixer; f != nil {
		fmt.Fprintf(w, "Handed over by: %s (visit %d), %s: %s\n", f.From, f.FromVisit, f.Mode, f.Reason)
		fmt.Fprintf(w, "Its artifact: %s\n", f.Artifact)
		fmt.Fprintf(w, "Its check log: %s (if its checks ran)\n", f.CheckLog)
		fmt.Fprintf(w, "May return the work to: %s\n", strings.Join(f.Targets, ", "))
	}
	if r := c.ReworkOf; r != nil {
		fmt.Fprintf(w, "Rework: the fixer sent the work back to %s (visit %d): %s\n", r.Target, r.TargetVisit, r.Reason)
		fmt.Fprintf(w, "Fixer's artifact: %s\n", r.FixerArtifact)
	}
	fmt.Fprintf(w, "Artifact: %s\n", c.Artifact)
	fmt.Fprintf(w, "Exit checks (run by `gf submit --passed`): %s\n", strings.Join(c.Checks, ", "))
	if p := c.LastProblem; p != nil {
		label := "Last failure"
		if p.Kind == ProblemRejection {
			label = "Last rejection"
		}
		fmt.Fprintf(w, "%s (attempt %d): %s\n", label, p.Attempt, p.Text)
	}
	fmt.Fprintf(w, "\nNext: %s\n", v.Next.Message)
}
