package exitchecks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// Visit is the stage visit whose exit checks run.
type Visit struct {
	Assignment domain.AssignmentID
	Work       domain.WorkID
	Stage      domain.Stage
	Visit      domain.Visit
	Attempt    domain.Attempt
	// Specify is the work's specify visit, whose tests implement must keep
	// and make pass. Zero when the work has none.
	Specify domain.AssignmentID
	// Changed is what the visit changed since its baseline.
	Changed domain.ChangedFiles
	// BaselineStarted: the visit had no baseline until this submission, so
	// nothing is known about what it changed.
	BaselineStarted bool
}

// Runner runs exit checks in one repository.
type Runner struct {
	Root   string
	Config Config
	// Env is the environment of the commands the checks run; GF_ACTOR is
	// never passed on.
	Env []string
	// Progress is told about each check as it finishes.
	Progress io.Writer
}

// LogPath is where the full output of one attempt's checks goes, relative
// to the repository root.
func LogPath(id domain.AssignmentID, attempt domain.Attempt) string {
	return eventlog.Dir("") + "/" + eventlog.RuntimeDir + "/checks/" + id.String() + "-attempt" + strconv.Itoa(attempt.Int()) + ".log"
}

// maxSummary bounds a check result's summary, log path included.
const maxSummary = 300

// Run runs the exit checks the visit's stage requires, in order, and
// returns how each went. An error means a check could not be run at all.
func (r Runner) Run(v Visit) ([]domain.CheckResult, error) {
	logPath := LogPath(v.Assignment, v.Attempt)
	abs := filepath.Join(r.Root, filepath.FromSlash(logPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("exit checks: %w", err)
	}
	log, err := os.Create(abs)
	if err != nil {
		return nil, fmt.Errorf("exit checks: %w", err)
	}
	defer log.Close()
	progress := r.Progress
	if progress == nil {
		progress = io.Discard
	}
	run := &checkRun{Runner: r, visit: v, log: log, logPath: logPath, baselines: worktree.OpenBaselines(r.Root)}
	var results []domain.CheckResult
	for _, check := range v.Stage.RequiredChecks() {
		fmt.Fprintf(log, "=== %s\n", check)
		f, err := run.check(check)
		if err != nil {
			return nil, fmt.Errorf("exit check %s: %w", check, err)
		}
		if f.passed {
			fmt.Fprintf(log, "=== %s passed\n\n", check)
			fmt.Fprintf(progress, "✓ %s\n", check)
			results = append(results, domain.CheckPassed(check))
			continue
		}
		summary := f.summary
		if f.ranCommand {
			suffix := " (log: " + logPath + ")"
			summary = truncate(summary, maxSummary-len(suffix)) + suffix
		} else {
			summary = truncate(summary, maxSummary)
		}
		fmt.Fprintf(log, "=== %s failed: %s\n\n", check, f.summary)
		fmt.Fprintf(progress, "✗ %s: %s\n", check, summary)
		result, err := domain.CheckFailed(check, summary)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := log.Close(); err != nil {
		return nil, fmt.Errorf("exit checks: %w", err)
	}
	return results, nil
}

// finding is how one check went, before it becomes a domain result.
type finding struct {
	passed     bool
	summary    string
	ranCommand bool // its full output is in the log
}

var pass = finding{passed: true}

func fail(format string, args ...any) finding { return finding{summary: fmt.Sprintf(format, args...)} }

type checkRun struct {
	Runner
	visit     Visit
	log       io.Writer
	logPath   string
	baselines worktree.Baselines
}

func (c *checkRun) check(check domain.ExitCheck) (finding, error) {
	switch check {
	case domain.CheckArtifactWritten:
		return c.artifactWritten()
	case domain.CheckSpecsFail:
		return c.specsFail()
	case domain.CheckBuildPasses:
		return c.buildPasses()
	case domain.CheckSpecsPass:
		return c.specsPass()
	case domain.CheckSpecsUntouched:
		return c.specsUntouched()
	case domain.CheckSuitePasses:
		return c.suitePasses()
	case domain.CheckFormatted:
		return c.formatted()
	case domain.CheckIntegrationPasses:
		return c.integrationPasses()
	}
	return finding{}, fmt.Errorf("gf does not know how to run %q", check)
}

func (c *checkRun) artifactWritten() (finding, error) {
	artifact := domain.ArtifactFor(c.visit.Work, c.visit.Stage, c.visit.Visit)
	content, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(artifact.String())))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fail("%s is missing", artifact), nil
	case err != nil:
		return finding{}, err
	case len(bytes.TrimSpace(content)) == 0:
		return fail("%s is empty", artifact), nil
	}
	base, ok, err := c.ownBaseline()
	if err != nil || !ok {
		return c.noOwnBaseline(), err
	}
	before, existed, err := worktree.FileAt(c.Root, base, artifact)
	if err != nil {
		return finding{}, err
	}
	if existed && bytes.Equal(before, content) {
		return fail("%s did not change during this stage visit", artifact), nil
	}
	return pass, nil
}

// ownBaseline is the baseline of the visit being checked.
func (c *checkRun) ownBaseline() (string, bool, error) {
	if c.visit.BaselineStarted {
		return "", false, nil
	}
	return c.baselines.Tree(c.visit.Assignment)
}

func (c *checkRun) noOwnBaseline() finding { return missingBaseline(c.visit.Stage) }

// missingBaseline: without a stage's baseline gf can't tell what the stage
// changed, and nothing the owner can do on this work brings it back.
func missingBaseline(stage domain.Stage) finding {
	return fail("The baseline for '%s' is missing (the work was started before this version of gf, or on another machine). The owner should abandon this work with `gf abandon \"<reason>\"` and start it again.", stage)
}

func (c *checkRun) specsFail() (finding, error) {
	base, ok, err := c.ownBaseline()
	if err != nil || !ok {
		return c.noOwnBaseline(), err
	}
	var specs []domain.RepoPath
	for _, p := range c.visit.Changed.Paths() {
		if strings.HasSuffix(p.String(), "_test.go") {
			specs = append(specs, p)
		}
	}
	tests, broken, err := c.testsChanged(specs, base, "")
	if err != nil {
		return finding{}, err
	}
	if broken != "" {
		return fail("specs don't compile: %s", broken), nil
	}
	if len(tests) == 0 {
		return fail("no test written in this stage"), nil
	}
	report, timedOut, err := c.goTest(tests, packagesOf(specs))
	if err != nil {
		return finding{}, err
	}
	switch {
	case timedOut != "":
		return c.ran(fail("%s", timedOut)), nil
	case !report.built():
		return c.ran(fail("specs don't compile: %s", report.buildProblem())), nil
	case len(intersect(tests, report.failed)) == 0:
		return c.ran(fail("no test written in this stage fails (%s)", describeRun(tests, report))), nil
	}
	return pass, nil
}

// specTests are the test files specify changed and the tests it wrote in
// them: what changed between the specify visit's baseline and this visit's.
func (c *checkRun) specTests() (files []domain.RepoPath, tests []string, problem *finding, err error) {
	var specifyBase string
	ok := false
	if c.visit.Specify != (domain.AssignmentID{}) {
		if specifyBase, ok, err = c.baselines.Tree(c.visit.Specify); err != nil {
			return nil, nil, nil, err
		}
	}
	if !ok {
		f := missingBaseline(domain.StageSpecify)
		return nil, nil, &f, nil
	}
	ownBase, ok, err := c.ownBaseline()
	if err != nil || !ok {
		f := c.noOwnBaseline()
		return nil, nil, &f, err
	}
	names, err := worktree.Diff(c.Root, specifyBase, ownBase)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			p, err := domain.NewRepoPath(c.Root, filepath.FromSlash(name))
			if err != nil {
				return nil, nil, nil, err
			}
			files = append(files, p)
		}
	}
	tests, broken, err := c.testsChanged(files, specifyBase, ownBase)
	if err != nil {
		return nil, nil, nil, err
	}
	if broken != "" {
		f := fail("the specify tests don't parse: %s", broken)
		return nil, nil, &f, nil
	}
	return files, tests, nil, nil
}

func (c *checkRun) specsUntouched() (finding, error) {
	files, _, problem, err := c.specTests()
	if err != nil || problem != nil {
		return deref(problem), err
	}
	ownBase, _, err := c.ownBaseline()
	if err != nil {
		return finding{}, err
	}
	now, err := worktree.Snapshot(c.Root)
	if err != nil {
		return finding{}, err
	}
	changed, err := worktree.Diff(c.Root, ownBase, now)
	if err != nil {
		return finding{}, err
	}
	var touched []string
	for _, p := range files {
		if !slices.Contains(changed, p.String()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.Root, filepath.FromSlash(p.String()))); errors.Is(err, os.ErrNotExist) {
			touched = append(touched, p.String()+" (deleted)")
		} else {
			touched = append(touched, p.String()+" (modified)")
		}
	}
	if len(touched) > 0 {
		return fail("the tests written during specify were changed: %s; restore them", strings.Join(touched, ", ")), nil
	}
	return pass, nil
}

func (c *checkRun) specsPass() (finding, error) {
	files, tests, problem, err := c.specTests()
	if err != nil || problem != nil {
		return deref(problem), err
	}
	if len(tests) == 0 {
		return fail("specify wrote no tests, so there is nothing to make pass"), nil
	}
	report, timedOut, err := c.goTest(tests, packagesOf(files))
	if err != nil {
		return finding{}, err
	}
	if timedOut != "" {
		return c.ran(fail("%s", timedOut)), nil
	}
	if !report.built() {
		return c.ran(fail("the spec tests don't compile: %s", report.buildProblem())), nil
	}
	var failing []string
	for _, t := range tests {
		if !slices.Contains(report.passed, t) || slices.Contains(report.failed, t) {
			failing = append(failing, t)
		}
	}
	if len(failing) > 0 {
		return c.ran(fail("%d of %d spec tests don't pass (%s)", len(failing), len(tests), strings.Join(failing, ", "))), nil
	}
	return pass, nil
}

func (c *checkRun) buildPasses() (finding, error) {
	for _, argv := range [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}} {
		out, err := c.command(c.Config.Timeout, argv...)
		if err != nil {
			return finding{}, err
		}
		if !out.ok() {
			return c.ran(fail("`%s` %s: %s", strings.Join(argv, " "), out.failure(), firstLines(withoutPackageHeaders(string(out.stderr)+string(out.stdout)), 3))), nil
		}
	}
	return pass, nil
}

func (c *checkRun) suitePasses() (finding, error) {
	out, err := c.command(c.Config.Timeout, "go", "test", "-json", "./...")
	if err != nil {
		return finding{}, err
	}
	report := parseTestJSON(out.stdout)
	fmt.Fprint(c.log, report.text)
	switch {
	case out.timedOut:
		return c.ran(fail("`go test ./...` %s", out.failure())), nil
	case !report.built():
		return c.ran(fail("the suite doesn't build: %s", report.buildProblem())), nil
	case len(report.failed) > 0:
		return c.ran(fail("%s failed (%s)", plural(len(report.failed), "test"), strings.Join(report.failed, ", "))), nil
	case !out.ok():
		return c.ran(fail("`go test ./...` %s: %s", out.failure(), firstLines(string(out.stderr), 3))), nil
	}
	return pass, nil
}

func (c *checkRun) formatted() (finding, error) {
	out, err := c.command(c.Config.Timeout, "gofmt", "-l", ".")
	if err != nil {
		return finding{}, err
	}
	if !out.ok() {
		return c.ran(fail("`gofmt -l .` %s: %s", out.failure(), firstLines(string(out.stderr), 3))), nil
	}
	var unformatted []string
	for _, name := range strings.Split(string(out.stdout), "\n") {
		name = filepath.ToSlash(strings.TrimSpace(name))
		if name == "" || slices.ContainsFunc(strings.Split(name, "/"), func(dir string) bool { return dir == "vendor" || dir == "testdata" }) {
			continue
		}
		unformatted = append(unformatted, name)
	}
	if len(unformatted) > 0 {
		return c.ran(fail("not gofmt-formatted: %s", strings.Join(unformatted, ", "))), nil
	}
	return pass, nil
}

func (c *checkRun) integrationPasses() (finding, error) {
	command := c.Config.Integration
	if command == nil {
		return fail("No integration command is configured. The owner adds one to %s", ConfigPath), nil
	}
	out, err := c.command(command.Timeout, command.Argv...)
	if err != nil {
		return finding{}, err
	}
	if out.ok() {
		return pass, nil
	}
	combined := string(out.stdout) + string(out.stderr)
	detail := failedTestNames(combined)
	if detail == "" {
		detail = lastLines(combined, 3)
	}
	if detail == "" {
		return c.ran(fail("`%s` %s", command, out.failure())), nil
	}
	return c.ran(fail("`%s` %s: %s", command, out.failure(), detail)), nil
}

// goTest runs the named tests of the packages with `go test -json`.
// timedOut describes a timeout.
func (c *checkRun) goTest(tests []string, packages []string) (report testReport, timedOut string, err error) {
	argv := append([]string{"go", "test", "-json", "-run", "^(" + strings.Join(tests, "|") + ")$"}, packages...)
	out, err := c.command(c.Config.Timeout, argv...)
	if err != nil {
		return testReport{}, "", err
	}
	report = parseTestJSON(out.stdout)
	fmt.Fprint(c.log, report.text)
	if out.timedOut {
		return report, "`go test` " + out.failure(), nil
	}
	if !out.ok() && len(report.failed) == 0 && report.built() {
		// go test failed before running anything, e.g. on a package pattern.
		report.buildErrors["go test"] = strings.Split(strings.TrimSpace(string(out.stderr)), "\n")
	}
	return report, "", nil
}

// testsChanged returns the tests declared or changed in files between two
// trees; an empty to means the working tree. broken describes a file in to
// that doesn't parse.
func (c *checkRun) testsChanged(files []domain.RepoPath, from, to string) (tests []string, broken string, err error) {
	for _, p := range files {
		var src []byte
		if to == "" {
			src, err = os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(p.String())))
			if errors.Is(err, os.ErrNotExist) {
				continue // deleted
			}
		} else {
			var ok bool
			src, ok, err = worktree.FileAt(c.Root, to, p)
			if err == nil && !ok {
				continue // deleted
			}
		}
		if err != nil {
			return nil, "", err
		}
		old, _, err := worktree.FileAt(c.Root, from, p)
		if err != nil {
			return nil, "", err
		}
		names, err := changedTests(old, src)
		if err != nil {
			return nil, firstLines(err.Error(), 1), nil
		}
		for _, n := range names {
			tests = appendUnique(tests, n)
		}
	}
	return tests, "", nil
}

// output is what a check's command did.
type output struct {
	stdout, stderr []byte
	err            error // from running it: an exit status, or why it couldn't start
	timedOut       bool
	timeout        time.Duration
}

func (o output) ok() bool { return o.err == nil && !o.timedOut }

func (o output) failure() string {
	if o.timedOut {
		return "timed out after " + o.timeout.String()
	}
	var exit *exec.ExitError
	if errors.As(o.err, &exit) {
		return "failed (" + exit.ProcessState.String() + ")"
	}
	return "could not run: " + o.err.Error()
}

// command runs argv in the repository root, with no terminal and without
// GF_ACTOR, and logs it with its output. A command that times out is killed
// with the processes it started.
func (c *checkRun) command(timeout time.Duration, argv ...string) (output, error) {
	fmt.Fprintf(c.log, "$ %s\n", strings.Join(argv, " "))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = c.Root
	cmd.Env = withoutActor(c.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	out := output{stdout: stdout.Bytes(), stderr: stderr.Bytes(), err: runErr, timeout: timeout,
		timedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	if !bytes.HasPrefix(out.stdout, []byte("{")) {
		c.log.Write(out.stdout) // JSON output is logged as text by its parser
	}
	c.log.Write(out.stderr)
	if !out.ok() {
		fmt.Fprintf(c.log, "(%s)\n", out.failure())
	}
	return out, nil
}

func withoutActor(env []string) []string {
	var kept []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GF_ACTOR=") {
			kept = append(kept, kv)
		}
	}
	return kept
}

func (c *checkRun) ran(f finding) finding {
	f.ranCommand = true
	return f
}

func deref(f *finding) finding {
	if f == nil {
		return finding{}
	}
	return *f
}

// packagesOf returns the packages of Go files, as ./relative patterns.
func packagesOf(files []domain.RepoPath) []string {
	var pkgs []string
	for _, p := range files {
		pkgs = appendUnique(pkgs, "./"+path.Dir(p.String()))
	}
	slices.Sort(pkgs)
	return pkgs
}

func intersect(a, b []string) []string {
	var both []string
	for _, s := range a {
		if slices.Contains(b, s) {
			both = append(both, s)
		}
	}
	return both
}

// describeRun says how the tests went when none of them failed.
func describeRun(tests []string, r testReport) string {
	var passed, missing []string
	for _, t := range tests {
		if slices.Contains(r.passed, t) {
			passed = append(passed, t)
		} else {
			missing = append(missing, t)
		}
	}
	var parts []string
	if len(passed) > 0 {
		parts = append(parts, strings.Join(passed, ", ")+" passed")
	}
	if len(missing) > 0 {
		parts = append(parts, strings.Join(missing, ", ")+" did not run")
	}
	return strings.Join(parts, "; ")
}

// failedTestNames finds go test's "--- FAIL: TestX" lines in text output.
func failedTestNames(text string) string {
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "--- FAIL: "); ok {
			name, _, _ := strings.Cut(rest, " ")
			if !strings.Contains(name, "/") {
				names = appendUnique(names, name)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	return plural(len(names), "test") + " failed (" + strings.Join(names, ", ") + ")"
}

// withoutPackageHeaders drops the "# package" lines go prints above errors.
func withoutPackageHeaders(s string) string {
	var kept []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "#") {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "; ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
