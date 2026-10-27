package migrate

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// INISection represents a parsed section from a supervisord.conf file.
type INISection struct {
	Type    string            // e.g. "supervisord", "program", "group", etc.
	Name    string            // e.g. program name for [program:web]
	Options map[string]string // key-value pairs
	Comment string            // any comment associated with the section header
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

// ParseINI parses a supervisord.conf INI file from a reader.
func ParseINI(r io.Reader) (*INIFile, error) {
	result := &INIFile{}
	scanner := bufio.NewScanner(r)
	lineNum := 0

	var currentSection *INISection
	var lastKey string

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Strip inline comments (semicolons not inside quotes).
		stripped := stripInlineComment(line)
		trimmed := strings.TrimSpace(stripped)

		// Skip empty lines and pure comment lines.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}

		// Check for continuation line (starts with whitespace).
		if (line[0] == ' ' || line[0] == '\t') && currentSection != nil && lastKey != "" {
			currentSection.Options[lastKey] += " " + trimmed
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

			section := INISection{
				Type:    sectionType,
				Name:    sectionName,
				Options: make(map[string]string),
			}
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

		// Expand %(ENV_X)s to ${X} syntax.
		value, varWarnings := expandSupervisordVars(value)
		for _, w := range varWarnings {
			result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: %s", lineNum, w))
		}

		currentSection.Options[key] = value
		lastKey = key
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	return result, nil
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
