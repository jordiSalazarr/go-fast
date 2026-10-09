package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/gitbranch"
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
	)
	switch {
	case errors.Is(err, gitbranch.ErrDetachedHead):
		return "You are not on a branch. Check out a branch before running gf."
	case errors.As(err, &ownerOnly) && ownerOnly.NoTerminal:
		return fmt.Sprintf("Only the owner can %s, from their own terminal. Run `%s` there.", ownerOnly.Action, ownerOnly.Command)
	case errors.As(err, &ownerOnly):
		return fmt.Sprintf("Only the owner can %s. Ask the owner to run `%s`.", ownerOnly.Action, ownerOnly.Command)
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
		return "There is no active work on this branch. Start one with `gf start --type fix-bug \"<description>\"`."
	case errors.Is(err, domain.ErrNotAwaitingSubmission):
		return "There is nothing to submit right now. Run `gf status` to see what happens next."
	case errors.Is(err, domain.ErrNotAwaitingApproval):
		return "Nothing is waiting for approval. Run `gf status` to see what happens next."
	case errors.Is(err, domain.ErrNotEscalated):
		return "The budget can only be extended when a stage is escalated. Run `gf status` to see what happens next."
	case errors.Is(err, domain.ErrUnknownWorkType):
		names := make([]string, 0, len(domain.WorkTypes()))
		for _, t := range domain.WorkTypes() {
			names = append(names, t.String())
		}
		return fmt.Sprintf("Unknown work type. Available: %s.", strings.Join(names, ", "))
	case errors.Is(err, domain.ErrEmptyDescription):
		return "Describe the work: `gf start --type fix-bug \"<description>\"`."
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
