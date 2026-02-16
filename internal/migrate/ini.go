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
		value = expandSupervisordVars(value)

		currentSection.Options[key] = value
		lastKey = key
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	return result, nil
}

// stripInlineComment removes inline comments (;) from a line,
// preserving semicolons inside quoted strings.
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
			if !inSingle && !inDouble {
				return line[:i]
			}
		}
	}
	return line
}

// expandSupervisordVars converts %(ENV_X)s to ${X} and %(here)s, etc.
var supervisordVarRe = regexp.MustCompile(`%\(ENV_([A-Za-z_][A-Za-z0-9_]*)\)s`)
var supervisordBuiltinVarRe = regexp.MustCompile(`%\(([a-z_]+)\)s`)

func expandSupervisordVars(value string) string {
	// First handle %(ENV_X)s -> ${X}
	value = supervisordVarRe.ReplaceAllString(value, "${$1}")
	// Then handle other builtins like %(here)s -> ${here}
	value = supervisordBuiltinVarRe.ReplaceAllString(value, "${$1}")
	return value
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
