package domain

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// RepoPath is a clean, slash-separated path relative to the repository root.
type RepoPath struct{ value string }

// NewRepoPath makes a RepoPath from an absolute path, or from a path relative
// to root. Paths outside root are rejected. It does not touch the file system.
func NewRepoPath(root, p string) (RepoPath, error) {
	if p == "" || root == "" {
		return RepoPath{}, fmt.Errorf("repo path %q: %w", p, ErrInvalidRepoPath)
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return RepoPath{}, fmt.Errorf("repo path %q under %s: %w", p, root, ErrPathOutsideRepository)
	}
	if rel == "." {
		return RepoPath{}, fmt.Errorf("repo path %q is the repository root: %w", p, ErrInvalidRepoPath)
	}
	return RepoPath{value: filepath.ToSlash(rel)}, nil
}

func (p RepoPath) String() string { return p.value }
func (p RepoPath) isZero() bool   { return p.value == "" }

// within reports whether p is strictly inside dir.
func (p RepoPath) within(dir RepoPath) bool { return strings.HasPrefix(p.value, dir.value+"/") }

// gofastDir holds gofast's own files: the event log, its lock, the log file,
// runtime markers and the works' artifacts.
var gofastDir = RepoPath{value: ".gofast"}

// ArtifactDir is where a work's stage artifacts live.
func ArtifactDir(work WorkID) RepoPath {
	return RepoPath{value: gofastDir.value + "/works/" + work.value}
}

// StressScriptFor is where the stress-testing agent writes the k6 script
// for an NFR, in the work's artifact directory.
func StressScriptFor(work WorkID, nfr NFRID) RepoPath {
	return RepoPath{value: StressScriptsDir(work).value + "/" + nfr.value + ".js"}
}

// StressScriptsDir holds the work's k6 scripts, one per NFR.
func StressScriptsDir(work WorkID) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/stress"}
}

// DesignFor is the structured design a design visit writes next to its
// artifact.
func DesignFor(work WorkID, visit Visit) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/design-v" + strconv.Itoa(visit.n) + ".json"}
}

// EventModelFor is the canonical event model an event-modelling visit
// writes next to its artifact.
func EventModelFor(work WorkID, visit Visit) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/event-model-v" + strconv.Itoa(visit.n) + ".json"}
}

// RefactorPlanFor is the structured refactor plan a plan visit writes next
// to its artifact.
func RefactorPlanFor(work WorkID, visit Visit) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/refactor-plan-v" + strconv.Itoa(visit.n) + ".json"}
}

// GrillFor is the structured outcome a grill visit writes next to its
// artifact.
func GrillFor(work WorkID, visit Visit) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/grill-v" + strconv.Itoa(visit.n) + ".json"}
}

// NFRProposalsFor is where a design visit may propose NFR changes, in the
// schema of the repository's NFRs.
func NFRProposalsFor(work WorkID, visit Visit) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/nfrs.proposed-v" + strconv.Itoa(visit.n) + ".json"}
}

// ArtifactFor is the artifact every stage visit writes.
func ArtifactFor(work WorkID, stage Stage, visit Visit) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/" + stage.name + "-v" + strconv.Itoa(visit.n) + ".md"}
}

// WriteScope is what may be written during a stage.
type WriteScope struct{ name string }

var (
	// WriteScopeArtifacts allows only the work's artifact directory.
	WriteScopeArtifacts = WriteScope{name: "artifacts"}
	// WriteScopeArtifactsAndDecision also allows the work's decision
	// document in the decisions directory, the repository's glossaries and
	// its ADRs: grill writes them while it interviews the owner.
	WriteScopeArtifactsAndDecision = WriteScope{name: "artifacts, the decision document, glossaries and ADRs"}
	// WriteScopeTestsAndArtifacts also allows *_test.go and files under testdata/.
	WriteScopeTestsAndArtifacts = WriteScope{name: "tests and artifacts"}
	// WriteScopeTestsSkeletonsAndArtifacts also allows non-test Go files,
	// for the compile-only skeletons the specs need; skeletons-only checks
	// they are new and do nothing.
	WriteScopeTestsSkeletonsAndArtifacts = WriteScope{name: "tests, skeletons and artifacts"}
	// WriteScopeAnything allows everything outside gofast's own files.
	WriteScopeAnything = WriteScope{name: "anything"}
)

func (s WriteScope) String() string { return s.name }

// CanChangeCode reports whether the scope allows any code, tests included.
func (s WriteScope) CanChangeCode() bool {
	return s == WriteScopeTestsAndArtifacts || s == WriteScopeTestsSkeletonsAndArtifacts || s == WriteScopeAnything
}

// WriteScope is what may be written while the work is on this stage.
func (s Stage) WriteScope() WriteScope {
	switch s {
	case StageSpecify:
		return WriteScopeTestsSkeletonsAndArtifacts
	case StageCharacterize:
		return WriteScopeTestsAndArtifacts
	case StageImplement, StageOptimize:
		return WriteScopeAnything
	case StageGrill:
		return WriteScopeArtifactsAndDecision
	case StageEventModelling, StageDiscovery, StageDesign, StagePlan, StageMeasure, StageReview, StageIntegrationTesting, StageStressTesting, StageFixer:
		return WriteScopeArtifacts
	}
	return WriteScope{}
}

// Allows reports whether path may be written during a stage of work. The
// work's artifact directory is always allowed; the rest of .gofast/ never
// is, nor the index gf generates in the decisions directory.
func (s WriteScope) Allows(path RepoPath, work WorkID, decisions DecisionsDir) bool {
	if path.isZero() || work.isZero() {
		return false
	}
	if path.within(ArtifactDir(work)) {
		return true
	}
	if path == gofastDir || path.within(gofastDir) || path == decisions.Index() {
		return false
	}
	switch s {
	case WriteScopeArtifactsAndDecision:
		return decisions.IsDecisionOf(path, work) || isGlossary(path) || isADR(path)
	case WriteScopeTestsAndArtifacts:
		return isTestFile(path)
	case WriteScopeTestsSkeletonsAndArtifacts:
		return isTestFile(path) || IsGoFile(path)
	case WriteScopeAnything:
		return true
	}
	return false
}

// Violations returns the changed paths the scope does not allow, in path order.
func (s WriteScope) Violations(changed ChangedFiles, work WorkID, decisions DecisionsDir) []RepoPath {
	var violations []RepoPath
	for _, p := range changed.paths {
		if !s.Allows(p, work, decisions) {
			violations = append(violations, p)
		}
	}
	return violations
}

// ChangedFiles is the set of files a stage visit changed, however they were
// written, and which of them it added. The zero value is the empty set.
type ChangedFiles struct {
	paths []RepoPath // sorted, unique
	added []RepoPath // sorted, unique, a subset of paths
}

func NewChangedFiles(paths ...RepoPath) ChangedFiles {
	return ChangedFiles{paths: sortedSet(paths)}
}

// WithAdded marks paths as added: they didn't exist when the visit began.
// They are changed files too.
func (c ChangedFiles) WithAdded(added ...RepoPath) ChangedFiles {
	return ChangedFiles{paths: sortedSet(append(slices.Clone(c.paths), added...)), added: sortedSet(append(slices.Clone(c.added), added...))}
}

func sortedSet(paths []RepoPath) []RepoPath {
	var set []RepoPath
	for _, p := range paths {
		if !p.isZero() {
			set = append(set, p)
		}
	}
	slices.SortFunc(set, func(a, b RepoPath) int { return strings.Compare(a.value, b.value) })
	return slices.Compact(set)
}

func (c ChangedFiles) Paths() []RepoPath { return slices.Clone(c.paths) }

// Added reports whether the visit added the path.
func (c ChangedFiles) Added(p RepoPath) bool { return slices.Contains(c.added, p) }

// ScopeFacts are what a stage visit's write scope depends on beyond its
// stage: the repository's decisions directory and, for implement on a path
// with a refactor plan, the latest accepted plan.
type ScopeFacts struct {
	Decisions DecisionsDir
	Plan      *RefactorPlan
}

// Writes is what one stage visit of a work may write: its stage's write
// scope, narrowed on a refactor's implement to the accepted plan's areas.
type Writes struct {
	stage     Stage
	work      WorkID
	decisions DecisionsDir
	areas     []AreaGlob // set only when narrowed to a plan
}

// WritesOf is what a stage visit of work on a path may write. Implement on
// a path with a plan may write only the plan's areas, and new files under
// the directories they name: without an accepted plan it may write nothing.
func WritesOf(stage Stage, path Path, work WorkID, facts ScopeFacts) (Writes, error) {
	w := Writes{stage: stage, work: work, decisions: facts.Decisions}
	if stage != StageImplement || !path.Includes(StagePlan) {
		return w, nil
	}
	if facts.Plan == nil || len(facts.Plan.Areas()) == 0 {
		return Writes{}, fmt.Errorf("%s on %s work: %w", stage, path.workType, ErrNoAcceptedPlan)
	}
	w.areas = facts.Plan.Areas()
	return w, nil
}

func (w Writes) Stage() Stage { return w.stage }

// Areas are the plan's areas the scope is narrowed to; none when it isn't.
func (w Writes) Areas() []AreaGlob { return slices.Clone(w.areas) }

// Allows reports whether the visit may write path; added says the path
// didn't exist when the visit began.
func (w Writes) Allows(path RepoPath, added bool) bool {
	if !w.stage.WriteScope().Allows(path, w.work, w.decisions) {
		return false
	}
	if w.areas == nil || path.within(ArtifactDir(w.work)) {
		return true
	}
	for _, a := range w.areas {
		if a.Matches(path) {
			return true
		}
		if dir, ok := a.dir(); ok && added && path.within(dir) {
			return true
		}
	}
	return false
}

// Violations returns the changed paths the visit may not write, in path
// order.
func (w Writes) Violations(changed ChangedFiles) []RepoPath {
	var violations []RepoPath
	for _, p := range changed.paths {
		if !w.Allows(p, changed.Added(p)) {
			violations = append(violations, p)
		}
	}
	return violations
}

// isGlossary reports whether a path is a glossary of the domain model: a
// GLOSSARY.md, or the GLOSSARY-MAP.md that maps the contexts' glossaries,
// at any depth.
func isGlossary(p RepoPath) bool {
	name := p.value[strings.LastIndex(p.value, "/")+1:]
	return name == "GLOSSARY.md" || name == "GLOSSARY-MAP.md"
}

// isADR reports whether a path is an architecture decision record: a .md
// file under a docs/adr directory, the repository's or a context's
// (billing/docs/adr).
func isADR(p RepoPath) bool {
	if !strings.HasSuffix(p.value, ".md") {
		return false
	}
	dirs := strings.Split(p.value, "/")
	dirs = dirs[:len(dirs)-1]
	for i := 1; i < len(dirs); i++ {
		if dirs[i-1] == "docs" && dirs[i] == "adr" {
			return true
		}
	}
	return false
}

// IsGoFile reports whether a path is a Go source file that isn't test code:
// what a skeleton is written in.
func IsGoFile(p RepoPath) bool { return strings.HasSuffix(p.value, ".go") && !isTestFile(p) }

// IsTestFile reports whether a path is test code: a *_test.go file, or a
// file under a testdata directory.
func IsTestFile(p RepoPath) bool { return isTestFile(p) }

func isTestFile(p RepoPath) bool {
	segments := strings.Split(p.value, "/")
	for _, dir := range segments[:len(segments)-1] {
		if dir == "testdata" {
			return true
		}
	}
	return strings.HasSuffix(segments[len(segments)-1], "_test.go")
}
