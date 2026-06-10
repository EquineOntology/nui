package render_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPaintRule enforces SPEC §6's PAINT RULE structurally: every non-test .go
// file in render/ EXCEPT paint.go must NOT call a lipgloss Style's `.Render(`
// (the call that turns a style into an ANSI string). ANSI is produced in exactly
// one place — paint.go. This is the grep the spec asks for, run as a test so it
// gates CI rather than relying on a human remembering to look.
func TestPaintRule(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read render dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue // tests legitimately call Paint, which Renders internally
		}
		if name == "paint.go" {
			continue // the one sanctioned ANSI producer
		}
		// Parse the AST and look for selector-call expressions named exactly
		// "Render" (a lipgloss Style.Render() call). This ignores comments and
		// the renderer's own RenderBlock method, which a naive substring grep
		// would false-positive on.
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Render" {
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d calls .Render() — ANSI must be produced only in paint.go (SPEC §6 PAINT RULE)",
					name, pos.Line)
			}
			return true
		})
	}
}
