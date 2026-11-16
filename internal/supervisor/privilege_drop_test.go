package supervisor

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/userspec"
)

// SEC-010: DropPrivileges replaces the supplementary groups with the target
// user's own list via setgroups before changing gid/uid, in the order
// setgroups -> setgid -> setuid, and aborts rather than continue with a
// partial drop.

func stubDrop(t *testing.T) *[]string {
	t.Helper()
	origGroups, origGid, origUid := setgroups, setgid, setuid
	t.Cleanup(func() { setgroups, setgid, setuid = origGroups, origGid, origUid })
	var calls []string
	setgroups = func(gids []int) error { calls = append(calls, fmt.Sprint("setgroups", gids)); return nil }
	setgid = func(g int) error { calls = append(calls, fmt.Sprint("setgid ", g)); return nil }
	setuid = func(u int) error { calls = append(calls, fmt.Sprint("setuid ", u)); return nil }
	return &calls
}

func TestDropPrivilegesResetsSupplementaryGroups(t *testing.T) {
	calls := stubDrop(t)
	cred := userspec.Credential{Uid: 1000, Gid: 4, Groups: []uint32{4, 1000}}
	if err := DropPrivileges(cred, nil, testLogger()); err != nil {
		t.Fatalf("DropPrivileges: %v", err)
	}
	want := []string{"setgroups[4 1000]", "setgid 4", "setuid 1000"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %v, want %v", *calls, want)
	}
}

// A credential without groups (never produced by the resolver) still never
// keeps the inherited list: the target gid alone.
func TestDropPrivilegesNoGroupsUsesGid(t *testing.T) {
	calls := stubDrop(t)
	if err := DropPrivileges(userspec.Credential{Uid: 65534, Gid: 65534}, nil, testLogger()); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0] != "setgroups[65534]" {
		t.Errorf("calls = %v, want setgroups [65534] first", *calls)
	}
}

func TestDropPrivilegesAbortsOnSetgroupsFailure(t *testing.T) {
	stubDrop(t)
	setgroups = func([]int) error { return fmt.Errorf("boom") }
	setgidCalled, setuidCalled := false, false
	setgid = func(int) error { setgidCalled = true; return nil }
	setuid = func(int) error { setuidCalled = true; return nil }

	err := DropPrivileges(userspec.Credential{Uid: 1000, Gid: 2000, Groups: []uint32{2000}}, nil, testLogger())
	if err == nil || err.Error() != "privilege drop failed: setgroups failed: boom" {
		t.Fatalf("error = %v, want privilege drop failed: setgroups failed: boom", err)
	}
	if setgidCalled || setuidCalled {
		t.Fatal("setgid/setuid must not run after setgroups fails (no partial drop)")
	}
}

func TestDropPrivilegesSetuidFailure(t *testing.T) {
	stubDrop(t)
	setuid = func(int) error { return errors.New("EPERM") }
	err := DropPrivileges(userspec.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{1000}}, nil, testLogger())
	if err == nil || !strings.HasPrefix(err.Error(), "privilege drop failed: setuid(1000) failed: EPERM") {
		t.Fatalf("error = %v", err)
	}
}

// -u is resolved and checked before anything is bound: an unknown user and
// a non-root daemon are refused with a clear message.
func TestCheckPrivilegeDrop(t *testing.T) {
	origUID, origResolve := getuid, resolveDropUser
	t.Cleanup(func() { getuid, resolveDropUser = origUID, origResolve })
	resolveDropUser = func(spec string) (userspec.Credential, error) {
		switch spec {
		case "kahi":
			return userspec.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{4, 1000}}, nil
		case "65534":
			return userspec.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{65534}}, nil
		case "kahi:nosuch":
			return userspec.Credential{}, errors.New("group not found: nosuch")
		}
		return userspec.Credential{}, errors.New("user not found: " + spec)
	}

	getuid = func() int { return 501 }
	if _, err := CheckPrivilegeDrop("65534"); err == nil || err.Error() != "kahi daemon -u 65534: dropping privileges needs root (running as uid 501)" {
		t.Errorf("non-root: %v", err)
	}

	getuid = func() int { return 0 }
	if _, err := CheckPrivilegeDrop("www-data"); err == nil || err.Error() != "user not found: www-data" {
		t.Errorf("unknown name: %v", err)
	}
	if _, err := CheckPrivilegeDrop("kahi:nosuch"); err == nil || err.Error() != "group not found: nosuch" {
		t.Errorf("unknown group: %v", err)
	}
	cred, err := CheckPrivilegeDrop("kahi")
	if err != nil || cred.Uid != 1000 || !reflect.DeepEqual(cred.Groups, []uint32{4, 1000}) {
		t.Errorf("root, name: %+v, %v", cred, err)
	}
}

// setuid and friends take int: an id that does not fit a 32-bit int is
// refused before any call, on every target.
func TestDropPrivilegesRejectsIDsAboveMaxInt32(t *testing.T) {
	for _, cred := range []userspec.Credential{
		{Uid: 1 << 31, Gid: 1000, Groups: []uint32{1000}},
		{Uid: 1000, Gid: 1 << 31, Groups: []uint32{1000}},
		{Uid: 1000, Gid: 1000, Groups: []uint32{1000, 4294967294}},
	} {
		calls := stubDrop(t)
		err := DropPrivileges(cred, nil, testLogger())
		if err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("%+v: error = %v, want out of range", cred, err)
		}
		if len(*calls) != 0 {
			t.Errorf("%+v: calls = %v, want none", cred, *calls)
		}
	}
}
