package response

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestErrorCodeValuesUnique enforces the one hard invariant the codebase
// relies on but never checked at build time: no two error-code constants may
// share the same string value. A duplicate value would make two distinct
// failure modes indistinguishable to the frontend (which keys its i18n
// messages off the code string) and to audit/log filters.
//
// Note on scope: this deliberately does NOT assert one HTTP status per code.
// The codes are coarse error categories — e.g. ErrInvalidRequest, ErrCronError
// or ErrFileError legitimately surface as different statuses (400/409, or
// 400/500/503) depending on the failure context — so the status is the
// caller's contextual choice (see response.Fail), not a property of the code.
func TestErrorCodeValuesUnique(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read response package dir: %v", err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		files = append(files, file)
	}

	seen := make(map[string]string) // value -> first constant name that used it
	count := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Err") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					val, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						continue
					}
					count++
					if prev, dup := seen[val]; dup {
						t.Errorf("duplicate error-code value %q shared by %s and %s", val, prev, name.Name)
						continue
					}
					seen[val] = name.Name
				}
			}
		}
	}

	if count == 0 {
		t.Fatal("found no Err* string constants — test is not scanning errors.go correctly")
	}
	t.Logf("verified %d error-code constants, %d distinct values", count, len(seen))
}
