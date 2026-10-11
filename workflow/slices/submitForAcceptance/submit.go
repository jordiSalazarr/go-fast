// Package submitforacceptance submits the current stage visit with the
// agent's claim about its exit. The files the visit changed, however they
// were written, are checked against the stage's write scope, and on a
// passing claim gf runs the exit checks the stage requires.
package submitforacceptance

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

// Reader is what finding the assignment needs from the event log.
type Reader interface {
	ReadAll() ([]eventlog.Recorded, error)
}

// Log is what submitting needs from the event log.
type Log interface {
	Reader
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// Changes returns the files a stage visit changed since its baseline. When
// the visit has no baseline, it takes one now and reports started.
type Changes func(domain.AssignmentID) (changed domain.ChangedFiles, started bool, err error)

// Checks runs a stage visit's required exit checks, telling progress about
// each as it finishes.
type Checks func(v exitchecks.Visit, progress io.Writer) ([]domain.CheckResult, error)

// Target is the open assignment a submission is for, as it was found.
type Target struct {
	Open    domain.Open
	version int
	work    domain.ActiveWork
	specify domain.AssignmentID
}

// Checked is what gf found out about the stage visit before deciding.
type Checked struct {
	Changed domain.ChangedFiles
	// BaselineStarted: the visit had no baseline, so its write-scope check
	// starts now and this submission's changes were not checked.
	BaselineStarted bool
	// Results are the exit checks' results; none for a failed claim.
	Results []domain.CheckResult
}

// Submission is what submitting did.
type Submission struct {
	Assignment domain.Open
	Events     []domain.AssignmentEvent
	Checked    Checked
}

// SubmitForAcceptance submits the active work's current assignment in one
// go. The command does the same in three steps, so the checks run without
// holding the event log.
func SubmitForAcceptance(log Log, actor eventlog.Actor, branch domain.Branch, claim domain.AgentClaim, changes Changes, checks Checks) (Submission, error) {
	target, err := Find(log, branch)
	if err != nil {
		return Submission{}, err
	}
	if err := CheckClaim(target, claim); err != nil {
		return Submission{}, err
	}
	checked, err := Check(target, claim, changes, checks, io.Discard)
	if err != nil {
		return Submission{}, err
	}
	return Submit(log, actor, target, claim, checked)
}

// Find finds the open assignment of the branch's active work.
func Find(log Reader, branch domain.Branch) (Target, error) {
	records, err := log.ReadAll()
	if err != nil {
		return Target{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return Target{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	work, err := fact.Work()
	if err != nil {
		return Target{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	state, version, err := history.Assignment(work.CurrentAssignment())
	if err != nil {
		return Target{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	open, ok := state.(domain.Open)
	if !ok {
		return Target{}, fmt.Errorf("submit for acceptance on %s: assignment is %T: %w", work.CurrentStage(), state, domain.ErrNotAwaitingSubmission)
	}
	target := Target{Open: open, version: version, work: work}
	for _, r := range history.Records(eventlog.WorkStream(work.ID())) {
		if entered, ok := r.Event.(domain.StageEntered); ok && entered.Stage == domain.StageSpecify {
			target.specify = domain.AssignmentIDFor(work.ID(), entered.Stage, entered.Visit)
		}
	}
	return target, nil
}

// CheckClaim rejects a claim before any check runs, so it uses no attempt:
// the fixer's pass needs a route its work allows, and no other stage takes
// a route.
func CheckClaim(target Target, claim domain.AgentClaim) error {
	if err := target.Open.CheckClaim(claim); err != nil && !errors.Is(err, domain.ErrMissingRoute) {
		return fmt.Errorf("submit for acceptance: %w", err)
	}
	fixer, ok := target.work.(domain.InFixerWork)
	if !ok || !claim.Passed() {
		return nil
	}
	route, _ := claim.Route()
	if err := fixer.CheckRoute(route.Target()); err != nil {
		return fmt.Errorf("submit for acceptance: %w", err)
	}
	return nil
}

// Check finds the files the visit changed and, when the agent claims a
// pass, runs the stage's exit checks. A failed claim is the agent's own
// verdict, so its checks are not run.
func Check(target Target, claim domain.AgentClaim, changes Changes, checks Checks, progress io.Writer) (Checked, error) {
	open := target.Open
	changed, started, err := changes(open.ID())
	if err != nil {
		return Checked{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	checked := Checked{Changed: changed, BaselineStarted: started}
	if !claim.Passed() {
		return checked, nil
	}
	checked.Results, err = checks(exitchecks.Visit{
		Assignment: open.ID(), Work: open.Work(), Stage: open.Stage(), Visit: open.Visit(),
		Attempt: currentAttempt(open), Specify: target.specify, Changed: changed, BaselineStarted: started,
	}, progress)
	if err != nil {
		return Checked{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	return checked, nil
}

func currentAttempt(open domain.Open) domain.Attempt {
	attempt, _ := domain.NewAttempt(open.AttemptsUsed().Int() + 1)
	return attempt
}

// Submit decides the target's submission. If the assignment changed since it
// was found, it fails with a version conflict and the submission is lost:
// it must be made again.
func Submit(log Log, actor eventlog.Actor, target Target, claim domain.AgentClaim, checked Checked) (Submission, error) {
	open := target.Open
	events, err := open.SubmitForAcceptance(claim, checked.Changed, checked.Results)
	if err != nil {
		return Submission{}, err
	}
	if err := log.Append(eventlog.AssignmentStream(open.ID()), target.version, actor, eventlog.Events(events)...); err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	return Submission{Assignment: open, Events: events, Checked: checked}, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error), changes Changes, checks Checks) *cobra.Command {
	var passed bool
	var failed, routeTo string
	cmd := &cobra.Command{
		Use:   `submit (--passed [--route <stage> "<reason>"] | --failed "<reason>")`,
		Short: "Submit the current stage; a pass is checked by running the stage's exit checks",
		Long: "Submit the current stage; a pass is checked by running the stage's exit checks.\n\n" +
			"In the fixer, a pass says where the work goes and why: gf submit --passed --route <specify|implement> \"<reason>\".",
		Args: func(cmd *cobra.Command, args []string) error {
			if passed == cmd.Flags().Changed("failed") {
				return errors.New(`choose exactly one of --passed or --failed "<reason>"`)
			}
			if !cmd.Flags().Changed("route") {
				return cobra.NoArgs(cmd, args)
			}
			if !passed {
				return errors.New(`--route goes with --passed: gf submit --passed --route <stage> "<reason>"`)
			}
			if len(args) != 1 {
				return errors.New(`--route needs a reason: gf submit --passed --route <stage> "<reason>"`)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			claim := domain.ClaimPassed()
			if cmd.Flags().Changed("route") {
				target, err := domain.ParseStage(routeTo)
				if err != nil {
					return fmt.Errorf("route: %w", err)
				}
				reason, err := domain.NewReason(args[0])
				if err != nil {
					return err
				}
				route, err := domain.NewRoute(target, reason)
				if err != nil {
					return err
				}
				claim = domain.ClaimRouted(route)
			}
			if !passed {
				reason, err := domain.NewReason(failed)
				if err != nil {
					return err
				}
				claim = domain.ClaimFailed(reason)
			}
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			branch, err := currentBranch()
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			// The checks can take minutes, so they run between two short
			// holds of the event log; Submit refuses if the assignment
			// changed in between.
			var target Target
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automate.AroundCommand(s, func() error {
					if target, err = Find(s, branch); err != nil {
						return err
					}
					return CheckClaim(target, claim)
				})
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if claim.Passed() {
				fmt.Fprintf(out, "Running the exit checks of '%s': %s\n", target.Open.Stage(), checkNames(target.Open.Stage()))
			}
			checked, err := Check(target, claim, changes, checks, out)
			if err != nil {
				return err
			}
			var submitted Submission
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automate.AroundCommand(s, func() error {
					submitted, err = Submit(s, c.Actor(), target, claim, checked)
					return err
				})
			})
			if err != nil {
				return err
			}
			describe(out, submitted)
			return nil
		},
	}
	cmd.Flags().BoolVar(&passed, "passed", false, "the stage is done; gf runs its exit checks to confirm")
	cmd.Flags().StringVar(&failed, "failed", "", "the stage is not done, with this reason")
	cmd.Flags().StringVar(&routeTo, "route", "", "fixer only: the earlier stage the work returns to; the reason follows")
	return cmd
}

func checkNames(stage domain.Stage) string {
	names := make([]string, 0, len(stage.RequiredChecks()))
	for _, c := range stage.RequiredChecks() {
		names = append(names, c.String())
	}
	return strings.Join(names, ", ")
}

func describe(out io.Writer, submitted Submission) {
	open := submitted.Assignment
	stage := open.Stage()
	if submitted.Checked.BaselineStarted {
		fmt.Fprintf(out, "Note: no baseline of the working tree was recorded when '%s' began, so its write-scope check starts now.\n", stage)
	}
	for _, e := range submitted.Events {
		switch e := e.(type) {
		case domain.AttemptFailed:
			fmt.Fprintf(out, "Attempt %d on '%s' failed (%d of %d attempts used): %s\n",
				e.Attempt.Int(), stage, e.Attempt.Int(), open.Budget().Int(), e.Reason)
		case domain.AssignmentEscalated:
			fmt.Fprintf(out, "Escalated: budget exhausted on '%s'. The owner can run `gf extend <attempts>` or `gf abandon \"<reason>\"`.\n", stage)
		case domain.AssignmentHandedToFixer:
			fmt.Fprintf(out, "'%s' failed because the code is wrong, which this stage can't change: the work goes to the fixer (no attempt used): %s\n",
				stage, e.Reason)
		case domain.SubmittedForApproval:
			fmt.Fprintf(out, "'%s' passed its exit checks and waits for the owner: `gf approve` or `gf reject \"<feedback>\"`.\n", stage)
		case domain.AssignmentAccepted:
			if route := e.Route; route.Target() != (domain.Stage{}) {
				fmt.Fprintf(out, "'%s' accepted: the work returns to '%s' (%s). Run `gf status` to see what happens next.\n", stage, route.Target(), route.Reason())
				continue
			}
			fmt.Fprintf(out, "'%s' accepted. Run `gf status` to see what happens next.\n", stage)
		}
	}
}
