package sim

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// TestDeterminismLint enforces the rules of docs/tick.md section 5 on the
// non-test sources of this package.
func TestDeterminismLint(t *testing.T) {
	allowed := map[string]bool{
		"errors": true, "fmt": true, "slices": true, "math/bits": true,
		"encoding/binary": true, "crypto/sha256": true,
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if !allowed[path] {
					t.Errorf("%s: import %q is not on the allow list", name, path)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.MapType:
					t.Errorf("%s: map type", fset.Position(x.Pos()))
				case *ast.GoStmt:
					t.Errorf("%s: go statement", fset.Position(x.Pos()))
				case *ast.ChanType:
					t.Errorf("%s: channel type", fset.Position(x.Pos()))
				case *ast.Ident:
					switch x.Name {
					case "float32", "float64", "complex64", "complex128":
						t.Errorf("%s: %s", fset.Position(x.Pos()), x.Name)
					}
				case *ast.BasicLit:
					if x.Kind == token.FLOAT || x.Kind == token.IMAG {
						t.Errorf("%s: floating-point literal %s", fset.Position(x.Pos()), x.Value)
					}
				}
				return true
			})
		}
	}
}
