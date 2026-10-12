// Package submitforacceptance submits the current stage visit with the
// agent's claim about its exit. The files the visit changed, however they
// were written, are checked against the stage's write scope, and on a
// passing claim gf runs the exit checks the stage requires. Review's pass
// also decides whether stress testing runs.
package submitforacceptance

import (
	"errors"
	"fmt"
	"io"
	"slices"
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

// Scope returns what the target's write scope depends on: the repository's
// decisions directory, which grill's write scope includes, and, on a
// refactor's implement, the latest accepted plan, whose areas are the only
// code it may change.
type Scope func(target Target) (domain.ScopeFacts, error)

// PlanFacts returns a scope's facts: the decisions directory, and the
// accepted plan when the target's write scope depends on it, read by
// acceptedPlan.
func PlanFacts(target Target, decisions domain.DecisionsDir, acceptedPlan func(work domain.WorkID, visit domain.Visit, end domain.AssignmentID) (domain.RefactorPlan, error)) (domain.ScopeFacts, error) {
	facts := domain.ScopeFacts{Decisions: decisions}
	if target.Open.Stage() != domain.StageImplement || !target.work.Path().Includes(domain.StagePlan) || target.planVisit == (domain.Visit{}) {
		return facts, nil
	}
	plan, err := acceptedPlan(target.Open.Work(), target.planVisit, target.planEnd)
	if err != nil {
		return domain.ScopeFacts{}, fmt.Errorf("%w: %w", domain.ErrNoAcceptedPlan, err)
	}
	facts.Plan = &plan
	return facts, nil
}

// StressFacts returns the repository's NFRs and the files the work changed
// since it started.
type StressFacts func(work domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error)

// Baseline is how a measure visit's baseline is read and kept. Measure
// returns what the visit's latest stress run measured of the work's target
// NFRs, judged against their targets; Keep records it once the work
// recorded it as its baseline. The zero value records no baseline.
type Baseline struct {
	Measure func(target Target) ([]domain.NFRResult, error)
	Keep    func(target Target) error
}

// Target is the open assignment a submission is for, as it was found.
type Target struct {
	Open    domain.Open
	version int
	work    domain.ActiveWork
	// specify is the latest accepted specify visit, and specifyEnd the visit
	// entered when it was accepted; designVisit and designEnd the same for
	// design, eventModelVisit and eventModelEnd for event modelling,
	// grillVisit and grillEnd for grill.
	specify, specifyEnd domain.AssignmentID
	designVisit         domain.Visit
	designEnd           domain.AssignmentID
	eventModelVisit     domain.Visit
	eventModelEnd       domain.AssignmentID
	grillVisit          domain.Visit
	grillEnd            domain.AssignmentID
	planVisit           domain.Visit
	planEnd             domain.AssignmentID
}

// Work is the work the target's assignment is a stage visit of.
func (t Target) Work() domain.ActiveWork { return t.work }

// Checked is what gf found out about the stage visit before deciding.
type Checked struct {
	Changed domain.ChangedFiles
	Scope   domain.ScopeFacts
	// BaselineStarted: the visit had no baseline, so its write-scope check
	// starts now and this submission's changes were not checked.
	BaselineStarted bool
	// Results are the exit checks' results; none for a failed claim.
	Results []domain.CheckResult
	// Baseline is what a passing measure visit measured of the target
	// NFRs; none on any other stage.
	Baseline []domain.NFRResult
}

// Submission is what submitting did.
type Submission struct {
	Assignment domain.Open
	Events     []domain.AssignmentEvent
	// WorkEvents are what the work recorded: a measure visit's baseline,
	// and its completion when there is nothing to improve.
	WorkEvents []domain.WorkEvent
	Checked    Checked
}

// SubmitForAcceptance submits the active work's current assignment in one
// go. The command does the same in three steps, so the checks run without
// holding the event log.
func SubmitForAcceptance(log Log, actor eventlog.Actor, branch domain.Branch, claim domain.AgentClaim, changes Changes, scope Scope, checks Checks, baseline Baseline) (Submission, error) {
	target, err := Find(log, branch)
	if err != nil {
		return Submission{}, err
	}
	if err := CheckClaim(target, claim); err != nil {
		return Submission{}, err
	}
	checked, err := Check(target, claim, changes, scope, checks, baseline, io.Discard)
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
	target.specify, target.specifyEnd, _ = history.LatestAccepted(work.ID(), domain.StageSpecify)
	_, target.designEnd, target.designVisit = history.LatestAccepted(work.ID(), domain.StageDesign)
	_, target.eventModelEnd, target.eventModelVisit = history.LatestAccepted(work.ID(), domain.StageEventModelling)
	_, target.grillEnd, target.grillVisit = history.LatestAccepted(work.ID(), domain.StageGrill)
	_, target.planEnd, target.planVisit = history.LatestAccepted(work.ID(), domain.StagePlan)
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

// MandatoryStress is review's pass on a path that always runs stress
// testing: gf records the choice, including stress testing, forced by the
// work's target NFRs and those covering what it changed. Review makes no
// decision there, so a --stress decision is refused before any check
// runs, using no attempt.
func MandatoryStress(target Target, claim domain.AgentClaim, decided bool, facts StressFacts) (domain.AgentClaim, error) {
	if target.Open.Stage() != domain.StressDecider() || !target.work.Path().StressMandatory() {
		return claim, nil
	}
	if decided {
		return domain.AgentClaim{}, fmt.Errorf("submit for acceptance: %w", domain.ErrStressMandatoryOnPath)
	}
	if !claim.Passed() {
		return claim, nil
	}
	nfrs, changed, err := facts(target.work)
	if err != nil {
		return domain.AgentClaim{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	return domain.ClaimStressDecided(domain.MandatoryStress(nfrs, changed, target.work.TargetNFRs())), nil
}

// DecideStress is review's pass with its stress decision. The NFRs covering
// what the work changed make stress testing mandatory, so a skip is then
// refused (domain.StressForcedError) before any check runs, using no
// attempt. Only review decides.
func DecideStress(target Target, decision domain.StressDecision, reason domain.Reason, facts StressFacts) (domain.AgentClaim, error) {
	if stage := target.Open.Stage(); stage != domain.StressDecider() {
		return domain.AgentClaim{}, fmt.Errorf("submit for acceptance: %s stage: %w", stage, domain.ErrStressOutsideReview)
	}
	nfrs, changed, err := facts(target.work)
	if err != nil {
		return domain.AgentClaim{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	choice, err := domain.ChooseStress(decision, reason, nfrs, changed)
	if err != nil {
		return domain.AgentClaim{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	return domain.ClaimStressDecided(choice), nil
}

// Check finds the files the visit changed and, when the agent claims a
// pass, runs the stage's exit checks. A failed claim is the agent's own
// verdict, so its checks are not run.
func Check(target Target, claim domain.AgentClaim, changes Changes, scope Scope, checks Checks, baseline Baseline, progress io.Writer) (Checked, error) {
	open := target.Open
	changed, started, err := changes(open.ID())
	if err != nil {
		return Checked{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	facts, err := scope(target)
	if err != nil {
		return Checked{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	checked := Checked{Changed: changed, Scope: facts, BaselineStarted: started}
	if !claim.Passed() {
		return checked, nil
	}
	checked.Results, err = checks(exitchecks.Visit{
		Assignment: open.ID(), Work: open.Work(), Path: target.work.Path(), Stage: open.Stage(), Visit: open.Visit(),
		Attempt: currentAttempt(open), Specify: target.specify, SpecifyEnd: target.specifyEnd,
		Design: target.designVisit, DesignEnd: target.designEnd,
		EventModel: target.eventModelVisit, EventModelEnd: target.eventModelEnd, HasEventModel: hasEventModel(target.work),
		Grill: target.grillVisit, GrillEnd: target.grillEnd, Plan: target.planVisit, PlanEnd: target.planEnd,
		Changed: changed, BaselineStarted: started, FirstAssignment: target.work.FirstAssignment(),
		TargetNFRs: target.work.TargetNFRs(),
	}, progress)
	if err != nil {
		return Checked{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	allPassed := !slices.ContainsFunc(checked.Results, func(r domain.CheckResult) bool { return !r.Passed() })
	if open.Stage() == domain.StageMeasure && allPassed && baseline.Measure != nil {
		if checked.Baseline, err = baseline.Measure(target); err != nil {
			return Checked{}, fmt.Errorf("submit for acceptance: %w", err)
		}
	}
	return checked, nil
}

func hasEventModel(work domain.ActiveWork) bool {
	_, ok := work.Input(domain.InputEventModel)
	return ok
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
	events, err := open.SubmitForAcceptance(target.work.Path(), checked.Scope, claim, checked.Changed, checked.Results)
	if err != nil {
		return Submission{}, err
	}
	if err := log.Append(eventlog.AssignmentStream(open.ID()), target.version, actor, eventlog.Events(events)...); err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	submitted := Submission{Assignment: open, Events: events, Checked: checked}
	accepted := slices.ContainsFunc(events, func(e domain.AssignmentEvent) bool { _, ok := e.(domain.AssignmentAccepted); return ok })
	if !accepted || len(checked.Baseline) == 0 {
		return submitted, nil
	}
	// Measure was accepted: the work records the baseline it measured.
	measuring, ok := target.work.(domain.InProgressWork)
	if !ok {
		return submitted, nil
	}
	if submitted.WorkEvents, err = measuring.RecordBaseline(checked.Baseline); err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	if len(submitted.WorkEvents) == 0 {
		return submitted, nil
	}
	records, err := log.ReadAll()
	if err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	stream := eventlog.WorkStream(open.Work())
	if err := log.Append(stream, eventlog.NewHistory(records).Version(stream), actor, eventlog.Events(submitted.WorkEvents)...); err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	return submitted, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error), changes Changes, scope Scope, checks Checks, stressFacts StressFacts, baseline Baseline) *cobra.Command {
	var passed bool
	var failed, routeTo, stress string
	cmd := &cobra.Command{
		Use:   `submit (--passed [--route <stage> "<reason>" | --stress <include|skip> "<reason>"] | --failed "<reason>")`,
		Short: "Submit the current stage; a pass is checked by running the stage's exit checks",
		Long: "Submit the current stage; a pass is checked by running the stage's exit checks.\n\n" +
			"In review, a pass decides whether stress testing runs, and why: gf submit --passed --stress <include|skip> \"<reason>\".\n" +
			"In the fixer, a pass says where the work goes and why: gf submit --passed --route <event-modelling|design|specify|implement> \"<reason>\"\n" +
			"(design only on paths with design, event-modelling only on work that started from an event model).",
		Args: func(cmd *cobra.Command, args []string) error {
			if passed == cmd.Flags().Changed("failed") {
				return errors.New(`choose exactly one of --passed or --failed "<reason>"`)
			}
			routed, decided := cmd.Flags().Changed("route"), cmd.Flags().Changed("stress")
			switch {
			case !routed && !decided:
				return cobra.NoArgs(cmd, args)
			case routed && decided:
				return errors.New(`--route is the fixer's and --stress review's: use only one`)
			case routed && !passed:
				return errors.New(`--route goes with --passed: gf submit --passed --route <stage> "<reason>"`)
			case decided && !passed:
				return errors.New(`--stress goes with --passed: gf submit --passed --stress <include|skip> "<reason>"`)
			case routed && len(args) != 1:
				return errors.New(`--route needs a reason: gf submit --passed --route <stage> "<reason>"`)
			case decided && len(args) != 1:
				return errors.New(`--stress needs a reason: gf submit --passed --stress <include|skip> "<reason>"`)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			claim := domain.ClaimPassed()
			var decision domain.StressDecision
			if cmd.Flags().Changed("stress") {
				var err error
				if decision, err = domain.ParseStressDecision(stress); err != nil {
					return err
				}
			}
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
					if target.work.Path().StressMandatory() {
						if claim, err = MandatoryStress(target, claim, decision != (domain.StressDecision{}), stressFacts); err != nil {
							return err
						}
						return CheckClaim(target, claim)
					}
					if decision != (domain.StressDecision{}) {
						reason, err := domain.NewReason(args[0])
						if err != nil {
							return err
						}
						if claim, err = DecideStress(target, decision, reason, stressFacts); err != nil {
							return err
						}
					}
					return CheckClaim(target, claim)
				})
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if claim.Passed() {
				fmt.Fprintf(out, "Running the exit checks of '%s': %s\n", target.Open.Stage(), checkNames(target.Open.Stage(), target.work.Path()))
			}
			checked, err := Check(target, claim, changes, scope, checks, baseline, out)
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
			if recordsBaseline(submitted.WorkEvents) && baseline.Keep != nil {
				if err := baseline.Keep(target); err != nil {
					return err
				}
			}
			describe(out, submitted)
			return nil
		},
	}
	cmd.Flags().BoolVar(&passed, "passed", false, "the stage is done; gf runs its exit checks to confirm")
	cmd.Flags().StringVar(&failed, "failed", "", "the stage is not done, with this reason")
	cmd.Flags().StringVar(&routeTo, "route", "", "fixer only: the earlier stage the work returns to; the reason follows")
	cmd.Flags().StringVar(&stress, "stress", "", "review only: include or skip stress testing; the reason follows")
	return cmd
}

func recordsBaseline(events []domain.WorkEvent) bool {
	return slices.ContainsFunc(events, func(e domain.WorkEvent) bool { _, ok := e.(domain.BaselineMeasured); return ok })
}

func checkNames(stage domain.Stage, path domain.Path) string {
	var names []string
	for _, c := range domain.RequiredChecks(stage, path) {
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
			if stress := e.Stress; stress.Decision() != (domain.StressDecision{}) {
				fmt.Fprintf(out, "'%s' accepted, deciding to %s stress testing%s. Run `gf status` to see what happens next.\n",
					stage, stress.Decision(), forcedBy(stress))
				continue
			}
			fmt.Fprintf(out, "'%s' accepted. Run `gf status` to see what happens next.\n", stage)
		}
	}
	for _, e := range submitted.WorkEvents {
		switch e := e.(type) {
		case domain.BaselineMeasured:
			fmt.Fprintf(out, "Baseline of %s: %s.\n", e.NFR, e.Measurement.Describe())
		case domain.WorkFoundNothingToImprove:
			fmt.Fprintln(out, "Every target NFR already meets its targets at baseline: there is nothing to improve, so the work is complete. "+
				"To ask for more, tighten the NFR with `gf nfrs adopt` on a later design, or start new work with a stricter NFR.")
		}
	}
}

// forcedBy names the NFRs that made stress testing mandatory, if any.
func forcedBy(c domain.StressChoice) string {
	ids := c.ForcedBy()
	if len(ids) == 0 {
		return ""
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id.String()
	}
	return " (mandatory: " + strings.Join(names, ", ") + ")"
}
