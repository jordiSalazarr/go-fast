// Command gf keeps work on its path of stages, with approval gates and
// attempt budgets.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/decisions"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/gitbranch"
	abandonwork "github.com/jordiSalazarr/go-fast/workflow/slices/abandonWork"
	accepteventlog "github.com/jordiSalazarr/go-fast/workflow/slices/acceptEventLog"
	adoptnfrproposals "github.com/jordiSalazarr/go-fast/workflow/slices/adoptNFRProposals"
	"github.com/jordiSalazarr/go-fast/workflow/slices/approve"
	briefagentonsessionstart "github.com/jordiSalazarr/go-fast/workflow/slices/briefAgentOnSessionStart"
	"github.com/jordiSalazarr/go-fast/workflow/slices/checks"
	extendbudget "github.com/jordiSalazarr/go-fast/workflow/slices/extendBudget"
	extendreworkbudget "github.com/jordiSalazarr/go-fast/workflow/slices/extendReworkBudget"
	guardagentactions "github.com/jordiSalazarr/go-fast/workflow/slices/guardAgentActions"
	keepagentonstage "github.com/jordiSalazarr/go-fast/workflow/slices/keepAgentOnStage"
	observeskillloads "github.com/jordiSalazarr/go-fast/workflow/slices/observeSkillLoads"
	overridestressdecision "github.com/jordiSalazarr/go-fast/workflow/slices/overrideStressDecision"
	"github.com/jordiSalazarr/go-fast/workflow/slices/reject"
	returnwork "github.com/jordiSalazarr/go-fast/workflow/slices/returnWork"
	startwork "github.com/jordiSalazarr/go-fast/workflow/slices/startWork"
	"github.com/jordiSalazarr/go-fast/workflow/slices/status"
	submitforacceptance "github.com/jordiSalazarr/go-fast/workflow/slices/submitForAcceptance"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

func main() {
	os.Exit(execute(os.Args[1:], os.Getenv, caller.StdinIsTerminal, os.Stdin, os.Stdout, os.Stderr))
}

// app holds what the commands share, resolved lazily once flags are parsed.
type app struct {
	getenv   func(string) string
	terminal func() bool // whether stdin is a terminal
	dir      string      // --dir
	root     string
	store    *eventlog.Store
	logger   *slog.Logger
	logFile  *os.File
	ran      bool // a command's RunE started: flags and arguments were valid
}

// execute runs gf and returns its exit code.
func execute(args []string, getenv func(string) string, terminal func() bool, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{getenv: getenv, terminal: terminal}
	defer a.close()

	root := a.rootCommand()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	cmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	if a.openLogger(false) == nil {
		a.logger.Error("command failed", "command", cmd.CommandPath(), "args", args, "error", err.Error())
	}
	fmt.Fprintln(stderr, friendly(err, cmd, a.ran))
	return 1
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:               "gf",
		Short:             "gofast keeps work on its path of stages",
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.PersistentFlags().StringVar(&a.dir, "dir", "", "repository root (default: the git repository containing the working directory)")
	root.AddCommand(
		startwork.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch, a.repositoryRoot, exitchecks.LoadNFRs),
		submitforacceptance.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch, a.changedFiles, a.scopeFacts, a.runChecks, a.stressFacts, a.baseline()),
		approve.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch),
		reject.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch),
		extendbudget.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch),
		extendreworkbudget.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch),
		overridestressdecision.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch, a.stressFacts),
		abandonwork.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch),
		returnwork.NewCommand(a.openStore, a.automations(), a.resolveCaller, a.currentBranch),
		adoptnfrproposals.NewCommand(a.openStore, a.resolveCaller, a.currentBranch, a.repositoryRoot),
		status.NewCommand(a.openStore, a.currentBranch, a.repositoryRoot),
		checks.NewCommand(a.repositoryRoot, a.getenv),
		accepteventlog.NewCommand(a.openStore, a.resolveCaller),
		a.hookCommand(),
	)
	a.markRun(root)
	return root
}

// hookCommand groups the Claude Code hook handlers the gofast plugin runs.
func (a *app) hookCommand() *cobra.Command {
	hook := &cobra.Command{
		Use:   "hook <event>",
		Short: "Claude Code hook handlers, run by the gofast plugin",
	}
	dir := func() string { return a.dir }
	hook.AddCommand(
		guardagentactions.NewCommand(dir, a.getenv, gitbranch.Current),
		briefagentonsessionstart.NewCommand(dir, a.getenv, gitbranch.Current),
	)
	ensureBaseline := func(root string, id domain.AssignmentID) (bool, error) {
		return worktree.OpenBaselines(root).Ensure(id)
	}
	hook.AddCommand(keepagentonstage.NewCommands(dir, a.getenv, gitbranch.Current, ensureBaseline)...)
	hook.AddCommand(observeskillloads.NewCommands(dir, a.getenv, gitbranch.Current)...)
	return hook
}

// markRun wraps every runnable command so execute knows whether cobra
// accepted the flags and arguments before an error happened.
func (a *app) markRun(c *cobra.Command) {
	if run := c.RunE; run != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			a.ran = true
			return run(cmd, args)
		}
	}
	for _, sub := range c.Commands() {
		a.markRun(sub)
	}
}

func (a *app) repositoryRoot() (string, error) {
	if a.root != "" {
		return a.root, nil
	}
	if a.dir != "" {
		abs, err := filepath.Abs(a.dir)
		if err != nil {
			return "", fmt.Errorf("resolve --dir %s: %w", a.dir, err)
		}
		a.root = abs
		return a.root, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	a.root, err = eventlog.FindRoot(wd)
	return a.root, err
}

// openLogger opens .gofast/gf.log. With create unset it only logs into an
// existing .gofast/, so a mistyped command never creates one.
func (a *app) openLogger(create bool) error {
	if a.logger != nil {
		return nil
	}
	root, err := a.repositoryRoot()
	if err != nil {
		return err
	}
	if create {
		if err := eventlog.Init(root); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(eventlog.LogFile(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	a.logFile = f
	a.logger = slog.New(slog.NewTextHandler(f, nil))
	return nil
}

func (a *app) openStore() (*eventlog.Store, error) {
	if a.store != nil {
		return a.store, nil
	}
	if err := a.openLogger(true); err != nil {
		return nil, err
	}
	store, err := eventlog.Open(a.root, eventlog.WithLogger(a.logger), eventlog.WithSeal(gitbranch.NewCommits(a.root)))
	if err != nil {
		return nil, err
	}
	a.store = store
	return store, nil
}

// automations records each opened assignment's working-tree baseline, and
// refreshes the decisions index at stage boundaries. It runs inside a
// command, after openStore, so root and logger are set. A baseline that
// cannot be recorded is logged; submit then takes one late. An index that
// cannot be refreshed is logged; the next boundary tries again.
func (a *app) automations() automations.Runner {
	return automations.Runner{
		AssignmentOpened: func(id domain.AssignmentID) {
			if _, err := worktree.OpenBaselines(a.root).Ensure(id); err != nil {
				a.logger.Error("could not record the baseline of an assignment", "assignment", id.String(), "error", err.Error())
			}
		},
		Boundary: a.refreshDecisionsIndex,
	}
}

// refreshDecisionsIndex regenerates the decisions directory's README.md
// when the branch is between stages and the index would change.
func (a *app) refreshDecisionsIndex(history eventlog.History) {
	err := func() error {
		branch, err := gitbranch.Current(a.root)
		if err != nil {
			return err
		}
		config, err := exitchecks.LoadConfig(a.root)
		if err != nil {
			return err
		}
		readGrill := func(p domain.RepoPath) (domain.Grill, error) { return exitchecks.LoadGrill(a.root, p) }
		_, err = decisions.Refresh(a.root, config.Decisions, history, branch, readGrill)
		return err
	}()
	if err != nil {
		a.logger.Error("could not refresh the decisions index", "error", err.Error())
	}
}

func (a *app) changedFiles(id domain.AssignmentID) (domain.ChangedFiles, bool, error) {
	root, err := a.repositoryRoot()
	if err != nil {
		return domain.ChangedFiles{}, false, err
	}
	return worktree.OpenBaselines(root).ChangedSince(id)
}

// decisionsDir is the repository's decisions directory, as its check
// configuration says.
func (a *app) decisionsDir() (domain.DecisionsDir, error) {
	root, err := a.repositoryRoot()
	if err != nil {
		return domain.DecisionsDir{}, err
	}
	config, err := exitchecks.LoadConfig(root)
	if err != nil {
		return domain.DecisionsDir{}, err
	}
	return config.Decisions, nil
}

// scopeFacts is what a submission's write scope depends on: the decisions
// directory and, on a refactor's implement, the plan as the owner accepted
// it.
func (a *app) scopeFacts(target submitforacceptance.Target) (domain.ScopeFacts, error) {
	dir, err := a.decisionsDir()
	if err != nil {
		return domain.ScopeFacts{}, err
	}
	return submitforacceptance.PlanFacts(target, dir, func(work domain.WorkID, visit domain.Visit, end domain.AssignmentID) (domain.RefactorPlan, error) {
		return exitchecks.AcceptedRefactorPlan(a.root, work, visit, end)
	})
}

// baseline reads what a measure visit measured, and keeps it once the
// work recorded it as its baseline.
func (a *app) baseline() submitforacceptance.Baseline {
	return submitforacceptance.Baseline{
		Measure: func(target submitforacceptance.Target) ([]domain.NFRResult, []domain.FrozenScript, error) {
			measured, err := exitchecks.MeasuredBaseline(a.root, target.Open.ID(), target.Work().TargetNFRs())
			return measured.Results, measured.Scripts, err
		},
		Keep: func(target submitforacceptance.Target) error {
			measured, err := exitchecks.MeasuredBaseline(a.root, target.Open.ID(), target.Work().TargetNFRs())
			if err != nil {
				return err
			}
			return exitchecks.WriteBaseline(a.root, target.Open.Work(), measured.Raw)
		},
	}
}

// stressFacts reads the repository's NFRs and the files the work changed
// since its first stage visit began.
func (a *app) stressFacts(work domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error) {
	root, err := a.repositoryRoot()
	if err != nil {
		return nil, domain.ChangedFiles{}, err
	}
	nfrs, err := exitchecks.LoadNFRs(root)
	if err != nil {
		return nil, domain.ChangedFiles{}, err
	}
	changed, err := worktree.OpenBaselines(root).WorkChanges(work.FirstAssignment())
	if err != nil {
		return nil, domain.ChangedFiles{}, err
	}
	return nfrs, changed, nil
}

// runChecks runs a stage visit's exit checks in the repository, with the
// environment gf itself was given.
func (a *app) runChecks(v exitchecks.Visit, progress io.Writer) ([]domain.CheckResult, error) {
	root, err := a.repositoryRoot()
	if err != nil {
		return nil, err
	}
	config, err := exitchecks.LoadConfig(root)
	if err != nil {
		return nil, err
	}
	return exitchecks.Runner{Root: root, Config: config, Env: os.Environ(), Progress: progress}.Run(v)
}

func (a *app) resolveCaller() (caller.Caller, error) {
	root, err := a.repositoryRoot()
	if err != nil {
		return caller.Caller{}, err
	}
	return caller.Resolve(a.getenv, caller.GitConfig(root), a.terminal())
}

func (a *app) currentBranch() (domain.Branch, error) {
	root, err := a.repositoryRoot()
	if err != nil {
		return domain.Branch{}, err
	}
	return gitbranch.Current(root)
}

func (a *app) close() {
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
}
