package migrate

import (
	"strings"
	"testing"
)

// [eventlistener:x] becomes [eventlisteners.x]: program keys as for a
// program, supertypes expanded to exact names, the rest warned (ADR-042).
func TestMigrateEventListener(t *testing.T) {
	input := `[eventlistener:watch]
command = /usr/bin/watch-listener
events = PROCESS_STATE,TICK_60,PROCESS_COMMUNICATION_STDOUT,SUPERVISOR_STATE_CHANGE_RUNNING
buffer_size = 20
numprocs = 2
process_name = %(program_name)s_%(process_num)d
result_handler = supervisor.dispatchers:default_handler
redirect_stderr = true
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[eventlisteners.watch]\n",
		`events = ["PROCESS_STATE_STARTING", "PROCESS_STATE_RUNNING", "PROCESS_STATE_BACKOFF", "PROCESS_STATE_STOPPING", "PROCESS_STATE_EXITED", "PROCESS_STATE_STOPPED", "PROCESS_STATE_FATAL", "TICK_60", "SUPERVISOR_STATE_RUNNING"]`,
		"buffer_size = 20\n",
		"numprocs = 2\n",
		`command = "/usr/bin/watch-listener"`,
		"# UNSUPPORTED: result_handler = supervisor.dispatchers:default_handler (kahi parses RESULT itself",
		"# UNSUPPORTED: redirect_stderr = true (not allowed for an event listener: stdout carries the protocol)",
	} {
		if !strings.Contains(result.TOML, want) {
			t.Errorf("TOML lacks %q:\n%s", want, result.TOML)
		}
	}
	warnings := strings.Join(result.Warnings, "\n")
	if !strings.Contains(warnings, "eventlisteners.watch: event type PROCESS_COMMUNICATION_STDOUT has no kahi equivalent; dropped") {
		t.Errorf("no dropped-type warning:\n%s", warnings)
	}
	if strings.Contains(result.TOML, "UNSUPPORTED SECTION") || strings.Contains(warnings, "unsupported section type") {
		t.Errorf("eventlistener still treated as unsupported:\n%s\n%s", result.TOML, warnings)
	}
	if len(result.ValidErrs) != 0 {
		t.Errorf("generated TOML does not validate: %v", result.ValidErrs)
	}
}

func TestMapListenerEventsEventAndDuplicates(t *testing.T) {
	names, dropped := mapListenerEvents("EVENT, PROCESS_STATE_FATAL ,REMOTE_COMMUNICATION")
	if len(names) != 16 || len(dropped) != 1 || dropped[0] != "REMOTE_COMMUNICATION" {
		t.Errorf("names %v (%d), dropped %v", names, len(names), dropped)
	}
}
