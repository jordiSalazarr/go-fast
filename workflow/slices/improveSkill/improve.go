// Package improveskill is `gf improvement applied <id>`: the improvement
// agent says it applied the improvement in progress (or that the skill
// already held it). gf computes the skill's version after the edit itself,
// writes the record, and records Skill improved in the repository's
// history, which it then syncs.
package improveskill

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/pluginskills"
)

func NewCommand(openStore func() (*eventlog.Store, error), repositoryRoot func() (string, error), getenv func(string) string,
	actor func() eventlog.Actor, afterRecord func(cmd *cobra.Command)) *cobra.Command {
	var summary string
	cmd := &cobra.Command{
		Use:   "applied <improvement-id> [--summary \"<one line>\"]",
		Short: "Record that the improvement in progress was applied, or that the skill already held it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := domain.ParseSkillImprovementID(args[0])
			if err != nil {
				return &pluginskills.Error{Message: fmt.Sprintf("%q is not a skill improvement's id: they look like si-1a2b.", args[0])}
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
				return fmt.Errorf("apply %s: %w", id, domain.ErrImprovementNotInProgress)
			}
			after, err := pluginskills.Version(plugin, skill)
			if err != nil {
				return err
			}
			line := strings.Join(strings.Fields(summary), " ")
			events, err := pluginskills.Change(plugin, skill, false, func(s domain.PluginSkill) ([]domain.Event, error) {
				return domain.ImproveSkill(s, id, session, after, line)
			})
			if err != nil {
				return err
			}
			if err := pluginskills.RecordInHistory(openStore, actor, events); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s is recorded as applied in the skill's record, but gf could not add it to the history: %v\n", id, err)
			}
			for _, e := range events {
				if improved, ok := e.(domain.SkillImproved); ok {
					change := "changed it from " + improved.Before.Short() + " to " + improved.After.Short()
					if improved.Before == improved.After {
						change = "the skill already held it, so nothing changed"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s as applied to %s: %s.\n", id, skill, change)
				}
			}
			if len(events) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s was already recorded as applied to %s.\n", id, skill)
				return nil
			}
			afterRecord(cmd)
			return nil
		},
	}
	cmd.Flags().StringVar(&summary, "summary", "", "one line saying what the improvement changed in the skill")
	return cmd
}
