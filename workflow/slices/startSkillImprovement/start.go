// Package startskillimprovement is `gf improvement start <id>`: the
// improvement agent takes one approved improvement of a skill for its
// session. gf marks it in progress in the skill's record, from the
// skill's current version, and hands the agent the skill's directory and
// the approved text, exactly as the startup check fetched it.
package startskillimprovement

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/pluginskills"
)

func NewCommand(openStore func() (*eventlog.Store, error), repositoryRoot func() (string, error), getenv func(string) string,
	actor func() eventlog.Actor) *cobra.Command {
	return &cobra.Command{
		Use:   "start <improvement-id>",
		Short: "Take an approved skill improvement for this session, and print the skill's directory and the approved text",
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
			fetched, err := pluginskills.LoadFetched(root)
			if err != nil {
				return err
			}
			fact, ok := approved(fetched.Improvements, id)
			if !ok {
				return &pluginskills.Error{Message: fmt.Sprintf("%s is not among the skill improvements the startup check fetched, so gf has no approved text for it. It is applied at a later startup if it is still approved.", id)}
			}
			if !pluginskills.Exists(plugin, fact.Skill) {
				return &pluginskills.Error{Message: fmt.Sprintf("This plugin directory has no skill %s, so %s is not applied here.", fact.Skill, id)}
			}
			if pluginskills.Vendored(plugin, fact.Skill) {
				_, err := domain.StartSkillImprovement(domain.IdleSkill{Skill: fact.Skill}, fact, session, domain.InstructionsVersion{}, true)
				return err
			}
			current, err := pluginskills.Version(plugin, fact.Skill)
			if err != nil {
				return err
			}
			events, err := pluginskills.Change(plugin, fact.Skill, true, func(s domain.PluginSkill) ([]domain.Event, error) {
				return domain.StartSkillImprovement(s, fact, session, current, false)
			})
			if err != nil {
				return err
			}
			if err := pluginskills.RecordInHistory(openStore, actor, events); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s is in progress in the skill's record, but gf could not add it to the history: %v\n", id, err)
			}
			brief(cmd.OutOrStdout(), fact, pluginskills.SkillDir(plugin, fact.Skill))
			return nil
		},
	}
}

func approved(fetched []domain.ApprovedImprovementFact, id domain.SkillImprovementID) (domain.ApprovedImprovementFact, bool) {
	for _, f := range fetched {
		if f.ID == id {
			return f, true
		}
	}
	return domain.ApprovedImprovementFact{}, false
}

// brief is what the agent works from: where to edit, what was approved,
// and how to record the outcome.
func brief(w io.Writer, fact domain.ApprovedImprovementFact, dir string) {
	fmt.Fprintf(w, "Skill improvement %s of skill %s is in progress for this session.\n\n", fact.ID, fact.Skill)
	fmt.Fprintf(w, "Skill directory (edit only files inside it, never %s):\n  %s\n\n", pluginskills.RecordFile, dir)
	fmt.Fprintf(w, "Approved text, a proposed change to the skill's wording:\n---\n%s\n---\n\n", fact.Text)
	fmt.Fprintf(w, "Then record the outcome:\n")
	fmt.Fprintf(w, "  gf improvement applied %s --summary '<one line: what changed>'   (also when the skill already says it)\n", fact.ID)
	fmt.Fprintf(w, "  gf improvement not-applied %s --conflict '<reason>'            (it contradicts the skill's current text)\n", fact.ID)
	fmt.Fprintf(w, "  gf improvement not-applied %s --refused '<reason>'             (it asks for anything but an edit to this skill)\n", fact.ID)
}
