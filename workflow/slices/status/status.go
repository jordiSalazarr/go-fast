// Package status is the `gf status` query: where the current branch's work
// stands and what happens next, as text or JSON. The view itself is the
// progress read model.
package status

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
	"github.com/jordiSalazarr/go-fast/workflow/remote"
)

func NewCommand(openStore func() (*eventlog.Store, error), currentBranch func() (domain.Branch, error), repositoryRoot func() (string, error)) *cobra.Command {
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
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			var view progress.View
			err = store.Shared(func(s *eventlog.Snapshot) error {
				view, err = progress.Read(s, branch, progress.FactsAt(root))
				if err != nil {
					return err
				}
				progress.WarnIfChanged(&view, s)
				records, err := s.ReadAll()
				if err != nil {
					return err
				}
				view.Sync = syncView(root, records)
				return nil
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

// syncView is the sync state; a configuration gf can't use doesn't stop
// gf status.
func syncView(root string, records []eventlog.Recorded) *progress.SyncView {
	state, err := remote.StateOf(root, records)
	var invalid *remote.ConfigError
	if errors.As(err, &invalid) {
		return &progress.SyncView{Problem: remote.ConfigPath + " is invalid."}
	}
	if err != nil {
		return &progress.SyncView{Problem: "gf could not read " + remote.ConfigPath + "."}
	}
	view := &progress.SyncView{Configured: state.Configured, Pending: state.Pending}
	if state.Synced {
		at := state.SyncedAt
		view.SyncedAt = &at
	}
	return view
}
