// Package reportimprovementnotapplied is `gf improvement not-applied
// <id>`: the improvement agent says it did not apply the improvement in
// progress, because it contradicts the skill's current text (--conflict)
// or asks for anything but an edit to the skill's wording (--refused).
// gf records it with the reason, so it is not retried until the owner
// deletes its line, and records it in the history, which it then syncs.
package reportimprovementnotapplied

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/pluginskills"
)

// maxReason is the most characters a reason holds (the
// skill-improvement.not-applied contract).
const maxReason = 4000

func NewCommand(openStore func() (*eventlog.Store, error), repositoryRoot func() (string, error), getenv func(string) string,
	actor func() eventlog.Actor, afterRecord func(cmd *cobra.Command)) *cobra.Command {
	var conflict, refused string
	cmd := &cobra.Command{
		Use:   "not-applied <improvement-id> (--conflict \"<reason>\" | --refused \"<reason>\")",
		Short: "Record that the improvement in progress was not applied, and why",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := domain.ParseSkillImprovementID(args[0])
			if err != nil {
				return &pluginskills.Error{Message: fmt.Sprintf("%q is not a skill improvement's id: they look like si-1a2b.", args[0])}
			}
			cause, text, err := causeOf(cmd, conflict, refused)
			if err != nil {
				return err
			}
			reason, err := domain.NewReason(strings.Join(strings.Fields(text), " "))
			if err != nil {
				return &pluginskills.Error{Message: "Say why it was not applied: `gf improvement not-applied " + string(id) + " --conflict|--refused \"<reason>\"`."}
			}
			if utf8.RuneCountInString(reason.String()) > maxReason {
				return &pluginskills.Error{Message: fmt.Sprintf("A reason holds at most %d characters.", maxReason)}
			}
			session, err := pluginskills.Session(getenv)
			if err != nil {
				return err
			}
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			plugin, err := pluginskills.PluginRoot(root, getenv)
			if err != nil {
				return err
			}
			skill, ok, err := pluginskills.FindRecorded(plugin, id)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("report %s not applied: %w", id, domain.ErrImprovementNotInProgress)
			}
			events, err := pluginskills.Change(plugin, skill, false, func(s domain.PluginSkill) ([]domain.Event, error) {
				return domain.ReportImprovementNotApplied(s, id, session, cause, reason)
			})
			if err != nil {
				return err
			}
			if err := pluginskills.RecordInHistory(openStore, actor, events); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s is recorded as not applied in the skill's record, but gf could not add it to the history: %v\n", id, err)
			}
			if len(events) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s was already recorded as not applied to %s.\n", id, skill)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s as not applied to %s; the skill is unchanged. Deleting its line from %s retries it at the next startup.\n",
				id, skill, pluginskills.RecordPath(plugin, skill))
			afterRecord(cmd)
			return nil
		},
	}
	cmd.Flags().StringVar(&conflict, "conflict", "", "the improvement contradicts the skill's current text: say how")
	cmd.Flags().StringVar(&refused, "refused", "", "the improvement asks for anything but an edit to the skill's wording: say what")
	return cmd
}

// causeOf is the one cause the flags give.
func causeOf(cmd *cobra.Command, conflict, refused string) (domain.NotAppliedCause, string, error) {
	c, r := cmd.Flags().Changed("conflict"), cmd.Flags().Changed("refused")
	switch {
	case c && !r:
		return domain.CauseConflict, conflict, nil
	case r && !c:
		return domain.CauseRefused, refused, nil
	}
	return 0, "", &pluginskills.Error{Message: "Say why it was not applied, with one of --conflict \"<reason>\" or --refused \"<reason>\"."}
}
