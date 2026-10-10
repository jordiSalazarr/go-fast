package exitchecks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// testReport is what `go test -json` reported.
type testReport struct {
	failed      []string            // top-level tests that failed, in order, unique
	passed      []string            // top-level tests that passed, in order, unique
	buildErrors map[string][]string // package → its build output, for packages that did not build
	failedPkgs  []string            // packages that failed without a failing test (setup, TestMain, ...)
	text        string              // the human-readable output, for the log
}

func (r testReport) built() bool { return len(r.buildErrors) == 0 && len(r.failedPkgs) == 0 }

// buildProblem is the first lines of the build output of the packages that
// did not build, or names the packages that failed without a failing test.
func (r testReport) buildProblem() string {
	pkgs := make([]string, 0, len(r.buildErrors))
	for p := range r.buildErrors {
		pkgs = append(pkgs, p)
	}
	slices.Sort(pkgs)
	var lines []string
	for _, p := range pkgs {
		for _, l := range r.buildErrors[p] {
			if !strings.HasPrefix(l, "#") {
				lines = append(lines, l)
			}
		}
	}
	if len(lines) > 0 {
		return firstLines(strings.Join(lines, "\n"), 3)
	}
	if len(pkgs) > 0 {
		return strings.Join(pkgs, ", ") + " did not build"
	}
	return strings.Join(r.failedPkgs, ", ") + " failed without a failing test"
}

type testEvent struct {
	Action      string
	Package     string
	ImportPath  string
	Test        string
	Output      string
	FailedBuild string
}

// parseTestJSON reads `go test -json` output. Lines that are not JSON events
// are kept as text.
func parseTestJSON(stdout []byte) testReport {
	r := testReport{buildErrors: map[string][]string{}}
	var text strings.Builder
	failedTests := map[string]bool{} // package → had a failing test
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var e testEvent
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &e) != nil {
			text.Write(line)
			text.WriteByte('\n')
			continue
		}
		text.WriteString(e.Output)
		name, _, _ := strings.Cut(e.Test, "/") // subtests count as their test
		switch {
		case e.Action == "build-output":
			pkg, _, _ := strings.Cut(e.ImportPath, " ")
			r.buildErrors[pkg] = append(r.buildErrors[pkg], strings.TrimRight(e.Output, "\n"))
		case e.Action == "fail" && name != "":
			failedTests[e.Package] = true
			r.failed = appendUnique(r.failed, name)
		case e.Action == "pass" && name != "" && e.Test == name:
			r.passed = appendUnique(r.passed, name)
		case e.Action == "fail" && e.FailedBuild != "":
			if _, ok := r.buildErrors[e.Package]; !ok {
				r.buildErrors[e.Package] = nil
			}
		case e.Action == "fail" && !failedTests[e.Package]:
			r.failedPkgs = appendUnique(r.failedPkgs, e.Package)
		}
	}
	r.text = text.String()
	return r
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// changedTests returns the test functions that src declares and that old
// either lacks or declares differently, in source order. A nil old means
// the file is new. An old that doesn't parse counts as new.
func changedTests(old, src []byte) ([]string, error) {
	current, err := testFuncs(src)
	if err != nil {
		return nil, err
	}
	before := map[string]string{}
	if old != nil {
		if fs, err := testFuncs(old); err == nil {
			for _, f := range fs {
				before[f.name] = f.source
			}
		}
	}
	var names []string
	for _, f := range current {
		if prev, ok := before[f.name]; !ok || prev != f.source {
			names = append(names, f.name)
		}
	}
	return names, nil
}

type testFunc struct{ name, source string }

// testFuncs returns the `func TestXxx(t *testing.T)` declarations of a Go
// file with their source text.
func testFuncs(src []byte) ([]testFunc, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var funcs []testFunc
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || !isTestFunc(fd) {
			continue
		}
		start, end := fset.Position(fd.Pos()).Offset, fset.Position(fd.End()).Offset
		funcs = append(funcs, testFunc{name: fd.Name.Name, source: string(src[start:end])})
	}
	return funcs, nil
}

func isTestFunc(fd *ast.FuncDecl) bool {
	name := fd.Name.Name
	if fd.Recv != nil || !strings.HasPrefix(name, "Test") {
		return false
	}
	if rest := name[len("Test"):]; rest != "" {
		if r, _ := utf8.DecodeRuneInString(rest); unicode.IsLower(r) {
			return false
		}
	}
	params := fd.Type.Params.List
	if len(params) != 1 || len(params[0].Names) > 1 || fd.Type.Results != nil {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

// firstLines returns the first n non-empty lines of s, joined with "; ".
func firstLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
			if len(lines) == n {
				break
			}
		}
	}
	return strings.Join(lines, "; ")
}
