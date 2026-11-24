package clierr

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEverySentinelHasACode walks the module source and fails when an
// exported package-level Err* sentinel has no catalog entry. Unexported
// sentinels are internal bookkeeping and never reach a CLI error.
func TestEverySentinelHasACode(t *testing.T) {
	found := map[string]bool{}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "site", "vendor", "docs", "report", "bin", "dist":
				return fs.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." && d.Name() != ".." {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if !isExportedSentinel(name.Name) || i >= len(vs.Values) {
						continue
					}
					if isSentinelValue(vs.Values[i]) {
						found[f.Name.Name+"."+name.Name] = true
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("found no sentinels; the scan is broken")
	}

	catalogued := map[string]bool{}
	for _, e := range Catalog() {
		if e.SentinelName == "" {
			continue
		}
		if catalogued[e.SentinelName] {
			t.Errorf("%s appears twice in the catalog", e.SentinelName)
		}
		catalogued[e.SentinelName] = true
		if e.Err == nil {
			t.Errorf("%s has no sentinel value", e.SentinelName)
		}
		if code, exit := Classify(e.Err); code != e.Code || exit != e.Exit {
			t.Errorf("Classify(%s) = %s/%d, want %s/%d", e.SentinelName, code, exit, e.Code, e.Exit)
		}
	}

	var missing, stale []string
	for name := range found {
		if !catalogued[name] {
			missing = append(missing, name)
		}
	}
	for name := range catalogued {
		if !found[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("sentinels with no error code: %s\nadd an entry to internal/clierr/catalog", strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("catalog names sentinels that no longer exist: %s", strings.Join(stale, ", "))
	}
}

func isExportedSentinel(name string) bool {
	return strings.HasPrefix(name, "Err") && len(name) > 3 && name[3] >= 'A' && name[3] <= 'Z'
}

// isSentinelValue accepts errors.New(...) and fmt.Errorf(...); those are how
// this module defines sentinels.
func isSentinelValue(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return (pkg.Name == "errors" && sel.Sel.Name == "New") || (pkg.Name == "fmt" && sel.Sel.Name == "Errorf")
}

func TestCatalogPageIsCurrent(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "errors.md"))
	if err != nil {
		t.Fatalf("docs/errors.md: %v; run scripts/gen-error-catalog.sh", err)
	}
	if string(b) != Markdown() {
		t.Fatal("docs/errors.md is stale; run scripts/gen-error-catalog.sh")
	}
}

func TestClassifyTaggedAndUnknown(t *testing.T) {
	if code, exit := Classify(Newf("retention_gap", "ledger %d is gone", 7)); code != "retention_gap" || exit != 1 {
		t.Fatalf("tagged = %s/%d", code, exit)
	}
	if code, exit := Classify(Newf("no_such_code", "typo")); code != "internal" || exit != 1 {
		t.Fatalf("unknown code = %s/%d, want internal/1", code, exit)
	}
	if code, exit := Classify(os.ErrNotExist); code != "internal" || exit != 1 {
		t.Fatalf("plain error = %s/%d, want internal/1", code, exit)
	}
	entries := Catalog()
	entries[0].Code = "mutated"
	if Catalog()[0].Code == "mutated" {
		t.Fatal("Catalog exposes the internal slice")
	}
}
