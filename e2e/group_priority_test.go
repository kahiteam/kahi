//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A [groups] table's priority orders its programs ahead of their own
// priorities (#196): the group with priority 1 starts before a program with
// priority 5 and stops after it, its members ordered by their own priority.
func TestGroups_PriorityOrdersMembers(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events")
	var cfg strings.Builder
	for _, p := range []struct {
		name     string
		priority int
	}{{"solo", 5}, {"m1", 900}, {"m2", 800}} {
		script := writeScript(t, dir, p.name+".sh", fmt.Sprintf(
			"trap 'echo term:%[1]s >> %[2]s; sleep 0.3; echo exit:%[1]s >> %[2]s; exit 0' TERM\necho start:%[1]s >> %[2]s\nwhile true; do sleep 0.1; done",
			p.name, events))
		fmt.Fprintf(&cfg, "[programs.%s]\ncommand = %q\nautostart = false\nstartsecs = 0\npriority = %d\nstopwaitsecs = 5\n\n", p.name, script, p.priority)
	}
	cfg.WriteString("[groups.g]\nprograms = [\"m1\", \"m2\"]\npriority = 1\n")
	_, socket := startDaemon(t, cfg.String())

	if got := bulkNames(postBulk(t, socket, "start")); got != "m2,m1,solo" {
		t.Fatalf("start order %s, want m2,m1,solo", got)
	}
	waitForLines(t, events, 3, 5*time.Second)
	_ = os.Remove(events)

	if got := bulkNames(postBulk(t, socket, "stop")); got != "solo,m1,m2" {
		t.Fatalf("stop order %s, want solo,m1,m2", got)
	}
	got, _ := os.ReadFile(events)
	if want := "term:solo\nexit:solo\nterm:m1\nexit:m1\nterm:m2\nexit:m2\n"; string(got) != want {
		t.Fatalf("stop sequence:\n%s\nwant:\n%s", got, want)
	}
}
