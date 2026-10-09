// Package accepteventlog lets the owner accept an event log changed outside
// gf, after checking the change: `gf log accept`.
package accepteventlog

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error)) *cobra.Command {
	log := &cobra.Command{
		Use:   "log",
		Short: "Look after the event log",
	}
	log.AddCommand(&cobra.Command{
		Use:   "accept",
		Short: "Accept the event log as it is now, after checking a change made outside gf (owner only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			if _, err := c.RequireOwner("accept the event log", "gf log accept"); err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			n, err := store.Accept()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Accepted the event log as it is now (%d events). Run `gf status` to see what happens next.\n", n)
			return nil
		},
	})
	return log
}
