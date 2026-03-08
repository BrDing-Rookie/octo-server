package event

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestCommitHasNilCheck verifies that the Commit function has a nil check
// for eventModel before calling handleEvent. This is a static analysis test
// that does not require a database connection.
//
// Issue #339: Commit() could panic when eventModel was nil because
// handleEvent() accesses model.Event without nil checking.
func TestCommitHasNilCheck(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "api.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse api.go: %v", err)
	}

	var commitFunc *ast.FuncDecl
	for _, decl := range node.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if fn.Name.Name == "Commit" {
				commitFunc = fn
				break
			}
		}
	}

	if commitFunc == nil {
		t.Fatal("Commit function not found in api.go")
	}

	// Look for a nil check on eventModel in the function body
	hasNilCheck := false
	ast.Inspect(commitFunc.Body, func(n ast.Node) bool {
		if binExpr, ok := n.(*ast.BinaryExpr); ok {
			// Check for "eventModel == nil" or "eventModel != nil"
			if binExpr.Op == token.EQL || binExpr.Op == token.NEQ {
				if ident, ok := binExpr.X.(*ast.Ident); ok {
					if ident.Name == "eventModel" {
						if nilIdent, ok := binExpr.Y.(*ast.Ident); ok {
							if nilIdent.Name == "nil" {
								hasNilCheck = true
								return false
							}
						}
					}
				}
			}
		}
		return true
	})

	if !hasNilCheck {
		t.Error("Commit function is missing nil check for eventModel. " +
			"This could cause a panic when QueryWithID returns nil for non-existent events. " +
			"See issue #339.")
	}
}
