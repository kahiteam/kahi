package supervisor

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/userspec"
)

func stubResolver(t *testing.T) {
	t.Helper()
	orig := resolveDropUser
	t.Cleanup(func() { resolveDropUser = orig })
	resolveDropUser = func(spec string) (userspec.Credential, error) {
		switch spec {
		case "kahi":
			return userspec.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{4, 1000}}, nil
		case "app":
			return userspec.Credential{Uid: 1001, Gid: 1001, Groups: []uint32{1001}}, nil
		case "kahi:nosuch":
			return userspec.Credential{}, errors.New("group not found: nosuch")
		}
		return userspec.Credential{}, errors.New("user not found: " + spec)
	}
}

func asUID(t *testing.T, uid int) {
	t.Helper()
	orig := getuid
	getuid = func() int { return uid }
	t.Cleanup(func() { getuid = orig })
}

// FUNC-045: -u wins over [supervisor] user; the key alone drops as root and
// is ignored with a warning as non-root; an unknown user is an error.
func TestPlanDrop(t *testing.T) {
	stubResolver(t)
	asUID(t, 0)

	p, err := PlanDrop("", "")
	if err != nil || p.Drop {
		t.Errorf("no user: %+v, %v; want no drop", p, err)
	}
	p, err = PlanDrop("", "kahi")
	if err != nil || !p.Drop || p.Cred.Uid != 1000 || p.Override || p.Warning != "" {
		t.Errorf("key: %+v, %v; want a drop to 1000", p, err)
	}
	p, err = PlanDrop("app", "kahi")
	if err != nil || !p.Drop || p.Cred.Uid != 1001 || !p.Override {
		t.Errorf("-u and key: %+v, %v; want a drop to 1001 with Override", p, err)
	}
	if _, err := PlanDrop("", "nosuch"); err == nil || err.Error() != "user not found: nosuch" {
		t.Errorf("unknown key user: %v", err)
	}
	if _, err := PlanDrop("", "kahi:nosuch"); err == nil || err.Error() != "group not found: nosuch" {
		t.Errorf("unknown key group: %v", err)
	}

	asUID(t, 501)
	p, err = PlanDrop("", "nosuch")
	if err != nil || p.Drop || p.Warning != "supervisor.user ignored: dropping privileges needs root (running as uid 501)" {
		t.Errorf("non-root key: %+v, %v; want the ignored warning and no drop", p, err)
	}
	if _, err := PlanDrop("kahi", ""); err == nil || err.Error() != "kahi daemon -u kahi: dropping privileges needs root (running as uid 501)" {
		t.Errorf("non-root -u: %v", err)
	}
}

// A missing socket directory is created, one level only, owner-only, and
// reported as created so it is handed over with the socket (ADR-045).
func TestPrepareSocketDirCreatesOneLevel(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "kahi")
	created, err := PrepareSocketDir(filepath.Join(dir, "kahi.sock"))
	if err != nil || !created {
		t.Fatalf("PrepareSocketDir = %v, %v; want created", created, err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v, %v; want a 0700 directory", fi, err)
	}
	// An existing directory is not "created".
	if created, err := PrepareSocketDir(filepath.Join(dir, "kahi.sock")); err != nil || created {
		t.Errorf("second call = %v, %v; want existing", created, err)
	}
	// A missing grandparent is still an error.
	_, err = PrepareSocketDir(filepath.Join(parent, "a", "b", "kahi.sock"))
	if err == nil || !strings.Contains(err.Error(), "socket directory does not exist") {
		t.Errorf("missing grandparent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "a")); err == nil {
		t.Error("created more than one level")
	}
}

func stubLchown(t *testing.T) *[]string {
	t.Helper()
	orig := lchown
	t.Cleanup(func() { lchown = orig })
	var calls []string
	lchown = func(path string, uid, gid int) error {
		calls = append(calls, fmt.Sprintf("lchown %s %d %d", path, uid, gid))
		return nil
	}
	return &calls
}

// The files the daemon created as root go to the target user before the
// drop, so kahi ctl works as that user and shutdown can remove them.
func TestDropPrivilegesHandsOverFiles(t *testing.T) {
	calls := stubDrop(t)
	chowns := stubLchown(t)
	cred := userspec.Credential{Uid: 1000, Gid: 4, Groups: []uint32{4, 1000}}
	if err := DropPrivileges(cred, []string{"/run/kahi/kahi.sock", "/run/kahi", "", "/run/kahi.pid"}, testLogger()); err != nil {
		t.Fatal(err)
	}
	want := []string{"lchown /run/kahi/kahi.sock 1000 4", "lchown /run/kahi 1000 4", "lchown /run/kahi.pid 1000 4"}
	if !reflect.DeepEqual(*chowns, want) {
		t.Errorf("chowns = %v, want %v", *chowns, want)
	}
	if len(*calls) != 3 {
		t.Errorf("drop calls = %v", *calls)
	}
}

// A chown failure exits before any privilege is given up.
func TestDropPrivilegesChownFailure(t *testing.T) {
	calls := stubDrop(t)
	stubLchown(t)
	lchown = func(path string, uid, gid int) error { return errors.New("operation not permitted") }
	err := DropPrivileges(userspec.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{1000}}, []string{"/run/kahi.sock"}, testLogger())
	if err == nil || err.Error() != "privilege drop failed: chown /run/kahi.sock: operation not permitted" {
		t.Fatalf("error = %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("drop calls after a chown failure: %v", *calls)
	}
}

// At shutdown the PID file is removed and the socket is checked; what is
// left (a dropped daemon without write access to the directory) gets one
// warning.
func TestRemoveRuntimeFiles(t *testing.T) {
	dir := t.TempDir()
	pid, sock := filepath.Join(dir, "kahi.pid"), filepath.Join(dir, "kahi.sock")
	if err := os.WriteFile(pid, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	RemoveRuntimeFiles(pid, sock, slog.New(slog.NewTextHandler(&buf, nil)))
	if _, err := os.Stat(pid); err == nil {
		t.Error("PID file left")
	}
	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("warning with nothing left: %s", buf.String())
	}

	// A socket that is still there is reported.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	RemoveRuntimeFiles("", sock, slog.New(slog.NewTextHandler(&buf, nil)))
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 || !strings.Contains(buf.String(), "cannot remove at shutdown") || !strings.Contains(buf.String(), sock) {
		t.Errorf("want one warning naming the socket: %s", buf.String())
	}
}

func TestRootWarningNamesBoth(t *testing.T) {
	asUID(t, 0)
	var buf bytes.Buffer
	RootWarning(slog.New(slog.NewTextHandler(&buf, nil)), false)
	if want := "running as root without user config; set [supervisor] user or kahi daemon -u <user> for privilege dropping"; !strings.Contains(buf.String(), want) {
		t.Fatalf("log %q lacks %q", buf.String(), want)
	}
}
