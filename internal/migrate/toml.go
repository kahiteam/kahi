package migrate

import (
	"fmt"
	"regexp"
	"strings"
)

// tomlString writes s as a TOML basic string. Go's %q is not TOML: it can
// emit \a, \v and \xNN, which TOML rejects. Invalid UTF-8 becomes U+FFFD.
func tomlString(s string) string {
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

var bareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tomlKey writes a table or key name: bare when TOML allows it, quoted
// otherwise. A name with a dot or a space written bare would nest tables or
// not parse, so [program:api.worker] became a sub-table of api.
func tomlKey(name string) string {
	if bareKeyRe.MatchString(name) {
		return name
	}
	return tomlString(name)
}

// expandedKeys are the keys whose values Kahi expands (%(var)s, ${VAR},
// with $$ and %% as escapes), per supervisord section type.
var expandedKeys = map[string]map[string]bool{
	"supervisord":      {"logfile": true, "directory": true},
	"unix_http_server": {"file": true},
	"program": {"command": true, "directory": true, "environment": true, "process_name": true,
		"stdout_logfile": true, "stderr_logfile": true, "user": true},
	"eventlistener": {"command": true, "directory": true, "environment": true, "process_name": true,
		"stdout_logfile": true, "stderr_logfile": true, "user": true},
}

// splitEnvironment parses supervisord's environment=KEY="val",KEY2=val2 the
// way its shlex-based parser does: commas and = inside quotes belong to
// the value, and the quotes are removed.
func splitEnvironment(value string) map[string]string {
	result := make(map[string]string)
	var key, cur strings.Builder
	var quote rune
	inValue := false
	flush := func() {
		k := strings.TrimSpace(key.String())
		if inValue && k != "" {
			result[k] = strings.TrimSpace(cur.String())
		}
		key.Reset()
		cur.Reset()
		inValue = false
	}
	for _, r := range value {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if inValue {
				cur.WriteRune(r)
			} else {
				key.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ',':
			flush()
		case r == '=' && !inValue:
			inValue = true
		case inValue:
			cur.WriteRune(r)
		default:
			key.WriteRune(r)
		}
	}
	flush()
	return result
}
