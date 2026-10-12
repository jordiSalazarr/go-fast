package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/gitbranch"
	startwork "github.com/jordiSalazarr/go-fast/workflow/slices/startWork"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

const seeLog = "Details are in .gofast/gf.log."

// friendly turns an error into a short message that says what to do next.
// The full error chain goes to .gofast/gf.log, never to the terminal.
func friendly(err error, cmd *cobra.Command, ran bool) string {
	var (
		ownerOnly *caller.OwnerOnlyError
		active    *domain.WorkAlreadyActiveError
		conflict  *domain.ConflictingActiveWorkError
		malformed *eventlog.MalformedLogError
		config    *exitchecks.ConfigError
		nfrs      *exitchecks.NFRsError
		route     *domain.InvalidRouteError
		noRoute   *domain.RouteMissingError
		forced    *domain.StressForcedError
		back      *domain.InvalidReturnError
		input     *startwork.InputError
		target    *domain.UnknownTargetNFRError
	)
	switch {
	case errors.Is(err, gitbranch.ErrDetachedHead):
		return "You are not on a branch. Check out a branch before running gf."
	case errors.As(err, &ownerOnly) && ownerOnly.NoTerminal:
		return fmt.Sprintf("Only the owner can %s, from their own terminal. Run `%s` there.", ownerOnly.Action, ownerOnly.Command)
	case errors.As(err, &ownerOnly):
		return fmt.Sprintf("Only the owner can %s. Ask the owner to run `%s`.", ownerOnly.Action, ownerOnly.Command)
	case errors.As(err, &input):
		return input.Error()
	case errors.Is(err, domain.ErrInputNotAccepted), errors.Is(err, domain.ErrEventModelNotOnPath):
		return "Inputs (--spec, --event-model) are for feature work: new-feature or modify-behaviour."
	case errors.Is(err, domain.ErrTargetNFRsRequired):
		return "Improve-performance work targets the NFRs it must meet: `gf start --type improve-performance --nfr <id> [--nfr <id>…] \"<description>\"`, with ids from .gofast/nfrs.json (`gf checks` lists them)."
	case errors.Is(err, domain.ErrTargetNFRsNotAccepted):
		return "Target NFRs (--nfr) are for improve-performance work."
	case errors.As(err, &target) && len(target.Known) == 0:
		return fmt.Sprintf("The NFR '%s' is not in .gofast/nfrs.json, which lists none. The owner adds it first; `gf checks` shows the NFRs.", target.NFR)
	case errors.As(err, &target):
		return fmt.Sprintf("The NFR '%s' is not in .gofast/nfrs.json. Its NFRs: %s.", target.NFR, target.KnownIDs())
	case errors.Is(err, domain.ErrStressMandatoryOnPath) && cmd.Name() == "stress":
		return "Improve-performance work always runs stress testing against its target NFRs: there is no stress decision to change."
	case errors.Is(err, domain.ErrStressMandatoryOnPath):
		return "Improve-performance work always runs stress testing: review makes no stress decision. Submit with `gf submit --passed` or `gf submit --failed \"<reason>\"`. No attempt was used."
	case errors.Is(err, domain.ErrSpecNotMarkdown):
		return "The spec must be a markdown file (.md)."
	case errors.As(err, &active):
		return fmt.Sprintf("There is already active work on branch %s: %s %q (%s). Finish it, or have the owner run `gf abandon \"<reason>\"`, before starting another.",
			active.Branch, active.Type, active.Description, active.ID)
	case errors.As(err, &conflict):
		ids := make([]string, len(conflict.Works))
		for i, id := range conflict.Works {
			ids[i] = id.String()
		}
		return fmt.Sprintf("Branch %s has more than one work in progress (%s), usually after a bad merge. Resolve .gofast/events.jsonl by hand. %s",
			conflict.Branch, strings.Join(ids, ", "), seeLog)
	case errors.Is(err, domain.ErrNoActiveWork):
		return "There is no active work on this branch. Start one with `gf start --type <fix-bug|new-feature|modify-behaviour|refactor|improve-performance> \"<description>\"`."
	case errors.Is(err, domain.ErrNotAwaitingSubmission):
		return "There is nothing to submit right now. Run `gf status` to see what happens next."
	case errors.Is(err, domain.ErrNotAwaitingApproval):
		return "Nothing is waiting for approval. Run `gf status` to see what happens next."
	case errors.Is(err, domain.ErrNotEscalated):
		return "The budget can only be extended when a stage is escalated. Run `gf status` to see what happens next."
	case errors.As(err, &route):
		return fmt.Sprintf("The fixer can only send the work back to an earlier stage that can fix it: %s. '%s' is not one of them. No attempt was used.",
			stageNames(route.Allowed), route.Target)
	case errors.As(err, &noRoute):
		return fmt.Sprintf("The fixer says where the work goes: `gf submit --passed --route <%s> \"<reason>\"`. No attempt was used.",
			strings.ReplaceAll(stageNames(noRoute.Allowed), ", ", "|"))
	case errors.As(err, &back) && len(back.Allowed) == 0:
		return fmt.Sprintf("The work can't go back to '%s': it is on the first stage of its path.", back.Target)
	case errors.As(err, &back):
		return fmt.Sprintf("The work can only go back to an earlier stage of its path that it entered: %s. '%s' is not one of them.",
			stageNames(back.Allowed), back.Target)
	case errors.Is(err, domain.ErrNoAcceptedPlan):
		return "A refactor's implement may change only the accepted plan's areas, and gf can't read that plan as the owner accepted it. " +
			"The owner can send the work back with `gf return plan \"<reason>\"`. " + seeLog
	case errors.Is(err, domain.ErrRouteOutsideFixer):
		return "Only the fixer chooses a route. Submit with `gf submit --passed` or `gf submit --failed \"<reason>\"`. No attempt was used."
	case errors.As(err, &forced) && cmd.Name() == "stress":
		return fmt.Sprintf("Stress testing is mandatory: the change touches the areas of %s. Run `gf stress include \"<reason>\"` instead, or change the NFRs.",
			forced.Describe())
	case errors.As(err, &forced):
		return fmt.Sprintf("Stress testing is mandatory: the change touches the areas of %s. Submit with `gf submit --passed --stress include \"<reason>\"`. No attempt was used.",
			forced.Describe())
	case errors.Is(err, domain.ErrMissingStressDecision):
		return "Review decides whether stress testing runs: `gf submit --passed --stress <include|skip> \"<reason>\"`. Include it if the change can affect behaviour under load. No attempt was used."
	case errors.Is(err, domain.ErrStressOutsideReview):
		return "Only review decides stress testing. Submit with `gf submit --passed` or `gf submit --failed \"<reason>\"`. No attempt was used."
	case errors.Is(err, domain.ErrUnknownStressDecision):
		return "Stress testing is either `include` or `skip`."
	case errors.Is(err, domain.ErrStressDecisionClosed):
		return "The stress decision can only change while the work is on a stage up to integration testing, outside the fixer. Run `gf status` to see where it is."
	case errors.Is(err, worktree.ErrNoWorkBaseline):
		return "gf can't tell what this work changed: the baseline of its first stage is missing (the work was started before this version of gf, or on another machine). The owner should abandon this work with `gf abandon \"<reason>\"` and start it again."
	case errors.Is(err, domain.ErrUnknownStage):
		return fmt.Sprintf("Unknown stage. Stages: %s.", stageNames(domain.Stages()))
	case errors.Is(err, domain.ErrNotAtReworkLimit):
		return "Rework can only be extended when the work reached its rework limit. Run `gf status` to see what happens next."
	case errors.Is(err, domain.ErrInvalidReworkBudget):
		return "The number of extra reworks must be a positive whole number, e.g. `gf rework-extend 1`."
	case errors.Is(err, domain.ErrUnknownWorkType):
		names := make([]string, 0, len(domain.WorkTypes()))
		for _, t := range domain.WorkTypes() {
			names = append(names, t.String())
		}
		return fmt.Sprintf("Unknown work type. Available: %s.", strings.Join(names, ", "))
	case errors.Is(err, domain.ErrEmptyDescription):
		return "Describe the work: `gf start --type <fix-bug|new-feature|modify-behaviour|refactor|improve-performance> \"<description>\"`."
	case errors.Is(err, domain.ErrEmptyFeedback):
		return "Say what needs to change: `gf reject \"<feedback>\"`."
	case errors.Is(err, domain.ErrEmptyReason):
		return "The reason cannot be empty."
	case errors.Is(err, domain.ErrInvalidAttemptBudget):
		return "The number of extra attempts must be a positive whole number, e.g. `gf extend 2`."
	case errors.Is(err, caller.ErrNoOwnerIdentity):
		return "gf could not tell who you are. Set `git config user.name \"Your Name\"` (and user.email) in this repository."
	case errors.Is(err, eventlog.ErrNoRepository):
		return "Not inside a git repository. Run gf from your repository, or pass --dir."
	case errors.Is(err, eventlog.ErrLogChangedOutsideGf):
		return "The event log was changed outside gf since its last write. Inspect .gofast/events.jsonl (git diff) and run `gf log accept` from your own terminal if the change is legitimate."
	case errors.Is(err, eventlog.ErrDivergedStream):
		return "The event log .gofast/events.jsonl has the same work advanced on two branches, which usually comes from a merge. Resolve it by hand, keeping one branch's events for that work. " + seeLog
	case errors.As(err, &malformed):
		return fmt.Sprintf("The event log .gofast/events.jsonl is damaged at line %d. Restore it from git. %s", malformed.Line, seeLog)
	case errors.As(err, &config):
		return fmt.Sprintf("The exit-check configuration %s is invalid: %s. The owner fixes it; `gf checks` shows the result.", exitchecks.ConfigPath, config.Problem)
	case errors.As(err, &nfrs) && nfrs.Path != "":
		return fmt.Sprintf("The NFR proposals %s are invalid: %s. Reject the design with `gf reject \"<feedback>\"` so it fixes them.", nfrs.Path, nfrs.Problem)
	case errors.As(err, &nfrs):
		return fmt.Sprintf("The NFRs %s are invalid: %s. The owner fixes them; `gf checks` shows the result.", exitchecks.NFRsPath, nfrs.Problem)
	case errors.Is(err, domain.ErrNFRAdoptionClosed):
		return "NFR proposals can only be adopted while design waits for your approval: after design submits, before `gf approve`. Run `gf status` to see where the work is."
	case errors.Is(err, exitchecks.ErrNoNFRProposals):
		return "This design visit proposes no NFRs: its nfrs.proposed-v<visit>.json does not exist. There is nothing to adopt."
	case errors.Is(err, eventlog.ErrVersionConflict):
		return "The workflow changed while this command ran. Run it again."
	case errors.Is(err, domain.ErrInconsistentHistory):
		return "The event log holds a history gf cannot replay. Restore .gofast/events.jsonl from git. " + seeLog
	case !ran:
		// Cobra rejected the flags or arguments before the command ran.
		msg := err.Error()
		return fmt.Sprintf("%s%s.\nSee `%s --help`.", strings.ToUpper(msg[:1]), msg[1:], cmd.CommandPath())
	default:
		return "Something went wrong. " + seeLog
	}
}

func stageNames(stages []domain.Stage) string {
	names := make([]string, len(stages))
	for i, s := range stages {
		names[i] = s.String()
	}
	return strings.Join(names, ", ")
}
