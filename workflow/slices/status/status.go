// Package status shows where the active work stands and what happens next.
package status

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Reader is what the status needs from the event log.
type Reader interface {
	ReadAll() ([]eventlog.Recorded, error)
}

// View is the status document. Its JSON form is consumed by hooks: field
// names and values are part of gf's interface; change them only with a new
// schema number.
type View struct {
	Schema  int          `json:"schema"`
	Work    *WorkView    `json:"work"`    // null when there is no active work
	Current *CurrentView `json:"current"` // null when there is no active work
	Next    NextView     `json:"next"`
}

type WorkView struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	ArtifactDir string      `json:"artifactDir"` // relative to the repository root, ends in "/"
	Stages      []StageView `json:"stages"`
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
}

// Problem kinds.
const (
	ProblemFailedCheck = "failed-check"
	ProblemRejection   = "rejection"
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

// Status projects the event log into a View.
func Status(log Reader) (View, error) {
	records, err := log.ReadAll()
	if err != nil {
		return View{}, fmt.Errorf("status: %w", err)
	}
	history := eventlog.NewHistory(records)
	work, _, err := history.ActiveWork()
	if errors.Is(err, domain.ErrNoActiveWork) {
		return View{Schema: 1, Next: NextView{
			Actor:    NextAnyone,
			Message:  "No active work. Start one with `gf start --type fix-bug \"<description>\"`.",
			Commands: []string{`gf start --type fix-bug "<description>"`},
		}}, nil
	}
	if err != nil {
		return View{}, fmt.Errorf("status: %w", err)
	}

	view := View{Schema: 1, Work: &WorkView{
		ID: work.ID().String(), Type: work.Type().String(), Description: work.Description().String(),
		ArtifactDir: domain.ArtifactDir(work.ID()).String() + "/",
	}}
	lastVisit := map[domain.Stage]domain.Visit{}
	for _, r := range history.Records(eventlog.WorkStream(work.ID())) {
		if entered, ok := r.Event.(domain.StageEntered); ok {
			lastVisit[entered.Stage] = entered.Visit
		}
	}
	stageStatus := StageDone
	var currentStep domain.PathStep
	for _, step := range work.Path().Steps() {
		sv := StageView{Stage: step.Stage().String(), Gate: step.Gate().String(), Budget: step.Budget().Int(), Status: stageStatus}
		if step.Stage() == work.CurrentStage() {
			sv.Status, stageStatus, currentStep = StageCurrent, StagePending, step
		}
		if sv.Status == StageDone {
			sv.Artifact = domain.ArtifactFor(work.ID(), step.Stage(), lastVisit[step.Stage()]).String()
		}
		view.Work.Stages = append(view.Work.Stages, sv)
	}

	current := &CurrentView{
		Stage: work.CurrentStage().String(), Visit: work.CurrentVisit().Int(),
		Gate: currentStep.Gate().String(), Assignment: AssignmentOpen, Budget: currentStep.Budget().Int(),
		Artifact: domain.ArtifactFor(work.ID(), work.CurrentStage(), work.CurrentVisit()).String(),
	}
	view.Current = current
	stage := work.CurrentStage()
	assignmentID := work.CurrentAssignment()

	state, _, err := history.Assignment(assignmentID)
	switch {
	case errors.Is(err, eventlog.ErrStreamNotFound):
		// Opened by the automations on the next command; until then it is open.
	case err != nil:
		return View{}, fmt.Errorf("status: %w", err)
	default:
		current.Assignment, current.AttemptsUsed, current.Budget = describeAssignment(state)
	}
	current.LastProblem = lastProblem(history.Records(eventlog.AssignmentStream(assignmentID)))

	switch current.Assignment {
	case AssignmentAwaitingApproval:
		view.Next = NextView{
			Actor:    NextOwner,
			Message:  fmt.Sprintf("Waiting for the owner to approve '%s': run `gf approve` or `gf reject \"<feedback>\"`.", stage),
			Commands: []string{"gf approve", `gf reject "<feedback>"`},
		}
	case AssignmentEscalated:
		view.Next = NextView{
			Actor:    NextOwner,
			Message:  fmt.Sprintf("Escalated: budget exhausted on '%s'; the owner can run `gf extend <n>` or `gf abandon \"<reason>\"`.", stage),
			Commands: []string{"gf extend <n>", `gf abandon "<reason>"`},
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

func describeAssignment(state domain.AssignmentState) (string, int, int) {
	switch s := state.(type) {
	case domain.Open:
		return AssignmentOpen, s.AttemptsUsed().Int(), s.Budget().Int()
	case domain.AwaitingApproval:
		return AssignmentAwaitingApproval, s.AttemptsUsed().Int(), s.Budget().Int()
	case domain.Escalated:
		return AssignmentEscalated, s.AttemptsUsed().Int(), s.Budget().Int()
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
		}
	}
	return last
}

func NewCommand(openStore func() (*eventlog.Store, error)) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [--json]",
		Short: "Show the active work, its path and what happens next",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			var view View
			err = store.Shared(func(s *eventlog.Snapshot) error {
				view, err = Status(s)
				return err
			})
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(view)
			}
			Render(cmd.OutOrStdout(), view)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print a stable JSON document")
	return cmd
}

// Render writes the view as the text `gf status` prints.
func Render(w io.Writer, v View) {
	if v.Work == nil {
		fmt.Fprintln(w, v.Next.Message)
		return
	}
	fmt.Fprintf(w, "%s: %s (%s)\n\n", v.Work.Type, v.Work.Description, v.Work.ID)
	for _, s := range v.Work.Stages {
		fmt.Fprintf(w, "  %-9s %-20s %s gate\n", "["+s.Status+"]", s.Stage, s.Gate)
	}
	c := v.Current
	fmt.Fprintf(w, "\nCurrent: %s (%s gate), %s, attempts used %d of %d\n", c.Stage, c.Gate, strings.ReplaceAll(c.Assignment, "-", " "), c.AttemptsUsed, c.Budget)
	fmt.Fprintf(w, "Artifact: %s\n", c.Artifact)
	if p := c.LastProblem; p != nil {
		label := "Last failure"
		if p.Kind == ProblemRejection {
			label = "Last rejection"
		}
		fmt.Fprintf(w, "%s (attempt %d): %s\n", label, p.Attempt, p.Text)
	}
	fmt.Fprintf(w, "\nNext: %s\n", v.Next.Message)
}
