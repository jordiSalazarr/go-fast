// Package slices_test guards the vertical-slice boundaries.
package slices_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const slicesImportPath = "github.com/jordiSalazarr/go-fast/workflow/slices/"

// A slice owns everything specific to it; what several slices share lives at
// bounded-context level (eventlog, progress, ...). So no package under
// workflow/slices/ may import another one, test files included.
func TestSlicesDoNotImportEachOther(t *testing.T) {
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		own := strings.SplitN(filepath.ToSlash(path), "/", 2)[0]
		if !strings.Contains(filepath.ToSlash(path), "/") {
			return nil // this file, at the root of workflow/slices/
		}
		files++
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if other, ok := strings.CutPrefix(p, slicesImportPath); ok && strings.SplitN(other, "/", 2)[0] != own {
				t.Errorf("%s imports slice %s: slices must not import each other; move what they share to bounded-context level", path, other)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("found no slice files: is the test running in workflow/slices/?")
	}
}
