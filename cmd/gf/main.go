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
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	abandonwork "github.com/jordiSalazarr/go-fast/workflow/slices/abandonWork"
	"github.com/jordiSalazarr/go-fast/workflow/slices/approve"
	extendbudget "github.com/jordiSalazarr/go-fast/workflow/slices/extendBudget"
	"github.com/jordiSalazarr/go-fast/workflow/slices/reject"
	startwork "github.com/jordiSalazarr/go-fast/workflow/slices/startWork"
	"github.com/jordiSalazarr/go-fast/workflow/slices/status"
	submitforacceptance "github.com/jordiSalazarr/go-fast/workflow/slices/submitForAcceptance"
)

func main() {
	os.Exit(execute(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// app holds what the commands share, resolved lazily once flags are parsed.
type app struct {
	getenv  func(string) string
	dir     string // --dir
	root    string
	store   *eventlog.Store
	logger  *slog.Logger
	logFile *os.File
	ran     bool // a command's RunE started: flags and arguments were valid
}

// execute runs gf and returns its exit code.
func execute(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	a := &app{getenv: getenv}
	defer a.close()

	root := a.rootCommand()
	root.SetArgs(args)
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
		startwork.NewCommand(a.openStore, a.resolveCaller),
		submitforacceptance.NewCommand(a.openStore, a.resolveCaller),
		approve.NewCommand(a.openStore, a.resolveCaller),
		reject.NewCommand(a.openStore, a.resolveCaller),
		extendbudget.NewCommand(a.openStore, a.resolveCaller),
		abandonwork.NewCommand(a.openStore, a.resolveCaller),
		status.NewCommand(a.openStore),
	)
	for _, c := range root.Commands() {
		run := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			a.ran = true
			return run(cmd, args)
		}
	}
	return root
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
	return caller.Resolve(a.getenv, caller.GitConfig(root))
}

func (a *app) close() {
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
}
