// Command verifiedby reports feature_list.json entries whose passes flag is
// not backed by a named end-to-end test (#134).
//
// Every passing functional entry must list, in verified_by, the top-level
// e2e tests that prove it against the shipped binary. Any name listed, on
// any entry, must exist as a func TestXxx in e2e/. Infrastructure, style
// and testing entries are proven by CI and release jobs rather than by e2e
// tests, so verified_by is optional for them.
//
// It prints one finding per line, tab-separated:
//
//	missing	<id>
//	unknown	<id>	<test>
//
// and exits 0 when it finds problems, 1 only when the check cannot run. It
// also exits 1 when the feature list schema no longer declares verified_by:
// the schema is a specforge template that an upgrade can overwrite, and
// specforge validates feature_list.json against it.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type feature struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Passes     bool     `json:"passes"`
	VerifiedBy []string `json:"verified_by"`
}

type finding struct {
	Kind string // "missing" or "unknown"
	ID   string
	Test string
}

func (f finding) String() string {
	if f.Test == "" {
		return f.Kind + "\t" + f.ID
	}
	return f.Kind + "\t" + f.ID + "\t" + f.Test
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	findings, err := check(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verifiedby: %v\n", err)
		os.Exit(1)
	}
	for _, f := range findings {
		fmt.Println(f)
	}
}

func check(root string) ([]finding, error) {
	if err := schemaDeclaresVerifiedBy(filepath.Join(root, ".specify", "templates", "feature-list-schema.json")); err != nil {
		return nil, err
	}
	features, err := loadFeatures(filepath.Join(root, "feature_list.json"))
	if err != nil {
		return nil, err
	}
	tests, err := e2eTests(filepath.Join(root, "e2e"))
	if err != nil {
		return nil, err
	}
	return evaluate(features, tests), nil
}

// evaluate returns the findings for features given the set of e2e test names.
func evaluate(features []feature, tests map[string]bool) []finding {
	var out []finding
	for _, f := range features {
		if f.Passes && f.Category == "functional" && len(f.VerifiedBy) == 0 {
			out = append(out, finding{Kind: "missing", ID: f.ID})
		}
		for _, name := range f.VerifiedBy {
			if !tests[name] {
				out = append(out, finding{Kind: "unknown", ID: f.ID, Test: name})
			}
		}
	}
	return out
}

func loadFeatures(path string) ([]feature, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list struct {
		Features []feature `json:"features"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(list.Features) == 0 {
		return nil, fmt.Errorf("%s: no features", path)
	}
	return list.Features, nil
}

func schemaDeclaresVerifiedBy(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var schema struct {
		Properties struct {
			Features struct {
				Items struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"items"`
			} `json:"features"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, ok := schema.Properties.Features.Items.Properties["verified_by"]; !ok {
		return fmt.Errorf("%s does not declare verified_by; restore it (a specforge upgrade may have overwritten the template)", path)
	}
	return nil
}

// e2eTests returns the names of the top-level test functions in dir.
// Subtests are not addressable by name, so they are not collected.
func e2eTests(dir string) (map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: no test files", dir)
	}
	sort.Strings(files)
	tests := make(map[string]bool)
	fset := token.NewFileSet()
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && isTestName(fn.Name.Name) {
				tests[fn.Name.Name] = true
			}
		}
	}
	if len(tests) == 0 {
		return nil, errors.New(dir + ": no test functions")
	}
	return tests, nil
}

// isTestName reports whether name is a test function name as go test sees
// it: "Test" followed by nothing or by a character that is not lower case.
// TestMain is the package's setup hook, not a test.
func isTestName(name string) bool {
	if name == "TestMain" {
		return false
	}
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok {
		return false
	}
	return rest == "" || rest[0] < 'a' || rest[0] > 'z'
}
