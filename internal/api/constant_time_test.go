package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The credential checks in requireAuth and checkPassword must not compare a
// secret with == or != (SEC-018): the username goes through
// subtle.ConstantTimeCompare and the password through bcrypt. Comparing with
// the empty string literal is allowed; it reveals only whether a value is set.
func TestCredentialComparisonsAreConstantTime(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "api.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]bool{"user": true, "pass": true, "plain": true, "hash": true, "authUser": true, "authPass": true}
	isSecret := func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.Ident:
			return secrets[e.Name]
		case *ast.SelectorExpr:
			return secrets[e.Sel.Name]
		}
		return false
	}
	isEmptyString := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Kind == token.STRING && lit.Value == `""`
	}

	want := map[string][]string{
		"requireAuth":   {"subtle.ConstantTimeCompare", "checkPassword"},
		"checkPassword": {"bcrypt.CompareHashAndPassword"},
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || want[fn.Name.Name] == nil {
			continue
		}
		calls := map[string]bool{}
		ast.Inspect(fn, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if n.Op != token.EQL && n.Op != token.NEQ {
					return true
				}
				if (isSecret(n.X) && !isEmptyString(n.Y)) || (isSecret(n.Y) && !isEmptyString(n.X)) {
					t.Errorf("%s: %s compares a credential with %s", fn.Name.Name, fset.Position(n.Pos()), n.Op)
				}
			case *ast.CallExpr:
				switch fun := n.Fun.(type) {
				case *ast.Ident:
					calls[fun.Name] = true
				case *ast.SelectorExpr:
					if x, ok := fun.X.(*ast.Ident); ok {
						calls[x.Name+"."+fun.Sel.Name] = true
					}
				}
			}
			return true
		})
		for _, c := range want[fn.Name.Name] {
			if !calls[c] {
				t.Errorf("%s does not call %s", fn.Name.Name, c)
			}
		}
		delete(want, fn.Name.Name)
	}
	for name := range want {
		t.Errorf("function %s not found in api.go", name)
	}
}
