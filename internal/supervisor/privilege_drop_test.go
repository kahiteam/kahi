package supervisor

import (
	"fmt"
	"reflect"
	"testing"
)

// SEC-010: DropPrivileges must reset supplementary groups via setgroups before
// changing gid/uid, in the order setgroups -> setgid -> setuid, and must abort
// rather than continue with a partial drop if setgroups fails.

func TestDropPrivilegesResetsSupplementaryGroups(t *testing.T) {
	origGroups, origGid, origUid := setgroups, setgid, setuid
	defer func() { setgroups, setgid, setuid = origGroups, origGid, origUid }()

	var calls []string
	var gotGroups []int
	setgroups = func(gids []int) error { calls = append(calls, "setgroups"); gotGroups = gids; return nil }
	setgid = func(int) error { calls = append(calls, "setgid"); return nil }
	setuid = func(int) error { calls = append(calls, "setuid"); return nil }

	if err := DropPrivileges("1000:2000", testLogger()); err != nil {
		t.Fatalf("DropPrivileges: %v", err)
	}

	want := []string{"setgroups", "setgid", "setuid"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("call order = %v, want %v", calls, want)
	}
	if !reflect.DeepEqual(gotGroups, []int{2000}) {
		t.Fatalf("setgroups arg = %v, want [2000]", gotGroups)
	}
}

func TestDropPrivilegesAbortsOnSetgroupsFailure(t *testing.T) {
	origGroups, origGid, origUid := setgroups, setgid, setuid
	defer func() { setgroups, setgid, setuid = origGroups, origGid, origUid }()

	setgroups = func([]int) error { return fmt.Errorf("boom") }
	setgidCalled, setuidCalled := false, false
	setgid = func(int) error { setgidCalled = true; return nil }
	setuid = func(int) error { setuidCalled = true; return nil }

	if err := DropPrivileges("1000:2000", testLogger()); err == nil {
		t.Fatal("expected error when setgroups fails")
	}
	if setgidCalled || setuidCalled {
		t.Fatal("setgid/setuid must not run after setgroups fails (no partial drop)")
	}
}

// -u is checked before anything is bound: a non-numeric user and a non-root
// daemon are refused with a clear message (#159 adds names).
func TestCheckPrivilegeDrop(t *testing.T) {
	original := getuid
	defer func() { getuid = original }()

	getuid = func() int { return 501 }
	if err := CheckPrivilegeDrop("65534"); err == nil || err.Error() != "kahi daemon -u 65534: dropping privileges needs root (running as uid 501)" {
		t.Errorf("non-root: %v", err)
	}
	if err := CheckPrivilegeDrop("www-data"); err == nil || err.Error() != `cannot resolve user "www-data": invalid uid: www-data` {
		t.Errorf("name: %v", err)
	}

	getuid = func() int { return 0 }
	if err := CheckPrivilegeDrop("65534:65534"); err != nil {
		t.Errorf("root, numeric user: %v", err)
	}
}
