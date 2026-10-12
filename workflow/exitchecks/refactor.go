package exitchecks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// refactorPlanFile is plan's structured refactor plan in JSON. Its field
// names are the contract with the plan agent.
type refactorPlanFile struct {
	Areas []string `json:"areas"`
	Moves []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"moves"`
	BehaviourChange *bool `json:"behaviourChange"`
}

// PlanError says why a refactor plan can't be read.
type PlanError struct {
	Path    string // relative to the repository root
	Missing bool
	Problem string
}

func (e *PlanError) Error() string {
	if e.Missing {
		return e.Path + " is missing"
	}
	return e.Path + ": " + e.Problem
}

// LoadRefactorPlan reads a plan visit's structured refactor plan, by its
// path relative to root; a *PlanError says why it can't. Whether it is
// complete is RefactorPlan.Problems' to say.
func LoadRefactorPlan(root string, path domain.RepoPath) (domain.RefactorPlan, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path.String())))
	if errors.Is(err, os.ErrNotExist) {
		return domain.RefactorPlan{}, &PlanError{Path: path.String(), Missing: true}
	}
	if err != nil {
		return domain.RefactorPlan{}, fmt.Errorf("read refactor plan: %w", err)
	}
	plan, problem := parseRefactorPlan(data)
	if problem != "" {
		return domain.RefactorPlan{}, &PlanError{Path: path.String(), Problem: problem}
	}
	return plan, nil
}

func parseRefactorPlan(data []byte) (domain.RefactorPlan, string) {
	var file refactorPlanFile
	if problem := decodeStrict(data, &file); problem != "" {
		return domain.RefactorPlan{}, problem
	}
	if file.BehaviourChange == nil {
		return domain.RefactorPlan{}, `"behaviourChange" is missing: a refactor plan says "behaviourChange": false`
	}
	v := domain.RefactorPlanValues{Areas: file.Areas, BehaviourChange: *file.BehaviourChange}
	for _, m := range file.Moves {
		v.Moves = append(v.Moves, domain.RefactorMove{From: m.From, To: m.To})
	}
	return domain.NewRefactorPlan(v), ""
}

// AcceptedRefactorPlan reads a work's latest accepted plan, visit, as it
// was when the owner accepted it: in the baseline of the stage visit entered
// then, end. The working tree's copy may have changed since.
func AcceptedRefactorPlan(root string, work domain.WorkID, visit domain.Visit, end domain.AssignmentID) (domain.RefactorPlan, error) {
	path := domain.RefactorPlanFor(work, visit)
	tree, ok, err := worktree.OpenBaselines(root).Tree(end)
	if err != nil {
		return domain.RefactorPlan{}, err
	}
	if !ok {
		return domain.RefactorPlan{}, &PlanError{Path: path.String(), Problem: fmt.Sprintf("gf has no baseline of '%s', so it can't read the plan as the owner accepted it", end)}
	}
	data, exists, err := worktree.FileAt(root, tree, path)
	if err != nil {
		return domain.RefactorPlan{}, err
	}
	if !exists {
		return domain.RefactorPlan{}, &PlanError{Path: path.String(), Missing: true}
	}
	plan, problem := parseRefactorPlan(data)
	if problem != "" {
		return domain.RefactorPlan{}, &PlanError{Path: path.String(), Problem: problem}
	}
	return plan, nil
}

func (c *checkRun) planValid() (finding, error) {
	path := domain.RefactorPlanFor(c.visit.Work, c.visit.Visit)
	plan, err := LoadRefactorPlan(c.Root, path)
	var invalid *PlanError
	switch {
	case errors.As(err, &invalid):
		return fail("%s", invalid.Error()), nil
	case err != nil:
		return finding{}, err
	}
	artifact := domain.ArtifactFor(c.visit.Work, c.visit.Stage, c.visit.Visit)
	markdown, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(artifact.String())))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return finding{}, err
	}
	var grillDoc domain.RepoPath
	if c.visit.Grill != (domain.Visit{}) {
		grill, problem, err := c.acceptedGrill()
		if err != nil || problem != nil {
			return deref(problem), err
		}
		grillDoc = grill.DocPath()
	}
	problems := plan.Problems(string(markdown), grillDoc)
	if len(problems) == 0 {
		return pass, nil
	}
	return fail("%s: %s", path, strings.Join(problems, "; ")), nil
}

// acceptedPlan is the work's latest accepted refactor plan, as it was when
// it was accepted.
func (c *checkRun) acceptedPlan() (domain.RefactorPlan, *finding, error) {
	if c.visit.Plan == (domain.Visit{}) {
		f := fail("the work has no accepted plan")
		return domain.RefactorPlan{}, &f, nil
	}
	path := domain.RefactorPlanFor(c.visit.Work, c.visit.Plan)
	data, exists, problem, err := c.acceptedFile(domain.StagePlan, path, c.visit.PlanEnd)
	if err != nil || problem != nil {
		return domain.RefactorPlan{}, problem, err
	}
	if !exists {
		f := fail("the accepted plan %s is missing", path)
		return domain.RefactorPlan{}, &f, nil
	}
	plan, invalid := parseRefactorPlan(data)
	if invalid != "" {
		f := fail("the accepted plan %s is invalid: %s", path, invalid)
		return domain.RefactorPlan{}, &f, nil
	}
	return plan, nil, nil
}

// characterizationPasses: the tests this visit wrote pin the current
// behaviour, so each must compile and pass against the code as it is.
func (c *checkRun) characterizationPasses() (finding, error) {
	specs, tests, problem, err := c.specsWritten()
	if err != nil || problem != nil {
		if problem != nil && strings.HasPrefix(problem.summary, "specs don't compile") {
			return fail("the characterization tests don't compile: %s", strings.TrimPrefix(problem.summary, "specs don't compile: ")), nil
		}
		return deref(problem), err
	}
	if len(tests) == 0 {
		return pass, nil // coverage may already suffice; coverage-sufficient judges it
	}
	report, timedOut, err := c.goTest(tests, packagesOf(specs))
	if err != nil {
		return finding{}, err
	}
	switch {
	case timedOut != "":
		return c.ran(fail("%s", timedOut)), nil
	case !report.built():
		return c.ran(fail("the characterization tests don't compile: %s", report.buildProblem())), nil
	}
	var failing []string
	for _, t := range tests {
		if !slices.Contains(report.passed, t) || slices.Contains(report.failed, t) {
			failing = append(failing, t)
		}
	}
	if len(failing) > 0 {
		return c.ran(fail("%d of %d characterization tests don't pass against the current code (%s): a characterization test pins what the code does now",
			len(failing), len(tests), strings.Join(failing, ", "))), nil
	}
	return pass, nil
}

// PackageCoverage is the statement coverage of one Go package, as gf
// measured it for coverage-sufficient.
type PackageCoverage struct {
	Package    string  `json:"package"`
	Dir        string  `json:"dir"` // relative to the repository root
	Statements int     `json:"statements"`
	Covered    int     `json:"covered"`
	Percent    float64 `json:"percent"`
}

// CoverageResult is what coverage-sufficient measured in one attempt: gf
// records it in the attempt's run directory, for gf status.
type CoverageResult struct {
	MinCoverage float64           `json:"minCoverage"` // 0: report only
	Packages    []PackageCoverage `json:"packages"`
}

const coverageResultFile = "coverage.json"

// ReadCoverage returns what the latest attempt of a stage visit measured
// for coverage-sufficient; ok is false if no attempt did.
func ReadCoverage(root string, id domain.AssignmentID) (attempt int, result CoverageResult, ok bool, err error) {
	prefix := filepath.Join(root, filepath.FromSlash(eventlog.Dir("")+"/"+eventlog.RuntimeDir+"/checks/"+id.String()+"-attempt"))
	matches, err := filepath.Glob(prefix + "*/" + coverageResultFile)
	if err != nil {
		return 0, CoverageResult{}, false, err
	}
	latest := ""
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimPrefix(filepath.Dir(m), prefix))
		if err == nil && n > attempt {
			attempt, latest = n, m
		}
	}
	if latest == "" {
		return 0, CoverageResult{}, false, nil
	}
	data, err := os.ReadFile(latest)
	if err != nil {
		return 0, CoverageResult{}, false, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, CoverageResult{}, false, fmt.Errorf("read %s: %w", latest, err)
	}
	return attempt, result, true, nil
}

// DescribeCoverage lists each package's coverage, e.g.
// "internal/wallet 82.5% (33 of 40 statements)".
func DescribeCoverage(packages []PackageCoverage) string {
	var parts []string
	for _, p := range packages {
		parts = append(parts, fmt.Sprintf("%s %s%% (%d of %d statements)", p.Dir, formatPercent(p.Percent), p.Covered, p.Statements))
	}
	return strings.Join(parts, ", ")
}

func formatPercent(f float64) string {
	return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64)
}

// coverageSufficient measures the statement coverage of the packages in the
// accepted plan's areas, from their own tests, against the owner's
// refactor.minCoverage: each package must reach it. Without one it only
// reports the coverage.
func (c *checkRun) coverageSufficient() (finding, error) {
	plan, problem, err := c.acceptedPlan()
	if err != nil || problem != nil {
		return deref(problem), err
	}
	min := c.Config.Refactor.MinCoverage
	pkgs, err := c.packagesIn(plan.Areas())
	if err != nil {
		return finding{}, err
	}
	if len(pkgs) == 0 {
		if min > 0 {
			return fail("no Go package is in the plan's areas (%s), so gf can't measure their coverage", describeAreas(plan.Areas())), nil
		}
		return finding{passed: true, note: "no Go package in the plan's areas"}, nil
	}
	dir := filepath.Join(c.Root, filepath.FromSlash(StressRunDir(c.visit.Assignment, c.visit.Attempt)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return finding{}, fmt.Errorf("coverage: %w", err)
	}
	profile := filepath.Join(dir, "coverage.out")
	argv := []string{"go", "test", "-covermode=set", "-coverprofile=" + profile}
	for _, p := range pkgs {
		argv = append(argv, p.importPath)
	}
	out, err := c.command(c.Config.Timeout, argv...)
	if err != nil {
		return finding{}, err
	}
	if !out.ok() {
		return c.ran(fail("`go test -cover` of the plan's packages %s: %s", out.failure(), firstLines(withoutPackageHeaders(string(out.stdout)+string(out.stderr)), 3))), nil
	}
	measured, err := readCoverProfile(profile, pkgs)
	if err != nil {
		return finding{}, err
	}
	result := CoverageResult{MinCoverage: min, Packages: measured}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return finding{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, coverageResultFile), append(data, '\n'), 0o644); err != nil {
		return finding{}, err
	}
	described := DescribeCoverage(measured)
	fmt.Fprintf(c.log, "coverage: %s\n", described)
	if min <= 0 {
		return finding{passed: true, note: described + " (report only: no refactor.minCoverage in " + ConfigPath + ")"}, nil
	}
	var below []string
	for _, p := range measured {
		if p.Percent < min {
			below = append(below, fmt.Sprintf("%s %s%%", p.Dir, formatPercent(p.Percent)))
		}
	}
	if len(below) > 0 {
		return c.ran(fail("below the owner's minimum coverage of %s%%: %s; add characterization tests for the code about to move (all: %s)",
			formatPercent(min), strings.Join(below, ", "), described)), nil
	}
	return finding{passed: true, note: described}, nil
}

func describeAreas(areas []domain.AreaGlob) string {
	names := make([]string, len(areas))
	for i, a := range areas {
		names[i] = a.String()
	}
	return strings.Join(names, ", ")
}

// goPackage is a package of the repository's module.
type goPackage struct {
	importPath string
	dir        string // relative to the repository root, slash-separated
}

// packagesIn lists the repository's Go packages with a Go file in one of
// the areas, in import path order.
func (c *checkRun) packagesIn(areas []domain.AreaGlob) ([]goPackage, error) {
	out, err := c.command(c.Config.Timeout, "go", "list", "-f", `{{.ImportPath}}|{{.Dir}}|{{join .GoFiles ","}}|{{join .TestGoFiles ","}}`, "./...")
	if err != nil {
		return nil, err
	}
	if !out.ok() {
		return nil, fmt.Errorf("go list ./... %s: %s", out.failure(), firstLines(string(out.stderr), 3))
	}
	root := c.Root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved // go list reports directories with symlinks resolved
	}
	var pkgs []goPackage
	scanner := bufio.NewScanner(bytes.NewReader(out.stdout))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "|")
		if len(fields) != 4 {
			continue
		}
		rel, err := filepath.Rel(root, fields[1])
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		dir := filepath.ToSlash(rel)
		files := slices.Concat(strings.Split(fields[2], ","), strings.Split(fields[3], ","))
		in := slices.ContainsFunc(files, func(name string) bool {
			if name == "" {
				return false
			}
			p, err := domain.ParseRepoPath(path.Join(dir, name))
			return err == nil && slices.ContainsFunc(areas, func(a domain.AreaGlob) bool { return a.Matches(p) })
		})
		if in {
			pkgs = append(pkgs, goPackage{importPath: fields[0], dir: dir})
		}
	}
	slices.SortFunc(pkgs, func(a, b goPackage) int { return strings.Compare(a.importPath, b.importPath) })
	return pkgs, nil
}

// readCoverProfile sums a cover profile's statements per package. A block
// listed more than once counts once, covered if any listing covered it.
func readCoverProfile(profile string, pkgs []goPackage) ([]PackageCoverage, error) {
	data, err := os.ReadFile(profile)
	if err != nil {
		return nil, fmt.Errorf("read cover profile: %w", err)
	}
	type block struct {
		statements int
		covered    bool
	}
	blocks := map[string]block{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// file:startLine.startCol,endLine.endCol statements count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		statements, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		b := blocks[fields[0]]
		b.statements = statements
		b.covered = b.covered || count > 0
		blocks[fields[0]] = b
	}
	var result []PackageCoverage
	for _, p := range pkgs {
		pc := PackageCoverage{Package: p.importPath, Dir: p.dir}
		for key, b := range blocks {
			file, _, _ := strings.Cut(key, ":")
			if path.Dir(file) == p.importPath {
				pc.Statements += b.statements
				if b.covered {
					pc.Covered += b.statements
				}
			}
		}
		if pc.Statements > 0 {
			pc.Percent = float64(pc.Covered) * 100 / float64(pc.Statements)
		}
		result = append(result, pc)
	}
	return result, nil
}

// testsUntouched: no test file that existed when the visit began was
// modified or deleted; new ones are fine.
func (c *checkRun) testsUntouched() (finding, error) {
	base, ok, err := c.ownBaseline()
	if err != nil || !ok {
		return c.noOwnBaseline(), err
	}
	var touched []string
	for _, p := range c.visit.Changed.Paths() {
		if !domain.IsTestFile(p) {
			continue
		}
		_, existed, err := worktree.FileAt(c.Root, base, p)
		if err != nil {
			return finding{}, err
		}
		if !existed {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.Root, filepath.FromSlash(p.String()))); errors.Is(err, os.ErrNotExist) {
			touched = append(touched, p.String()+" (deleted)")
		} else {
			touched = append(touched, p.String()+" (modified)")
		}
	}
	if len(touched) > 0 {
		return fail("existing tests were changed: %s; restore them (new test files are allowed)", strings.Join(touched, ", ")), nil
	}
	return pass, nil
}
