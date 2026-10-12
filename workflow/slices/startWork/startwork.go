// Package startwork starts a new work on the first stage of its path it
// enters, from the owner's inputs: a spec, an event model, or neither.
package startwork

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// Log is what starting work needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// StartWork starts a work on a branch, from its inputs, unless the branch
// has active work.
func StartWork(log Log, actor eventlog.Actor, branch domain.Branch, id domain.WorkID, workType domain.WorkType, description domain.Description, inputs ...domain.WorkInput) ([]domain.WorkEvent, error) {
	records, err := log.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	active, err := eventlog.NewHistory(records).ActiveWorkOn(branch)
	if err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	events, err := domain.StartWork(id, workType, description, branch, active, inputs...)
	if err != nil {
		return nil, err
	}
	if err := log.Append(eventlog.WorkStream(id), 0, actor, eventlog.Events(events)...); err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	return events, nil
}

// InputError says why a file can't be a work's input. Its text is meant for
// the owner.
type InputError struct {
	Kind    domain.InputKind
	Path    string
	Problem string
}

func (e *InputError) Error() string {
	return fmt.Sprintf("The %s %s %s", e.Kind, e.Path, e.Problem)
}

// ReadInput reads a work input: a file of the repository at root, given
// relative to the root or as an absolute path, and committed as it is, so
// every stage reads the same thing.
func ReadInput(root string, kind domain.InputKind, file string) (domain.WorkInput, error) {
	path, err := domain.NewRepoPath(root, file)
	if err != nil {
		return domain.WorkInput{}, &InputError{Kind: kind, Path: file, Problem: "is not a file inside the repository."}
	}
	content, err := worktree.CommittedFile(root, path)
	switch {
	case errors.Is(err, worktree.ErrNotAFile):
		return domain.WorkInput{}, &InputError{Kind: kind, Path: path.String(), Problem: "is not a file inside the repository."}
	case errors.Is(err, worktree.ErrNotCommitted):
		return domain.WorkInput{}, &InputError{Kind: kind, Path: path.String(),
			Problem: "is not committed. Commit it before starting the work, so every stage reads the same thing."}
	case err != nil:
		return domain.WorkInput{}, fmt.Errorf("read %s input: %w", kind, err)
	}
	return domain.NewWorkInput(kind, path, domain.HashOf(content))
}

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error), repositoryRoot func() (string, error)) *cobra.Command {
	var typeName, spec, eventModel string
	cmd := &cobra.Command{
		Use:   `start --type <fix-bug|new-feature|modify-behaviour> "<description>" [--spec <path.md>] [--event-model <path>]`,
		Short: "Start work on the first stage of its path",
		Long: "Start work on the first stage of its path.\n\n" +
			"Feature work (new-feature, modify-behaviour) can start from the owner's input: --spec, a markdown file describing\n" +
			"the feature, and --event-model, the owner's event model in any form (JSON, markdown, an image or PDF of a board).\n" +
			"Both are optional and must be committed files of the repository. With an event model, the work enters event\n" +
			"modelling first.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workType, err := domain.ParseWorkType(typeName)
			if err != nil {
				return err
			}
			description, err := domain.NewDescription(args[0])
			if err != nil {
				return err
			}
			id, err := newWorkID()
			if err != nil {
				return err
			}
			var inputs []domain.WorkInput
			for _, in := range []struct {
				kind domain.InputKind
				file string
			}{{domain.InputSpec, spec}, {domain.InputEventModel, eventModel}} {
				if in.file == "" {
					continue
				}
				root, err := repositoryRoot()
				if err != nil {
					return err
				}
				input, err := ReadInput(root, in.kind, in.file)
				if err != nil {
					return err
				}
				inputs = append(inputs, input)
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
			var events []domain.WorkEvent
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automate.AroundCommand(s, func() error {
					events, err = StartWork(s, c.Actor(), branch, id, workType, description, inputs...)
					return err
				})
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Started %s work %q (%s) on branch %s.\n", workType, description, id, branch)
			for _, in := range inputs {
				fmt.Fprintf(out, "Input: %s %s (sha256 %s).\n", in.Kind(), in.Path(), in.Hash().String()[:12])
			}
			for _, e := range events {
				if entered, ok := e.(domain.StageEntered); ok {
					fmt.Fprintf(out, "Current stage: %s. Run `gf status` to see what happens next.\n", entered.Stage)
				}
			}
			return nil
		},
	}
	types := make([]string, 0, len(domain.WorkTypes()))
	for _, t := range domain.WorkTypes() {
		types = append(types, t.String())
	}
	cmd.Flags().StringVar(&typeName, "type", "", "work type (available: "+strings.Join(types, ", ")+")")
	_ = cmd.MarkFlagRequired("type")
	cmd.Flags().StringVar(&spec, "spec", "", "a committed markdown file describing the feature")
	cmd.Flags().StringVar(&eventModel, "event-model", "", "the owner's committed event model, in any form (feature work only)")
	return cmd
}

func newWorkID() (domain.WorkID, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return domain.WorkID{}, fmt.Errorf("generate work id: %w", err)
	}
	return domain.NewWorkID("w-" + hex.EncodeToString(b))
}
