//go:build unix

package logging

import (
	"os"
	"path/filepath"
	"testing"
)

// ProbeDaemonLog fails exactly where OpenDaemonLog fails, with the same text,
// and creates nothing (FUNC-087, ADR-054).
func TestProbeDaemonLog_MatchesOpenDaemonLog(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, "absparent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "x.log"), filepath.Join(base, "final.log")); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(base, "existing.log")
	if err := os.WriteFile(existing, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"missingParent": filepath.Join(base, "nope", "k.log"),
		"absSymlinkDir": filepath.Join(base, "absparent", "k.log"),
		"finalSymlink":  filepath.Join(base, "final.log"),
		"directory":     target,
		"new":           filepath.Join(base, "new.log"),
		"existing":      existing,
		"devnull":       "/dev/null",
	}
	if os.Geteuid() != 0 {
		ro := filepath.Join(base, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		cases["unwritableDir"] = filepath.Join(ro, "k.log")
		roFile := filepath.Join(base, "ro.log")
		if err := os.WriteFile(roFile, nil, 0o444); err != nil {
			t.Fatal(err)
		}
		cases["unwritableFile"] = roFile
	}
	for name, path := range cases {
		probe := ProbeDaemonLog(path)
		if name == "new" {
			if _, err := os.Lstat(path); err == nil {
				t.Fatalf("probe created %s", path)
			}
		}
		dl, step := OpenDaemonLog("info", "text", path)
		if dl != nil {
			_ = dl.Close()
		}
		if (probe == nil) != (step == nil) || (probe != nil && probe.Error() != step.Error()) {
			t.Errorf("%s: probe %v, step %v", name, probe, step)
		}
	}
	if err := ProbeDaemonLog(""); err != nil {
		t.Errorf("empty logfile: %v", err)
	}
}
