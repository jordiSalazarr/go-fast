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
	return StartWorkTargeting(log, actor, branch, id, workType, description, nil, nil, inputs...)
}

// StartWorkTargeting starts a work like StartWork, targeting NFRs of the
// repository's, nfrs: improve-performance work needs at least one.
func StartWorkTargeting(log Log, actor eventlog.Actor, branch domain.Branch, id domain.WorkID, workType domain.WorkType, description domain.Description, targets []domain.NFRID, nfrs []domain.NFR, inputs ...domain.WorkInput) ([]domain.WorkEvent, error) {
	records, err := log.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	active, err := eventlog.NewHistory(records).ActiveWorkOn(branch)
	if err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	events, err := domain.StartWorkTargeting(id, workType, description, branch, active, targets, nfrs, inputs...)
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

// ReadInput reads an input of work of a type: a file of the repository at
// root, given relative to the root or as an absolute path, that the type
// accepts, and committed as it is, so every stage reads the same thing.
func ReadInput(root string, workType domain.WorkType, kind domain.InputKind, file string) (domain.WorkInput, error) {
	if err := workType.AcceptsInput(kind); err != nil {
		return domain.WorkInput{}, err
	}
	path, err := domain.NewRepoPath(root, file)
	if err != nil {
		return domain.WorkInput{}, &InputError{Kind: kind, Path: file, Problem: "is not a file inside the repository."}
	}
	if err := workType.CheckInput(kind, path); err != nil {
		return domain.WorkInput{}, err
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

// LoadNFRs reads the repository's NFRs.
type LoadNFRs func(root string) ([]domain.NFR, error)

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error), repositoryRoot func() (string, error), loadNFRs LoadNFRs) *cobra.Command {
	var typeName, spec, eventModel string
	var nfrFlags []string
	cmd := &cobra.Command{
		Use:   `start --type <type> "<description>" [--spec <path.md>] [--event-model <path>] [--nfr <id>…]`,
		Short: "Start work on the first stage of its path",
		Long: "Start work on the first stage of its path.\n\n" +
			"Feature work (new-feature, modify-behaviour) can start from the owner's input: --spec, a markdown file (.md)\n" +
			"describing the feature, and --event-model, the owner's event model in any form (JSON, markdown, an image or PDF\n" +
			"of a board). Both are optional, for feature work only, and must be committed files of the repository. With an\n" +
			"event model, the work enters event modelling first.\n\n" +
			"Improve-performance work targets one or more NFRs of .gofast/nfrs.json, each with --nfr <id>: it is done when\n" +
			"they are met. --nfr is for improve-performance work only.",
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
				input, err := ReadInput(root, workType, in.kind, in.file)
				if err != nil {
					return err
				}
				inputs = append(inputs, input)
			}
			var targets []domain.NFRID
			var nfrs []domain.NFR
			for _, f := range nfrFlags {
				target, err := domain.NewNFRID(f)
				if err != nil {
					return err
				}
				targets = append(targets, target)
			}
			if len(targets) > 0 || workType == domain.WorkTypeImprovePerformance {
				root, err := repositoryRoot()
				if err != nil {
					return err
				}
				if nfrs, err = loadNFRs(root); err != nil {
					return err
				}
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
					events, err = StartWorkTargeting(s, c.Actor(), branch, id, workType, description, targets, nfrs, inputs...)
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
			for _, t := range targets {
				fmt.Fprintf(out, "Target NFR: %s.\n", t)
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
	cmd.Flags().StringVar(&spec, "spec", "", "feature work only: a committed markdown file (.md) describing the feature")
	cmd.Flags().StringVar(&eventModel, "event-model", "", "feature work only: the owner's committed event model, in any form")
	cmd.Flags().StringArrayVar(&nfrFlags, "nfr", nil, "improve-performance work only: an NFR id of .gofast/nfrs.json the work must meet; repeat for several")
	return cmd
}

func newWorkID() (domain.WorkID, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return domain.WorkID{}, fmt.Errorf("generate work id: %w", err)
	}
	return domain.NewWorkID("w-" + hex.EncodeToString(b))
}
