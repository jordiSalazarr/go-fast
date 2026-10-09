// Package status is the `gf status` query: where the current branch's work
// stands and what happens next, as text or JSON. The view itself is the
// progress read model.
package status

import (
	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

func NewCommand(openStore func() (*eventlog.Store, error), currentBranch func() (domain.Branch, error)) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [--json]",
		Short: "Show the active work, its path and what happens next",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			branch, err := currentBranch()
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			var view progress.View
			err = store.Shared(func(s *eventlog.Snapshot) error {
				view, err = progress.Read(s, branch)
				progress.WarnIfChanged(&view, s)
				return err
			})
			if err != nil {
				return err
			}
			if asJSON {
				return progress.RenderJSON(cmd.OutOrStdout(), view)
			}
			progress.RenderText(cmd.OutOrStdout(), view)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print a stable JSON document")
	return cmd
}
