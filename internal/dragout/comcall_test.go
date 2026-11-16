package dragout

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestComCallKeepsPointerArgumentsAlive guards the Windows COM call helper
// from any host platform by inspecting its source.
//
// comCall receives the addresses of out-params as uintptr. That is only safe
// while the function carries the go:uintptrescapes directive, and the
// directive only takes effect for direct calls, so both are checked.
func TestComCallKeepsPointerArgumentsAlive(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "dragout_windows.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "comCall" {
			decl = fn
		}
	}
	if decl == nil {
		t.Fatal("comCall not found in dragout_windows.go")
	}

	const directive = "//go:uintptrescapes"
	hasDirective := false
	if decl.Doc != nil {
		for _, c := range decl.Doc.List {
			// A directive must start in the first column to be honoured.
			if c.Text == directive && fset.Position(c.Slash).Column == 1 {
				hasDirective = true
			}
		}
	}
	if !hasDirective {
		t.Errorf("comCall must be marked %s: without it the out-params whose addresses are passed as uintptr stay on the goroutine stack", directive)
	}

	callees := map[*ast.Ident]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				callees[id] = true
			}
		}
		return true
	})
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != "comCall" || id == decl.Name || callees[id] {
			return true
		}
		t.Errorf("%s: comCall is used as a value; %s only applies to direct calls", fset.Position(id.Pos()), directive)
		return true
	})
}
