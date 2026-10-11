package exitchecks

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// Stress testing: the agent writes one k6 script per NFR it exercises; gf
// starts the service, runs k6 with the NFR's load, stops the service and
// measures the run itself. stress-scripts-valid covers what the agent can
// get wrong (and what the owner must set up: k6, the stress config, the
// NFRs); nfrs-met covers what the code can get wrong.

// StressRunDir is where one attempt's stress run keeps its files, relative
// to the repository root: per NFR, <nfr-id>.json.gz (k6's raw output),
// <nfr-id>.k6.log and <nfr-id>.service.log.
func StressRunDir(id domain.AssignmentID, attempt domain.Attempt) string {
	return eventlog.Dir("") + "/" + eventlog.RuntimeDir + "/checks/" + id.String() + "-attempt" + strconv.Itoa(attempt.Int())
}

// Messages for a stress stage that can't run: it fails closed.
const (
	noK6Message           = "k6 is required for stress testing. Install it (https://k6.io) and run `gf checks`."
	noStressConfigMessage = "No stress configuration in the gofast config. The owner adds a \"stress\" section to " + ConfigPath + "; `gf checks` shows it."
	noNFRsMessage         = "No NFRs in " + NFRsPath + ", so there is nothing to stress test against. The owner adds them; `gf checks` shows them."
)

// stressRun is what the stress checks found, run once for both.
type stressRun struct {
	ran      bool     // k6 ran, so the check log has its output
	problems []string // why the scripts aren't valid: nothing is measured then
	outcomes []nfrOutcome
}

// nfrOutcome is how one scripted NFR went: measured, or the service failed
// before it could be.
type nfrOutcome struct {
	nfr     domain.NFR
	result  domain.NFRResult
	problem string
}

func (c *checkRun) stressScriptsValid() (finding, error) {
	run, err := c.runStress()
	if err != nil {
		return finding{}, err
	}
	if len(run.problems) > 0 {
		f := fail("%s", strings.Join(run.problems, "; "))
		if run.ran {
			f = c.ran(f)
		}
		return f, nil
	}
	return pass, nil
}

func (c *checkRun) nfrsMet() (finding, error) {
	run, err := c.runStress()
	if err != nil {
		return finding{}, err
	}
	if len(run.problems) > 0 {
		return fail("not measured: the stress scripts aren't valid (see stress-scripts-valid)"), nil
	}
	var missed []string
	for _, o := range run.outcomes {
		switch {
		case o.problem != "":
			missed = append(missed, o.nfr.ID().String()+": "+o.problem)
		case !o.result.Met():
			missed = append(missed, o.result.Summary())
		}
	}
	if len(missed) > 0 {
		return c.ran(fail("%s", strings.Join(missed, "; "))), nil
	}
	return pass, nil
}

// runStress checks the scripts and, if they are valid, runs them, once.
func (c *checkRun) runStress() (*stressRun, error) {
	if c.stress != nil {
		return c.stress, nil
	}
	run := &stressRun{}
	c.stress = run
	scripts, err := c.plannedScripts(run)
	if err != nil || len(run.problems) > 0 {
		return run, err
	}
	dir := filepath.Join(c.Root, filepath.FromSlash(StressRunDir(c.visit.Assignment, c.visit.Attempt)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return run, fmt.Errorf("stress run: %w", err)
	}
	for _, s := range scripts {
		if err := c.runScript(run, dir, s.nfr, s.path); err != nil {
			return run, err
		}
	}
	if len(run.problems) == 0 {
		if err := writeStressResults(filepath.Join(dir, stressResultsFile), run.outcomes); err != nil {
			return run, err
		}
	}
	return run, nil
}

// StressResult is how one NFR went in a stress run: gf records them in the
// run's directory, for gf status.
type StressResult struct {
	NFR     string `json:"nfr"`
	Targets string `json:"targets"`
	Met     bool   `json:"met"`
	// Missed describes each missed target, measured against target.
	Missed []string `json:"missed,omitempty"`
	// Problem is set when the service failed before it could be measured.
	Problem   string  `json:"problem,omitempty"`
	Measured  string  `json:"measured,omitempty"`
	P95Ms     float64 `json:"p95Ms"`
	P99Ms     float64 `json:"p99Ms"`
	ErrorRate float64 `json:"errorRate"`
	RPS       float64 `json:"rps"`
	Requests  int     `json:"requests"`
}

const stressResultsFile = "results.json"

func writeStressResults(path string, outcomes []nfrOutcome) error {
	results := []StressResult{}
	for _, o := range outcomes {
		r := StressResult{NFR: o.nfr.ID().String(), Targets: o.nfr.Targets().Describe(), Problem: o.problem}
		if o.problem == "" {
			m := o.result.Measurement().Values()
			r.Met, r.Missed, r.Measured = o.result.Met(), o.result.Missed(), o.result.Measurement().Describe()
			r.P95Ms, r.P99Ms = float64(m.P95)/float64(time.Millisecond), float64(m.P99)/float64(time.Millisecond)
			r.ErrorRate, r.RPS, r.Requests = m.ErrorRate, m.RPS, m.Requests
		}
		results = append(results, r)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ReadStressResults returns the NFR results of a stage visit's latest
// attempt that measured any; ok is false if none did.
func ReadStressResults(root string, id domain.AssignmentID) (attempt int, results []StressResult, ok bool, err error) {
	prefix := filepath.Join(root, filepath.FromSlash(eventlog.Dir("")+"/"+eventlog.RuntimeDir+"/checks/"+id.String()+"-attempt"))
	matches, err := filepath.Glob(prefix + "*/" + stressResultsFile)
	if err != nil {
		return 0, nil, false, err
	}
	latest := ""
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimPrefix(filepath.Dir(m), prefix))
		if err == nil && n > attempt {
			attempt, latest = n, m
		}
	}
	if latest == "" {
		return 0, nil, false, nil
	}
	data, err := os.ReadFile(latest)
	if err != nil {
		return 0, nil, false, err
	}
	if err := json.Unmarshal(data, &results); err != nil {
		return 0, nil, false, fmt.Errorf("read %s: %w", latest, err)
	}
	return attempt, results, true, nil
}

type plannedScript struct {
	nfr  domain.NFR
	path domain.RepoPath
}

// plannedScripts are the scripts to run, in NFR id order, once everything
// they need is there and they pass the static check. Problems go to run.
func (c *checkRun) plannedScripts(run *stressRun) ([]plannedScript, error) {
	problem := func(format string, args ...any) { run.problems = append(run.problems, fmt.Sprintf(format, args...)) }
	if c.Config.Stress == nil {
		problem("%s", noStressConfigMessage)
	}
	if _, ok := lookPath(c.Env, "k6"); !ok {
		problem("%s", noK6Message)
	}
	nfrs, err := LoadNFRs(c.Root)
	if err != nil {
		return nil, err
	}
	if len(nfrs) == 0 {
		problem("%s", noNFRsMessage)
	}
	if len(run.problems) > 0 {
		return nil, nil
	}
	changed, err := c.baselines.WorkChanges(c.visit.FirstAssignment)
	if errors.Is(err, worktree.ErrNoWorkBaseline) {
		problem("%s", "The baseline of the work's first stage is missing, so gf can't tell which NFRs are mandatory. The owner should abandon this work with `gf abandon \"<reason>\"` and start it again.")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dir := domain.StressScriptsDir(c.visit.Work)
	entries, err := os.ReadDir(filepath.Join(c.Root, filepath.FromSlash(dir.String())))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stress scripts: %w", err)
	}
	var scripts []plannedScript
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".js")
		i := slices.IndexFunc(nfrs, func(n domain.NFR) bool { return n.ID().String() == id })
		if i < 0 {
			problem("%s/%s names no NFR in %s", dir, e.Name(), NFRsPath)
			continue
		}
		scripts = append(scripts, plannedScript{nfr: nfrs[i], path: domain.StressScriptFor(c.visit.Work, nfrs[i].ID())})
	}
	for _, n := range domain.Covering(nfrs, changed) {
		if !slices.ContainsFunc(scripts, func(s plannedScript) bool { return s.nfr.ID() == n.ID() }) {
			problem("no script for %s, which this change makes mandatory: write %s", n.ID(), domain.StressScriptFor(c.visit.Work, n.ID()))
		}
	}
	if len(scripts) == 0 && len(run.problems) == 0 {
		problem("no stress script: write one per NFR to exercise, at %s/<nfr-id>.js", dir)
	}
	for _, s := range scripts {
		src, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(s.path.String())))
		if err != nil {
			return nil, fmt.Errorf("stress script %s: %w", s.path, err)
		}
		for _, p := range scriptProblems(string(src)) {
			problem("%s.js %s", s.nfr.ID(), p)
		}
	}
	slices.SortFunc(scripts, func(a, b plannedScript) int { return strings.Compare(a.nfr.ID().String(), b.nfr.ID().String()) })
	return scripts, nil
}

// runScript runs one NFR's script against a freshly started service.
func (c *checkRun) runScript(run *stressRun, dir string, nfr domain.NFR, script domain.RepoPath) error {
	config := *c.Config.Stress
	id := nfr.ID().String()
	fmt.Fprintf(c.log, "--- %s: %s, %d vus for %s, targets %s\n", id, script, nfr.Load().VUs(), nfr.Load().Duration(), nfr.Targets().Describe())
	if answers(config.Ready) {
		run.problems = append(run.problems, fmt.Sprintf("something already answers at %s before gf started the service, so gf would measure it instead; stop it and submit again", config.Ready))
		return nil
	}
	service, err := c.startService(config, filepath.Join(dir, id+".service.log"))
	if err != nil {
		run.problems = append(run.problems, fmt.Sprintf("the service could not start: %s (%s)", err, ConfigPath))
		return nil
	}
	defer service.stop()
	if problem := service.waitReady(config.Ready, config.StartTimeout); problem != "" {
		fmt.Fprintf(c.log, "(%s)\n", problem)
		run.outcomes = append(run.outcomes, nfrOutcome{nfr: nfr, problem: problem + " (log: " + c.relative(filepath.Join(dir, id+".service.log")) + ")"})
		return nil
	}

	raw := filepath.Join(dir, id+".json.gz")
	emptyConfig := filepath.Join(dir, "k6-config.json")
	if err := os.WriteFile(emptyConfig, []byte("{}\n"), 0o644); err != nil {
		return fmt.Errorf("stress run: %w", err)
	}
	run.ran = true
	k6, _ := lookPath(c.Env, "k6")
	out, err := c.commandWithEnv(config.Timeout, withoutK6(withoutActor(c.Env)), k6Args(k6, nfr, config.BaseURL, emptyConfig, raw, script)...)
	if err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, id+".k6.log"), append(out.stdout, out.stderr...), 0o644)
	service.stop()
	switch {
	case out.timedOut:
		run.outcomes = append(run.outcomes, nfrOutcome{nfr: nfr, problem: "k6 " + out.failure()})
		return nil
	case !out.ok():
		run.problems = append(run.problems, fmt.Sprintf("%s.js failed under k6 (%s): %s", id, out.failure(), lastLines(string(out.stderr)+string(out.stdout), 2)))
		return nil
	}
	m, err := readMeasurement(raw, nfr, config.BaseURL)
	if err != nil {
		return err
	}
	if m.Values().Requests == 0 {
		run.problems = append(run.problems, fmt.Sprintf("%s.js sent no %s requests under load (requests renamed with a `name` tag don't count)", id, nfr.Operation()))
		return nil
	}
	result := nfr.Evaluate(m)
	fmt.Fprintf(c.log, "measured %s: %s\n", id, m.Describe())
	run.outcomes = append(run.outcomes, nfrOutcome{nfr: nfr, result: result})
	return nil
}

// k6Args runs a script with the NFR's load, so the script can't set it, and
// only raw output: no thresholds, no end-of-test summary (so no
// handleSummary), an empty config file instead of the user's, and only the
// tags measure reads.
func k6Args(k6 string, nfr domain.NFR, baseURL, config, raw string, script domain.RepoPath) []string {
	return []string{
		k6, "run", "--quiet", "--no-usage-report", "--no-thresholds", "--summary-mode", "disabled",
		"--config", config, "--system-tags", "method,name,status,url,scenario",
		"--vus", strconv.Itoa(nfr.Load().VUs()), "--duration", nfr.Load().Duration().String(),
		"--env", "BASE_URL=" + baseURL, "--out", "json=" + raw, script.String(),
	}
}

func readMeasurement(raw string, nfr domain.NFR, baseURL string) (domain.Measurement, error) {
	f, err := os.Open(raw)
	if errors.Is(err, os.ErrNotExist) {
		return domain.NewMeasurement(domain.MeasurementValues{})
	}
	if err != nil {
		return domain.Measurement{}, fmt.Errorf("read k6 output: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if errors.Is(err, io.EOF) {
		return domain.NewMeasurement(domain.MeasurementValues{})
	}
	if err != nil {
		return domain.Measurement{}, fmt.Errorf("read k6 output: %w", err)
	}
	return measure(gz, nfr.Operation(), baseURL, nfr.Load().Duration())
}

// withoutK6 drops K6_* variables, which could set options behind the
// command line's back (K6_RPS throttles the load).
func withoutK6(env []string) []string {
	var kept []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "K6_") {
			kept = append(kept, kv)
		}
	}
	return kept
}

// lookPath finds a program on the PATH of env, the environment the checks'
// commands get (exec.Command would look in gf's own).
func lookPath(env []string, name string) (string, bool) {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

func (c *checkRun) relative(abs string) string {
	rel, err := filepath.Rel(c.Root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// service is the repository's service, started for one stress run in its
// own process group, so stopping it stops everything it started (`go run`
// runs the server as a child).
type service struct {
	cmd     *exec.Cmd
	exited  chan struct{}
	err     error
	stopped bool
}

func (c *checkRun) startService(config Stress, logPath string) (*service, error) {
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.log, "$ %s &\n", strings.Join(config.Start, " "))
	cmd := exec.Command(config.Start[0], config.Start[1:]...)
	cmd.Dir = c.Root
	cmd.Env = withoutActor(c.Env)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	s := &service{cmd: cmd, exited: make(chan struct{})}
	go func() {
		s.err = cmd.Wait()
		log.Close()
		close(s.exited)
	}()
	return s, nil
}

// waitReady polls the ready URL until it answers 2xx; a problem says why it
// didn't.
func (s *service) waitReady(ready string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-s.exited:
			return fmt.Sprintf("the service exited before it was ready (%v)", s.err)
		default:
		}
		if resp, err := client.Get(ready); err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return ""
			}
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("the service didn't answer 2xx at %s within %s (if that URL is wrong, the owner fixes %s)", ready, timeout, ConfigPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stop ends the service's process group: politely, then not.
func (s *service) stop() {
	if s.stopped {
		return
	}
	s.stopped = true
	pgid := -s.cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	select {
	case <-s.exited:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-s.exited
	}
	// Whatever the leader left behind in its group goes too.
	_ = syscall.Kill(pgid, syscall.SIGKILL)
}

// answers reports whether anything answers at url.
func answers(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
