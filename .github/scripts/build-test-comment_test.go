package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeJUnit writes body to a temp JUnit file and returns its path.
func writeJUnit(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "results.xml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

// junitDoc assembles a <testsuites> document from raw <testcase> fragments.
func junitDoc(rootTime float64, tests, failures, skipped int, cases string) string {
	return fmt.Sprintf(
		`<testsuites time="%g"><testsuite name="pkg" tests="%d" failures="%d" errors="0" skipped="%d" time="%g">%s</testsuite></testsuites>`,
		rootTime, tests, failures, skipped, rootTime, cases,
	)
}

func passCase(name string, secs float64) string {
	return fmt.Sprintf(`<testcase classname="github.com/kahiteam/kahi/pkg" name="%s" time="%g"></testcase>`, name, secs)
}

func failCase(name, msg string) string {
	return fmt.Sprintf(`<testcase classname="github.com/kahiteam/kahi/pkg" name="%s" time="0.01"><failure message="%s">stack</failure></testcase>`, name, msg)
}

func skipCase(name, reason string) string {
	return fmt.Sprintf(`<testcase classname="github.com/kahiteam/kahi/pkg" name="%s" time="0"><skipped message="%s"></skipped></testcase>`, name, reason)
}

// TestCompactModePreservesDefaultBehavior verifies the non-full twisty carries
// only failures/skipped plus an "all passed" line on green.
func TestCompactModeGreenShowsAllPassedNoTable(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 2, 0, 0,
		passCase("TestA", 0.1)+passCase("TestB", 0.2)))

	out := render("Unit test results", "Unit", junit, false)

	if !strings.Contains(out, "All 2 tests passed.") {
		t.Fatalf("compact green tile missing all-passed line:\n%s", out)
	}
	if strings.Contains(out, "| Test | Result | Duration |") {
		t.Fatalf("compact tile must not contain the full per-test table:\n%s", out)
	}
	if strings.Contains(out, "<details open>") {
		t.Fatalf("green tile must stay collapsed:\n%s", out)
	}
}

func TestCompactModeFailureExpandsAndListsFailure(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 2, 1, 0,
		passCase("TestA", 0.1)+failCase("TestB", "boom")))

	out := render("Unit test results", "Unit", junit, false)

	if !strings.Contains(out, "<details open>") {
		t.Fatalf("failing tile must be expanded:\n%s", out)
	}
	if !strings.Contains(out, "#### ❌ Failures") {
		t.Fatalf("failing tile must list failures:\n%s", out)
	}
	if strings.Contains(out, "| Test | Result | Duration |") {
		t.Fatalf("compact tile must not contain the full table:\n%s", out)
	}
}

// TestFullModeRendersEveryTest checks the opt-in table lists every test with
// the Test/Result/Duration columns.
func TestFullModeRendersEveryTestWithColumns(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 3, 1, 1,
		passCase("TestPass", 0.12)+failCase("TestFail", "boom")+skipCase("TestSkip", "nope")))

	out := render("Unit test results", "Unit", junit, true)

	if !strings.Contains(out, "| Test | Result | Duration |") {
		t.Fatalf("full tile missing table header:\n%s", out)
	}
	for _, n := range []string{"TestPass", "TestFail", "TestSkip"} {
		if !strings.Contains(out, n) {
			t.Fatalf("full table missing test %q:\n%s", n, out)
		}
	}
	if !strings.Contains(out, "0.120s") {
		t.Fatalf("full table missing per-test duration:\n%s", out)
	}
}

// TestFullModeOrdersFailuresSkippedPassed checks the row ordering contract.
func TestFullModeOrdersFailuresSkippedPassed(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 3, 1, 1,
		passCase("TestPassed", 0.1)+skipCase("TestSkipped", "nope")+failCase("TestFailed", "boom")))

	out := render("Unit test results", "Unit", junit, true)

	iFail := strings.Index(out, "TestFailed")
	iSkip := strings.Index(out, "TestSkipped")
	iPass := strings.Index(out, "TestPassed")
	if iFail < 0 || iSkip < 0 || iPass < 0 {
		t.Fatalf("expected all three tests present:\n%s", out)
	}
	if !(iFail < iSkip && iSkip < iPass) {
		t.Fatalf("rows must be ordered failures → skipped → passed, got fail=%d skip=%d pass=%d\n%s",
			iFail, iSkip, iPass, out)
	}
}

// TestFullModeStatusEmojis checks each status carries its emoji + word.
func TestFullModeStatusEmojis(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 3, 1, 1,
		passCase("TestPass", 0.1)+failCase("TestFail", "boom")+skipCase("TestSkip", "nope")))

	out := render("Unit test results", "Unit", junit, true)

	for _, want := range []string{"✅ Passed", "⚠️ Skipped", "❌ Failed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("full table missing status marker %q:\n%s", want, out)
		}
	}
}

// TestFullModeGreenStaysCollapsed confirms the opt-in changes contents, not the
// collapse-on-green behavior.
func TestFullModeGreenStaysCollapsed(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 2, 0, 0,
		passCase("TestA", 0.1)+passCase("TestB", 0.2)))

	out := render("Unit test results", "Unit", junit, true)

	if strings.Contains(out, "<details open>") {
		t.Fatalf("green full tile must stay collapsed:\n%s", out)
	}
	if !strings.Contains(out, "| Test | Result | Duration |") {
		t.Fatalf("green full tile must still render the table:\n%s", out)
	}
}

// TestFullModeTruncationKeepsFailuresAndSkipped drives a suite whose full table
// exceeds the comment limit and checks deterministic truncation.
func TestFullModeTruncationKeepsFailuresAndSkipped(t *testing.T) {
	var cases strings.Builder
	cases.WriteString(failCase("TestFailKept", "boom"))
	cases.WriteString(skipCase("TestSkipKept", "nope"))
	const nPassed = 4000
	for i := 0; i < nPassed; i++ {
		cases.WriteString(passCase(fmt.Sprintf("TestPassed%04d", i), 0.001))
	}
	junit := writeJUnit(t, junitDoc(1.0, nPassed+2, 1, 1, cases.String()))

	out := render("Unit test results", "Unit", junit, true)

	if len(out) > maxCommentBytes {
		t.Fatalf("truncated comment is %d bytes, exceeds limit %d", len(out), maxCommentBytes)
	}
	if !strings.Contains(out, "TestFailKept") {
		t.Fatalf("truncation dropped a failure row:\n%s", out[:512])
	}
	if !strings.Contains(out, "TestSkipKept") {
		t.Fatalf("truncation dropped a skipped row")
	}
	if !strings.Contains(out, "passed rows omitted") {
		t.Fatalf("truncation must append an omission note")
	}

	// The omission note count must match the passed rows actually dropped.
	var kept int
	for i := 0; i < nPassed; i++ {
		if strings.Contains(out, fmt.Sprintf("TestPassed%04d", i)) {
			kept++
		}
	}
	omitted := nPassed - kept
	if omitted <= 0 {
		t.Fatalf("expected some passed rows to be omitted, kept=%d", kept)
	}
	if !strings.Contains(out, fmt.Sprintf("%d passed rows omitted", omitted)) {
		t.Fatalf("omission note count wrong; kept=%d omitted=%d\nnote region: %s",
			kept, omitted, out[len(out)-200:])
	}
}

// TestFullModeNoTruncationWhenSmall confirms no omission note appears when
// everything fits.
func TestFullModeNoTruncationWhenSmall(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 2, 0, 0,
		passCase("TestA", 0.1)+passCase("TestB", 0.2)))

	out := render("Unit test results", "Unit", junit, true)

	if strings.Contains(out, "passed rows omitted") {
		t.Fatalf("small suite must not emit an omission note:\n%s", out)
	}
}

// TestFullModeEscapesPipeAndBacktick checks names with markdown metacharacters
// cannot break the table.
func TestFullModeEscapesPipeAndBacktick(t *testing.T) {
	junit := writeJUnit(t, junitDoc(0.5, 1, 0, 0,
		passCase("TestPipe/a|b_and_`tick`", 0.1)))

	out := render("Unit test results", "Unit", junit, true)

	if !strings.Contains(out, "\\|") {
		t.Fatalf("pipe in test name must be escaped:\n%s", out)
	}
	// A name containing a backtick must be fenced with a longer run so it does
	// not terminate the code span early.
	if !strings.Contains(out, "`` ") || !strings.Contains(out, " ``") {
		t.Fatalf("backtick in test name must widen the code fence:\n%s", out)
	}
	// Every data row must present exactly three columns (4 pipes) after the
	// escaping; count only genuine column separators.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "| `") || strings.HasPrefix(line, "| ``") {
			cols := strings.Count(line, "|") - strings.Count(line, "\\|")
			if cols != 4 {
				t.Fatalf("row has %d column separators, want 4: %q", cols, line)
			}
		}
	}
}

// TestMissingJUnitYieldsNoResults confirms the no-results path is unaffected by
// the full flag.
func TestMissingJUnitYieldsNoResults(t *testing.T) {
	out := render("Unit test results", "Unit", filepath.Join(t.TempDir(), "absent.xml"), true)
	if !strings.Contains(out, "no results") {
		t.Fatalf("missing JUnit must yield a no-results tile:\n%s", out)
	}
}
