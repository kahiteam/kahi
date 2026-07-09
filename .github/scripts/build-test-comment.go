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
// With -full (opt-in, driven by the full-test-report PR label) the <details>
// block instead carries a table of every test — columns Test, Result (emoji +
// word), Duration — ordered failures → skipped → passed. If the comment would
// exceed GitHub's 65536-character limit, passed rows are dropped from the tail
// (failures and skipped are always kept) and an explicit omission note is
// appended. The collapse-on-green behaviour is unchanged by the flag.
//
// Usage:
//
//	go run .github/scripts/build-test-comment.go \
//	    -junit unit-results.xml \
//	    -title "Unit test results" \
//	    [-full] \
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

// maxCommentBytes is GitHub's hard limit on a single issue/PR comment body
// (65536 characters). The full per-test table is truncated to stay under it.
// The budget is measured in bytes, a conservative proxy for GitHub's character
// count (a multibyte rune is one character but several bytes), so we only ever
// truncate earlier than strictly required, never later.
const maxCommentBytes = 65536

func main() {
	junit := flag.String("junit", "", "path to the gotestsum JUnit XML file")
	title := flag.String("title", "Test results", "heading shown at the top of the tile")
	label := flag.String("label", "", "short suite name for the summary row (defaults to -title)")
	output := flag.String("output", "", "path to write the markdown tile")
	full := flag.Bool("full", false, "render the complete per-test table (opt-in full report)")
	flag.Parse()

	if *junit == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "build-test-comment: -junit and -output are required")
		os.Exit(2)
	}

	row := *label
	if row == "" {
		row = *title
	}
	md := render(*title, row, *junit, *full)
	if err := os.WriteFile(*output, []byte(md), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "build-test-comment: writing %s: %v\n", *output, err)
		os.Exit(1)
	}
}

func render(title, label, junitPath string, full bool) string {
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

	if full {
		// The closing </details>\n is part of the comment, so reserve it when
		// budgeting the truncatable table.
		const closer = "</details>\n"
		budget := maxCommentBytes - b.Len() - len(closer)
		writeFullTable(&b, cases, budget)
	} else {
		writeCompactBody(&b, cases, tests)
	}

	b.WriteString("</details>\n")
	return b.String()
}

// writeCompactBody renders the default twisty contents: only failing and
// skipped tests, plus an "all passed" line on a clean run.
func writeCompactBody(b *strings.Builder, cases []testcase, tests int) {
	failed := failedCases(cases)
	if len(failed) > 0 {
		b.WriteString("#### ❌ Failures\n\n")
		for _, tc := range failed {
			writeFailure(b, tc)
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
			fmt.Fprintf(b, "- `%s` — %s\n", name(tc), reason)
		}
		b.WriteString("\n")
	}

	if len(failed) == 0 && len(skips) == 0 {
		fmt.Fprintf(b, "All %d tests passed.\n\n", tests)
	}
}

// writeFullTable renders the opt-in full per-test table: every test as one row
// with columns Test, Result (emoji + word), and Duration, ordered failures →
// skipped → passed. Failing and skipped rows are always included; passed rows
// are capped so the whole comment stays within budget bytes, with an explicit
// omission note when any are dropped. Truncation is deterministic: passed rows
// are kept in their original order and dropped from the tail.
func writeFullTable(b *strings.Builder, cases []testcase, budget int) {
	failed := failedCases(cases)
	skips := skippedCases(cases)
	passed := passedCases(cases)

	b.WriteString("| Test | Result | Duration |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, tc := range failed {
		b.WriteString(fullRow(tc, "❌ Failed"))
	}
	for _, tc := range skips {
		b.WriteString(fullRow(tc, "⚠️ Skipped"))
	}

	// Reserve room for the worst-case omission note (all passed rows dropped)
	// so appending it later can never push the comment past the limit.
	noteReserve := len(omissionNote(len(passed)))
	remaining := budget - b.Len()

	included := 0
	for _, tc := range passed {
		row := fullRow(tc, "✅ Passed")
		if included+len(row)+noteReserve > remaining {
			break
		}
		b.WriteString(row)
		included += len(row)
	}

	if omitted := len(passed) - countRows(passed, included); omitted > 0 {
		b.WriteString(omissionNote(omitted))
	}
}

// countRows reports how many leading passed rows fit within writtenBytes; it
// mirrors the accumulation in writeFullTable so the omission count is exact.
func countRows(passed []testcase, writtenBytes int) int {
	used, n := 0, 0
	for _, tc := range passed {
		row := fullRow(tc, "✅ Passed")
		if used+len(row) > writtenBytes {
			break
		}
		used += len(row)
		n++
	}
	return n
}

// omissionNote is the deterministic marker appended when passed rows are
// dropped to satisfy the comment size limit.
func omissionNote(n int) string {
	return fmt.Sprintf("\n> ⚠️ %d passed rows omitted — see run log for the full list.\n", n)
}

// fullRow renders one per-test table row. The test name is code-spanned and
// pipe-escaped so it cannot break the table columns or inject markup.
func fullRow(tc testcase, result string) string {
	return fmt.Sprintf("| %s | %s | %.3fs |\n", cell(name(tc)), result, tc.Time)
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

func passedCases(cases []testcase) []testcase {
	var out []testcase
	for _, tc := range cases {
		if tc.Failure == nil && tc.Error == nil && tc.Skipped == nil {
			out = append(out, tc)
		}
	}
	return out
}

// cell renders s as a table-cell code span that cannot break the surrounding
// markdown table. Newlines and pipes are neutralised, and the backtick fence is
// grown past the longest run of backticks in s (CommonMark's escaping rule) so
// names containing backticks still render as inline code.
func cell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "|", "\\|")

	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if longest == 0 {
		return "`" + s + "`"
	}
	fence := strings.Repeat("`", longest+1)
	// Pad so a leading/trailing backtick in s is not merged into the fence.
	return fence + " " + s + " " + fence
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
