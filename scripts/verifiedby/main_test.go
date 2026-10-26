package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	tests := map[string]bool{"TestA": true, "TestB": true}
	features := []feature{
		{ID: "proven", Category: "functional", Passes: true, VerifiedBy: []string{"TestA"}},
		{ID: "unproven", Category: "functional", Passes: true},
		{ID: "typo", Category: "functional", Passes: true, VerifiedBy: []string{"TestB", "TestGone"}},
		{ID: "not-passing", Category: "functional"},
		{ID: "infra", Category: "infrastructure", Passes: true},
		{ID: "infra-named", Category: "infrastructure", Passes: true, VerifiedBy: []string{"TestNope"}},
		{ID: "pending", Category: "functional", VerifiedBy: []string{"TestMissing"}},
	}
	got := evaluate(features, tests)
	want := []finding{
		{Kind: "missing", ID: "unproven"},
		{Kind: "unknown", ID: "typo", Test: "TestGone"},
		{Kind: "unknown", ID: "infra-named", Test: "TestNope"},
		{Kind: "unknown", ID: "pending", Test: "TestMissing"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evaluate:\n got %v\nwant %v", got, want)
	}
}

func TestFindingString(t *testing.T) {
	if got := (finding{Kind: "missing", ID: "x"}).String(); got != "missing\tx" {
		t.Errorf("got %q", got)
	}
	if got := (finding{Kind: "unknown", ID: "x", Test: "TestY"}).String(); got != "unknown\tx\tTestY" {
		t.Errorf("got %q", got)
	}
}

func TestIsTestName(t *testing.T) {
	for name, want := range map[string]bool{
		"Test":       true,
		"TestFoo":    true,
		"Test_foo":   true,
		"Testify":    false,
		"TestMain":   false,
		"BenchmarkX": false,
		"helper":     false,
	} {
		if got := isTestName(name); got != want {
			t.Errorf("isTestName(%q) = %v, want %v", name, got, want)
		}
	}
}

// writeRepo lays out a minimal repository under a temp dir.
func writeRepo(t *testing.T, schema, features, e2e string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		if content == "" {
			return
		}
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".specify/templates/feature-list-schema.json", schema)
	write("feature_list.json", features)
	write("e2e/a_test.go", e2e)
	return root
}

const (
	goodSchema   = `{"properties":{"features":{"items":{"properties":{"id":{},"verified_by":{}}}}}}`
	goodFeatures = `{"features":[{"id":"a","category":"functional","passes":true,"verified_by":["TestA"]},{"id":"b","category":"functional","passes":true}]}`
	goodE2E      = "package e2e\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) {}\n\nfunc TestA(t *testing.T) { t.Run(\"Sub\", nil) }\n\nfunc (s suite) TestMethod(t *testing.T) {}\n\ntype suite struct{}\n"
)

func TestCheck(t *testing.T) {
	root := writeRepo(t, goodSchema, goodFeatures, goodE2E)
	got, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []finding{{Kind: "missing", ID: "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCheckErrors(t *testing.T) {
	for _, tc := range []struct {
		name, schema, features, e2e, want string
	}{
		{"schema without verified_by", `{"properties":{"features":{"items":{"properties":{"id":{}}}}}}`, goodFeatures, goodE2E, "does not declare verified_by"},
		{"schema missing", "", goodFeatures, goodE2E, "feature-list-schema.json"},
		{"schema invalid", "{", goodFeatures, goodE2E, "feature-list-schema.json"},
		{"features missing", goodSchema, "", goodE2E, "feature_list.json"},
		{"features invalid", goodSchema, `{"features":[{"id":"a","verified_by":"TestA"}]}`, goodE2E, "feature_list.json"},
		{"features empty", goodSchema, `{"features":[]}`, goodE2E, "no features"},
		{"no e2e files", goodSchema, goodFeatures, "", "no test files"},
		{"e2e unparsable", goodSchema, goodFeatures, "package e2e\nfunc {", "a_test.go"},
		{"e2e without tests", goodSchema, goodFeatures, "package e2e\n\nfunc helper() {}\n", "no test functions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeRepo(t, tc.schema, tc.features, tc.e2e)
			_, err := check(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestRepository runs the check against this repository, so a broken
// feature list or schema fails the unit tests, not only the CI warning job.
func TestRepository(t *testing.T) {
	if _, err := check(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
}
