// Command build-test-comment renders a gotestsum JUnit XML report into a
// markdown "tile" for a sticky pull-request comment.
//
// It is invoked from CI once per test surface (unit, integration, e2e) with
// go run so no build artifact is produced. The file lives under .github/ on
// purpose: the go tool ignores dot-prefixed directories, so this main package
// is never part of the module's build, vet, lint, or coverage.
//
// The tile is a compact summary table (the stats) followed by a <details>
// block. The block is collapsed on a green run and auto-expanded on failure;
// it carries the failing and skipped tests, which are the actionable part.
// The full per-test breakdown stays in the run log — dumping every passing
// case here would bloat the comment on a large suite.
//
// Usage:
//
//	go run .github/scripts/build-test-comment.go \
//	    -junit unit-results.xml \
//	    -title "Unit test results" \
//	    -output pr-comment-unit.md
//
// A missing or unparseable JUnit file yields a "no results" tile rather than
// an error, so the comment still posts when the test runner dies early.
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"strings"
)

type testsuites struct {
	Time   float64     `xml:"time,attr"`
	Suites []testsuite `xml:"testsuite"`
}

type testsuite struct {
	Name     string     `xml:"name,attr"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Errors   int        `xml:"errors,attr"`
	Skipped  int        `xml:"skipped,attr"`
	Time     float64    `xml:"time,attr"`
	Cases    []testcase `xml:"testcase"`
}

type testcase struct {
	Name      string   `xml:"name,attr"`
	Classname string   `xml:"classname,attr"`
	Time      float64  `xml:"time,attr"`
	Failure   *problem `xml:"failure"`
	Error     *problem `xml:"error"`
	Skipped   *skip    `xml:"skipped"`
}

type problem struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type skip struct {
	Message string `xml:"message,attr"`
}

// modulePrefix is trimmed from JUnit classnames (gotestsum sets classname to
// the full package import path) to keep test identifiers readable.
const modulePrefix = "github.com/kahiteam/kahi/"

func main() {
	junit := flag.String("junit", "", "path to the gotestsum JUnit XML file")
	title := flag.String("title", "Test results", "heading shown at the top of the tile")
	label := flag.String("label", "", "short suite name for the summary row (defaults to -title)")
	output := flag.String("output", "", "path to write the markdown tile")
	flag.Parse()

	if *junit == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "build-test-comment: -junit and -output are required")
		os.Exit(2)
	}

	row := *label
	if row == "" {
		row = *title
	}
	md := render(*title, row, *junit)
	if err := os.WriteFile(*output, []byte(md), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "build-test-comment: writing %s: %v\n", *output, err)
		os.Exit(1)
	}
}

func render(title, label, junitPath string) string {
	data, err := os.ReadFile(junitPath)
	if err != nil {
		return noResults(title, "Test runner produced no JUnit XML — see the run log for the underlying error.")
	}

	suites, rootTime, err := parse(data)
	if err != nil {
		return noResults(title, fmt.Sprintf("Could not parse %s: %v", junitPath, err))
	}

	var tests, failures, errors, skipped int
	var summed float64
	var cases []testcase
	for _, s := range suites {
		tests += s.Tests
		failures += s.Failures
		errors += s.Errors
		skipped += s.Skipped
		summed += s.Time
		cases = append(cases, s.Cases...)
	}
	// Packages run in parallel, so the summed per-suite time overcounts wall
	// time. Prefer the <testsuites> root time attribute (true elapsed) when set.
	elapsed := rootTime
	if elapsed == 0 {
		elapsed = summed
	}
	passed := tests - failures - errors - skipped
	green := failures+errors == 0

	var b strings.Builder
	status := "✅ pass"
	if !green {
		status = "❌ fail"
	}
	fmt.Fprintf(&b, "### %s — %s\n\n", title, status)
	b.WriteString("| Suite | Tests | Passed | Skipped | Failed |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(&b, "| %s | %d ran | %d ✅ | %d ⚠️ | %d ❌ |\n\n",
		label, tests, passed, skipped, failures+errors)

	openAttr := ""
	if !green {
		openAttr = " open"
	}
	fmt.Fprintf(&b, "<details%s>\n", openAttr)
	fmt.Fprintf(&b, "<summary><strong>Details — %d tests in %.2fs</strong></summary>\n\n", tests, elapsed)

	failed := failedCases(cases)
	if len(failed) > 0 {
		b.WriteString("#### ❌ Failures\n\n")
		for _, tc := range failed {
			writeFailure(&b, tc)
		}
	}

	skips := skippedCases(cases)
	if len(skips) > 0 {
		b.WriteString("#### ⚠️ Skipped\n\n")
		for _, tc := range skips {
			reason := "(no reason given)"
			if tc.Skipped != nil && tc.Skipped.Message != "" {
				reason = tc.Skipped.Message
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", name(tc), reason)
		}
		b.WriteString("\n")
	}

	if len(failed) == 0 && len(skips) == 0 {
		fmt.Fprintf(&b, "All %d tests passed.\n\n", tests)
	}

	b.WriteString("</details>\n")
	return b.String()
}

// parse accepts either a <testsuites> root (gotestsum's default, wrapping one
// suite per package) or a bare <testsuite> root.
func parse(data []byte) ([]testsuite, float64, error) {
	var multi testsuites
	if err := xml.Unmarshal(data, &multi); err == nil && len(multi.Suites) > 0 {
		return multi.Suites, multi.Time, nil
	}
	var single testsuite
	if err := xml.Unmarshal(data, &single); err != nil {
		return nil, 0, err
	}
	return []testsuite{single}, single.Time, nil
}

func failedCases(cases []testcase) []testcase {
	var out []testcase
	for _, tc := range cases {
		if tc.Failure != nil || tc.Error != nil {
			out = append(out, tc)
		}
	}
	return out
}

func skippedCases(cases []testcase) []testcase {
	var out []testcase
	for _, tc := range cases {
		if tc.Skipped != nil && tc.Failure == nil && tc.Error == nil {
			out = append(out, tc)
		}
	}
	return out
}

func writeFailure(b *strings.Builder, tc testcase) {
	p := tc.Failure
	if p == nil {
		p = tc.Error
	}
	fmt.Fprintf(b, "**`%s`**\n\n", name(tc))
	if p != nil && strings.TrimSpace(p.Message) != "" {
		fmt.Fprintf(b, "_%s_\n\n", strings.TrimSpace(p.Message))
	}
	if p != nil {
		body := strings.TrimSpace(p.Body)
		if body != "" {
			b.WriteString("```\n")
			b.WriteString(truncate(body, 30))
			b.WriteString("\n```\n\n")
		}
	}
}

// truncate keeps a long failure body readable: the first and last lines with
// an elision marker when it exceeds max lines. The full trace is in the log.
func truncate(body string, max int) string {
	lines := strings.Split(body, "\n")
	if len(lines) <= max {
		return body
	}
	head := lines[:15]
	tail := lines[len(lines)-15:]
	kept := append(append(append([]string{}, head...), "... [truncated] ..."), tail...)
	return strings.Join(kept, "\n")
}

func name(tc testcase) string {
	class := strings.TrimPrefix(tc.Classname, modulePrefix)
	if class == "" {
		return tc.Name
	}
	return class + "." + tc.Name
}

func noResults(title, detail string) string {
	return fmt.Sprintf("### %s — ⚠️ no results\n\n%s\n", title, detail)
}
