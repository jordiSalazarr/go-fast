package exitchecks

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// errSkeleton is the error a skeleton returns where its signature returns
// one; implement replaces every use of it.
const errSkeleton = "errSkeleton"

// skeletonsOnly: every non-test Go file this specify visit changed was
// added in it, and declares only what the specs need to compile.
func (c *checkRun) skeletonsOnly() (finding, error) {
	base, ok, err := c.ownBaseline()
	if err != nil || !ok {
		return c.noOwnBaseline(), err
	}
	var problems []string
	errSkeletons := map[string]int{} // per package directory
	for _, p := range c.visit.Changed.Paths() {
		if !domain.IsGoFile(p) {
			continue
		}
		_, existed, err := worktree.FileAt(c.Root, base, p)
		if err != nil {
			return finding{}, err
		}
		if existed {
			problems = append(problems, p.String()+": an existing production file changed; specify may only add skeleton files")
			continue
		}
		src, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(p.String())))
		if errors.Is(err, os.ErrNotExist) {
			continue // added and removed again in this visit
		}
		if err != nil {
			return finding{}, err
		}
		found, vars := skeletonProblems(p.String(), src)
		problems = append(problems, found...)
		errSkeletons[path.Dir(p.String())] += vars
	}
	for dir, n := range errSkeletons {
		if n > 1 {
			problems = append(problems, fmt.Sprintf("%s: %d declarations of errSkeleton; declare it once per package", dir, n))
		}
	}
	if len(problems) > 0 {
		return fail("%s", strings.Join(problems, "; ")), nil
	}
	return pass, nil
}

// skeletonProblems lists what in a skeleton file does more than declare:
// only the package clause, imports, types, constants, `var errSkeleton =
// errors.New("…")` and functions whose body is empty (without results) or
// a single return of zero values or errSkeleton are allowed. vars is how
// many errSkeleton declarations it has.
func skeletonProblems(name string, src []byte) (problems []string, vars int) {
	file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return []string{fmt.Sprintf("%s: doesn't parse: %v", name, firstLine(err.Error()))}, 0
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue // import, type and const
			}
			for _, spec := range d.Specs {
				if isErrSkeletonVar(spec.(*ast.ValueSpec)) {
					vars++
					continue
				}
				problems = append(problems, fmt.Sprintf("%s: var %s is not `var errSkeleton = errors.New(\"…\")`", name, valueNames(spec.(*ast.ValueSpec))))
			}
		case *ast.FuncDecl:
			if problem := funcProblem(d); problem != "" {
				problems = append(problems, fmt.Sprintf("%s: %s %s", name, funcName(d), problem))
			}
		default:
			problems = append(problems, name+": a declaration that doesn't parse")
		}
	}
	return problems, vars
}

func isErrSkeletonVar(spec *ast.ValueSpec) bool {
	if len(spec.Names) != 1 || spec.Names[0].Name != errSkeleton || spec.Type != nil || len(spec.Values) != 1 {
		return false
	}
	call, ok := spec.Values[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	fun, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || fun.Sel.Name != "New" {
		return false
	}
	if pkg, ok := fun.X.(*ast.Ident); !ok || pkg.Name != "errors" {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// funcProblem says what a skeleton function does beyond returning zero
// values; empty when it does nothing.
func funcProblem(f *ast.FuncDecl) string {
	if f.Body == nil {
		return "has no body"
	}
	if len(f.Body.List) == 0 {
		if f.Type.Results != nil && len(f.Type.Results.List) > 0 {
			return "has results but an empty body"
		}
		return ""
	}
	if len(f.Body.List) > 1 {
		return "has logic"
	}
	if panics(f.Body.List[0]) {
		return "panics (a panic aborts the package's other tests; return zero values or errSkeleton)"
	}
	ret, ok := f.Body.List[0].(*ast.ReturnStmt)
	if !ok {
		return "has logic"
	}
	for _, r := range ret.Results {
		if !isZeroValue(r) {
			return "has logic"
		}
	}
	return ""
}

func panics(stmt ast.Stmt) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	fun, ok := call.Fun.(*ast.Ident)
	return ok && fun.Name == "panic"
}

// isZeroValue: nil, false, a zero number, "", a composite literal with no
// elements (T{}, pkg.T{}), or errSkeleton.
func isZeroValue(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name == "nil" || v.Name == "false" || v.Name == errSkeleton
	case *ast.BasicLit:
		switch v.Kind {
		case token.INT, token.FLOAT:
			c := constant.MakeFromLiteral(v.Value, v.Kind, 0)
			return c.Kind() != constant.Unknown && constant.Sign(c) == 0
		case token.STRING:
			return v.Value == `""` || v.Value == "``"
		}
	case *ast.CompositeLit:
		return len(v.Elts) == 0
	case *ast.ParenExpr:
		return isZeroValue(v.X)
	}
	return false
}

func funcName(f *ast.FuncDecl) string {
	if f.Recv == nil || len(f.Recv.List) == 0 {
		return "func " + f.Name.Name
	}
	return "func (" + exprString(f.Recv.List[0].Type) + ") " + f.Name.Name
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return "*" + exprString(v.X)
	case *ast.IndexExpr:
		return exprString(v.X) + "[…]"
	case *ast.IndexListExpr:
		return exprString(v.X) + "[…]"
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	}
	return "…"
}

func valueNames(spec *ast.ValueSpec) string {
	names := make([]string, len(spec.Names))
	for i, n := range spec.Names {
		names[i] = n.Name
	}
	return strings.Join(names, ", ")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// skeletonsFilled: no non-test Go file of the repository declares or
// references errSkeleton.
func (c *checkRun) skeletonsFilled() (finding, error) {
	var left []string
	err := filepath.WalkDir(c.Root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// The go tool ignores directories starting with "." or "_", and
			// testdata; vendor holds other modules' code.
			if abs != c.Root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		if !strings.Contains(string(src), errSkeleton) || !usesErrSkeleton(abs, src) {
			return nil
		}
		rel, err := filepath.Rel(c.Root, abs)
		if err != nil {
			return err
		}
		left = append(left, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return finding{}, err
	}
	if len(left) > 0 {
		return fail("errSkeleton is still in %s; fill every skeleton specify wrote", strings.Join(left, ", ")), nil
	}
	return pass, nil
}

// usesErrSkeleton reports whether a Go file declares or references
// errSkeleton, not just mentions it in a comment or string. A file that
// doesn't parse counts when its text names it.
func usesErrSkeleton(name string, src []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return true
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == errSkeleton {
			found = true
		}
		return !found
	})
	return found
}
