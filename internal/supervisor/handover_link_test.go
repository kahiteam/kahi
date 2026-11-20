package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/userspec"
)

// A hard link planted at a handed-over path is refused instead of having
// its target chowned; a regular file with one link is chowned through its
// descriptor, not by path.
func TestHandOverRefusesHardLinks(t *testing.T) {
	var fchowned, lchowned []string
	origF, origL := fchown, lchown
	t.Cleanup(func() { fchown, lchown = origF, origL })
	fchown = func(f *os.File, uid, gid int) error { fchowned = append(fchowned, f.Name()); return nil }
	lchown = func(path string, uid, gid int) error { lchowned = append(lchowned, path); return nil }

	dir := t.TempDir()
	target := filepath.Join(dir, "sudoers")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(dir, "kahi.pid")
	if err := os.Link(target, planted); err != nil {
		t.Fatal(err)
	}
	err := DropPrivileges(userspec.Credential{Uid: 1000, Gid: 1000}, []string{planted}, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "privilege drop failed: chown "+planted+": 2 hard links") {
		t.Errorf("hard link: %v", err)
	}
	if len(fchowned)+len(lchowned) != 0 {
		t.Errorf("chowned %v %v", fchowned, lchowned)
	}

	single := filepath.Join(dir, "kahi.log")
	if err := os.WriteFile(single, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := handOver(single, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(fchowned) != 1 || fchowned[0] != single || len(lchowned) != 0 {
		t.Errorf("single link: fchown %v, lchown %v", fchowned, lchowned)
	}
	if err := handOver(dir, 1000, 1000); err != nil || len(lchowned) != 1 {
		t.Errorf("directory: %v %v", err, lchowned)
	}
}
