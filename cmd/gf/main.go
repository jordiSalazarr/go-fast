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

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/gitbranch"
	abandonwork "github.com/jordiSalazarr/go-fast/workflow/slices/abandonWork"
	"github.com/jordiSalazarr/go-fast/workflow/slices/approve"
	briefagentonsessionstart "github.com/jordiSalazarr/go-fast/workflow/slices/briefAgentOnSessionStart"
	extendbudget "github.com/jordiSalazarr/go-fast/workflow/slices/extendBudget"
	guardagentactions "github.com/jordiSalazarr/go-fast/workflow/slices/guardAgentActions"
	keepagentonstage "github.com/jordiSalazarr/go-fast/workflow/slices/keepAgentOnStage"
	"github.com/jordiSalazarr/go-fast/workflow/slices/reject"
	startwork "github.com/jordiSalazarr/go-fast/workflow/slices/startWork"
	"github.com/jordiSalazarr/go-fast/workflow/slices/status"
	submitforacceptance "github.com/jordiSalazarr/go-fast/workflow/slices/submitForAcceptance"
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
		startwork.NewCommand(a.openStore, a.resolveCaller, a.currentBranch),
		submitforacceptance.NewCommand(a.openStore, a.resolveCaller, a.currentBranch),
		approve.NewCommand(a.openStore, a.resolveCaller, a.currentBranch),
		reject.NewCommand(a.openStore, a.resolveCaller, a.currentBranch),
		extendbudget.NewCommand(a.openStore, a.resolveCaller, a.currentBranch),
		abandonwork.NewCommand(a.openStore, a.resolveCaller, a.currentBranch),
		status.NewCommand(a.openStore, a.currentBranch),
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
	hook.AddCommand(keepagentonstage.NewCommands(dir, a.getenv, gitbranch.Current)...)
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
	store, err := eventlog.Open(a.root, eventlog.WithLogger(a.logger))
	if err != nil {
		return nil, err
	}
	a.store = store
	return store, nil
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
