package migrate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kahiteam/kahi/internal/events"
)

// supertypes expands supervisord's event supertypes to the exact Kahi
// types they cover (FUNC-055: Kahi takes exact names only). The order of
// PROCESS_STATE follows supervisord's state order.
var supertypes = map[string][]string{
	"PROCESS_STATE": {"PROCESS_STATE_STARTING", "PROCESS_STATE_RUNNING", "PROCESS_STATE_BACKOFF",
		"PROCESS_STATE_STOPPING", "PROCESS_STATE_EXITED", "PROCESS_STATE_STOPPED", "PROCESS_STATE_FATAL"},
	"PROCESS_LOG":                      {"PROCESS_LOG_STDOUT", "PROCESS_LOG_STDERR"},
	"SUPERVISOR_STATE_CHANGE":          {"SUPERVISOR_STATE_RUNNING", "SUPERVISOR_STATE_STOPPING"},
	"SUPERVISOR_STATE_CHANGE_RUNNING":  {"SUPERVISOR_STATE_RUNNING"},
	"SUPERVISOR_STATE_CHANGE_STOPPING": {"SUPERVISOR_STATE_STOPPING"},
	"TICK":                             {"TICK_5", "TICK_60", "TICK_3600"},
	"PROCESS_GROUP":                    {"PROCESS_GROUP_ADDED", "PROCESS_GROUP_REMOVED"},
}

// mapListenerEvents turns supervisord's events option into Kahi's exact
// event names, and lists the types Kahi has no equivalent for.
func mapListenerEvents(value string) (names, dropped []string) {
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, t := range strings.Split(value, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		switch {
		case t == "EVENT":
			for _, k := range events.AllEventTypes {
				add(string(k))
			}
		case supertypes[t] != nil:
			for _, k := range supertypes[t] {
				add(k)
			}
		case known(t):
			add(t)
		default:
			dropped = append(dropped, t)
		}
	}
	return names, dropped
}

func known(t string) bool {
	for _, k := range events.AllEventTypes {
		if string(k) == t {
			return true
		}
	}
	return false
}

// writeListenerSection maps [eventlistener:x] to [eventlisteners.x]: the
// program keys as for a program, events expanded to exact names, and the
// keys Kahi handles differently (FUNC-055, ADR-042).
func writeListenerSection(b *strings.Builder, sec INISection, result *Result) {
	writeProgramTable(b, sec, result, "eventlisteners", func(key, value string) bool {
		switch key {
		case "events":
			names, dropped := mapListenerEvents(value)
			quoted := make([]string, len(names))
			for i, n := range names {
				quoted[i] = strconv.Quote(n)
			}
			c := sec.Notes[key]
			writeComments(b, c.Above)
			line := fmt.Sprintf("events = [%s]", strings.Join(quoted, ", "))
			if c.Inline != "" {
				line += " # " + oneLine(c.Inline)
			}
			b.WriteString(line + "\n")
			for _, d := range dropped {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("eventlisteners.%s: event type %s has no kahi equivalent; dropped", sec.Name, d))
			}
		case "buffer_size":
			m := MapProgramOption("numprocs", value) // an int, like numprocs
			m.Key = "buffer_size"
			writeOption(b, m, sec.Notes[key])
		case "result_handler":
			writeOption(b, unsupported(key, value, "kahi parses RESULT itself: OK acknowledges, anything else re-queues"), sec.Notes[key])
			result.Warnings = append(result.Warnings, fmt.Sprintf("eventlisteners.%s: unsupported option %q", sec.Name, key))
		case "redirect_stderr":
			writeOption(b, unsupported(key, value, "not allowed for an event listener: stdout carries the protocol"), sec.Notes[key])
			result.Warnings = append(result.Warnings, fmt.Sprintf("eventlisteners.%s: unsupported option %q", sec.Name, key))
		default:
			return false
		}
		return true
	})
}

func unsupported(key, value, note string) MappedOption {
	return MappedOption{Key: key, Unsupported: true, Comment: fmt.Sprintf("UNSUPPORTED: %s = %s (%s)", key, value, note)}
}
