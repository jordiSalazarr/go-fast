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

// ArtifactFor is the artifact every stage visit writes.
func ArtifactFor(work WorkID, stage Stage, visit Visit) RepoPath {
	return RepoPath{value: ArtifactDir(work).value + "/" + stage.name + "-v" + strconv.Itoa(visit.n) + ".md"}
}

// WriteScope is what may be written during a stage.
type WriteScope struct{ name string }

var (
	// WriteScopeArtifacts allows only the work's artifact directory.
	WriteScopeArtifacts = WriteScope{name: "artifacts"}
	// WriteScopeTestsAndArtifacts also allows *_test.go and files under testdata/.
	WriteScopeTestsAndArtifacts = WriteScope{name: "tests and artifacts"}
	// WriteScopeAnything allows everything outside gofast's own files.
	WriteScopeAnything = WriteScope{name: "anything"}
)

func (s WriteScope) String() string { return s.name }

// WriteScope is what may be written while the work is on this stage.
func (s Stage) WriteScope() WriteScope {
	switch s {
	case StageSpecify:
		return WriteScopeTestsAndArtifacts
	case StageImplement:
		return WriteScopeAnything
	case StageDiscovery, StageReview, StageIntegrationTesting:
		return WriteScopeArtifacts
	}
	return WriteScope{}
}

// Allows reports whether path may be written during a stage of work. The
// work's artifact directory is always allowed; the rest of .gofast/ never is.
func (s WriteScope) Allows(path RepoPath, work WorkID) bool {
	if path.isZero() || work.isZero() {
		return false
	}
	if path.within(ArtifactDir(work)) {
		return true
	}
	if path == gofastDir || path.within(gofastDir) {
		return false
	}
	switch s {
	case WriteScopeTestsAndArtifacts:
		return isTestFile(path)
	case WriteScopeAnything:
		return true
	}
	return false
}

// Violations returns the changed paths the scope does not allow, in path order.
func (s WriteScope) Violations(changed ChangedFiles, work WorkID) []RepoPath {
	var violations []RepoPath
	for _, p := range changed.paths {
		if !s.Allows(p, work) {
			violations = append(violations, p)
		}
	}
	return violations
}

// ChangedFiles is the set of files a stage visit changed, however they were
// written. The zero value is the empty set.
type ChangedFiles struct{ paths []RepoPath } // sorted, unique

func NewChangedFiles(paths ...RepoPath) ChangedFiles {
	var c ChangedFiles
	for _, p := range paths {
		if !p.isZero() {
			c.paths = append(c.paths, p)
		}
	}
	slices.SortFunc(c.paths, func(a, b RepoPath) int { return strings.Compare(a.value, b.value) })
	c.paths = slices.Compact(c.paths)
	return c
}

func (c ChangedFiles) Paths() []RepoPath { return slices.Clone(c.paths) }

func isTestFile(p RepoPath) bool {
	segments := strings.Split(p.value, "/")
	for _, dir := range segments[:len(segments)-1] {
		if dir == "testdata" {
			return true
		}
	}
	return strings.HasSuffix(segments[len(segments)-1], "_test.go")
}
