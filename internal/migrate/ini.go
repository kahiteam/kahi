package migrate

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// INISection represents a parsed section from a supervisord.conf file.
type INISection struct {
	Type    string            // e.g. "supervisord", "program", "group", etc.
	Name    string            // e.g. program name for [program:web]
	Options map[string]string // key-value pairs
	Comment string            // the inline comment on the section header line

	// Comments kept for the output (FUNC-069, ADR-053): the full-line
	// comments above the header, those after the last key (end of section
	// or file), and per key its comments above and its inline comment.
	Above    []string
	Trailing []string
	Notes    map[string]OptionComments
}

// OptionComments are the comments attached to one key.
type OptionComments struct {
	Above  []string // full-line comments above the key
	Inline string   // the comment at the end of the key's line
}

// INIFile represents a fully parsed supervisord.conf.
type INIFile struct {
	Sections []INISection
	Warnings []string
}

// known section types in supervisord.conf
var knownSectionTypes = map[string]bool{
	"supervisord":      true,
	"program":          true,
	"group":            true,
	"eventlistener":    true,
	"fcgi-program":     true,
	"include":          true,
	"unix_http_server": true,
	"inet_http_server": true,
}

// sectionHeaderRe matches [section_type] or [section_type:name]
var sectionHeaderRe = regexp.MustCompile(`^\[([a-zA-Z_-]+)(?::([^\]]+))?\]\s*(?:;.*)?$`)

// ParseINI parses a supervisord.conf INI file from a reader. Comments are
// kept with the item they belong to (FUNC-069, ADR-053): a block of
// full-line comments attaches to the next key or section header; a block
// that a blank line separates from the next section header, or that ends
// the file, is the previous section's end-of-section comment. A comment on a
// continuation line is dropped with a warning.
func ParseINI(r io.Reader) (*INIFile, error) {
	result := &INIFile{}
	scanner := bufio.NewScanner(r)
	lineNum := 0

	var currentSection *INISection
	var lastKey string
	var pending []string // full-line comments not attached yet
	gap := false         // a blank line follows the pending comments
	split := 0           // pending[split:] is the last block (after a blank line)
	addComment := func(text string) {
		if gap {
			split = len(pending)
		}
		pending = append(pending, text)
		gap = false
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		code, comment := splitInlineComment(line)
		trimmed := strings.TrimSpace(code)

		if trimmed == "" {
			if text := commentText(line); text != "" || comment != "" {
				// A full-line comment (; or #, indented or not).
				if text == "" {
					text = comment
				}
				addComment(text)
			} else if len(pending) > 0 {
				gap = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			addComment(commentText(line))
			continue
		}

		// Check for continuation line (starts with whitespace).
		if (line[0] == ' ' || line[0] == '\t') && currentSection != nil && lastKey != "" {
			currentSection.Options[lastKey] += " " + trimmed
			if comment != "" {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("comment on continuation line %d dropped", lineNum))
			}
			continue
		}

		// Check for section header.
		if matches := sectionHeaderRe.FindStringSubmatch(trimmed); matches != nil {
			sectionType := matches[1]
			sectionName := matches[2]

			if !knownSectionTypes[sectionType] {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("unknown section type: %s", sectionType))
			}

			// Blocks a blank line separates from this header end the
			// previous section; the block right above it is its own.
			if currentSection != nil && len(pending) > 0 {
				end := split
				if gap {
					end = len(pending)
				}
				currentSection.Trailing = append(currentSection.Trailing, pending[:end]...)
				pending = pending[end:]
			}
			section := INISection{
				Type:    sectionType,
				Name:    sectionName,
				Options: make(map[string]string),
				Notes:   make(map[string]OptionComments),
				Comment: comment,
				Above:   pending,
			}
			pending, gap, split = nil, false, 0
			result.Sections = append(result.Sections, section)
			currentSection = &result.Sections[len(result.Sections)-1]
			lastKey = ""
			continue
		}

		// Parse key=value pair.
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			return nil, fmt.Errorf("parse error at line %d: expected key=value, got %q", lineNum, trimmed)
		}

		if currentSection == nil {
			return nil, fmt.Errorf("parse error at line %d: key-value pair outside of any section", lineNum)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// supervisord takes $ literally; where Kahi expands variables a
		// literal $ is written $$, before %(ENV_X)s becomes ${X}. Where
		// Kahi does not expand, ${X} would stay literal, so %(ENV_X)s is
		// replaced with its value here, with a warning (#302).
		var envWarnings []string
		if expandedKeys[currentSection.Type][key] {
			value = strings.ReplaceAll(value, "$", "$$")
		} else {
			value, envWarnings = substituteEnv(value, sectionLabel(currentSection), key)
		}
		// Expand %(ENV_X)s to ${X} syntax.
		value, varWarnings := expandSupervisordVars(value)
		value = strings.ReplaceAll(value, envKeep, "")
		varWarnings = append(envWarnings, varWarnings...)
		for _, w := range varWarnings {
			result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: %s", lineNum, w))
		}

		currentSection.Options[key] = value
		currentSection.Notes[key] = OptionComments{Above: pending, Inline: comment}
		pending, gap, split = nil, false, 0
		lastKey = key
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}
	if len(pending) > 0 && currentSection != nil {
		currentSection.Trailing = append(currentSection.Trailing, pending...)
	}

	return result, nil
}

// commentText returns the text of a full-line comment (";" or "#" first
// after any indentation), or "".
func commentText(line string) string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, ";") && !strings.HasPrefix(t, "#") {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(t, ";#"))
}

// splitInlineComment splits a line at its inline comment, by the rule
// stripInlineComment uses, and returns the code and the comment text.
func splitInlineComment(line string) (code, comment string) {
	code = stripInlineComment(line)
	if len(code) == len(line) {
		return line, ""
	}
	return code, strings.TrimSpace(strings.TrimLeft(line[len(code):], ";"))
}

// stripInlineComment removes an inline comment from a line. As in
// supervisord, a comment starts at a ; at the start of the line or after
// whitespace, so "a;b" keeps its semicolon (#188); semicolons inside quotes
// are kept too.
func stripInlineComment(line string) string {
	inSingle := false
	inDouble := false
	for i, ch := range line {
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';':
			if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				return line[:i]
			}
		}
	}
	return line
}

var supervisordEnvVarRe = regexp.MustCompile(`%\(ENV_([A-Za-z_][A-Za-z0-9_]*)\)s`)
var supervisordVarRe = regexp.MustCompile(`%\(([a-z_]+)\)(\d*)([sd])`)

// kahiVars are the supervisord template variables Kahi expands itself, with
// the same %(name)s / %(name)d syntax.
var kahiVars = map[string]bool{
	"here": true, "program_name": true, "group_name": true, "process_num": true, "numprocs": true,
}

// substituteEnv replaces %(ENV_X)s in a value Kahi does not expand with
// X's value in this environment. An unset X leaves the reference as it is,
// escaped so it is not turned into ${X}. Each replacement is reported.
func substituteEnv(value, section, key string) (string, []string) {
	var warnings []string
	value = supervisordEnvVarRe.ReplaceAllStringFunc(value, func(m string) string {
		name := supervisordEnvVarRe.FindStringSubmatch(m)[1]
		if v, ok := os.LookupEnv(name); ok {
			warnings = append(warnings, fmt.Sprintf("%s.%s: %s replaced with the value of %s in this environment (Kahi does not expand %s)", section, key, m, name, key))
			return v
		}
		warnings = append(warnings, fmt.Sprintf("%s.%s: %s left as is: %s is not set here and Kahi does not expand %s", section, key, m, name, key))
		return "%(ENV_" + name + ")" + envKeep + "s" // kept out of the ${X} conversion
	})
	return value, warnings
}

// envKeep marks an unset %(ENV_X)s that substituteEnv keeps, so the ${X}
// conversion does not match it; it is removed right after.
const envKeep = "\x00"

// sectionLabel names a section for warnings as supervisord writes it.
func sectionLabel(sec *INISection) string {
	if sec.Name != "" {
		return sec.Type + ":" + sec.Name
	}
	return sec.Type
}

// expandSupervisordVars converts %(ENV_X)s to Kahi's ${X} and keeps the
// template variables Kahi shares. Kahi has no number padding, so
// %(process_num)02d becomes %(process_num)d; a variable Kahi does not know is
// left in place. Both are reported (#188).
func expandSupervisordVars(value string) (string, []string) {
	value = supervisordEnvVarRe.ReplaceAllString(value, "${$1}")
	var warnings []string
	value = supervisordVarRe.ReplaceAllStringFunc(value, func(m string) string {
		sub := supervisordVarRe.FindStringSubmatch(m)
		name, width, verb := sub[1], sub[2], sub[3]
		switch {
		case !kahiVars[name]:
			warnings = append(warnings, fmt.Sprintf("%s is not a Kahi variable; left as is", m))
		case width != "":
			short := "%(" + name + ")" + verb
			warnings = append(warnings, fmt.Sprintf("%s: Kahi has no number padding; wrote %s", m, short))
			return short
		}
		return m
	})
	return value, warnings
}

// ParseBool converts supervisord boolean values to Go booleans.
// Supports: true/false, yes/no, on/off, 1/0.
func ParseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return true, nil
	case "false", "no", "off", "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value: %q", value)
	}
}
