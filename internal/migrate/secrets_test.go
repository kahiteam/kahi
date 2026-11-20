package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The environment's value is written only into keys that become active Kahi
// settings. The HTTP password (which must be a bcrypt hash) and unsupported
// keys and sections keep their %(ENV_X)s reference, so a secret kept out of
// supervisord.conf never lands in the output (decided 2026-11-20).
func TestMigrateKeepsEnvReferenceInSecretsAndComments(t *testing.T) {
	t.Setenv("ZZ_MIG_SECRET", "s3cr3t-value")
	t.Setenv("ZZ_MIG_NAME", "admin")
	in := "[inet_http_server]\nport=127.0.0.1:9001\nusername=%(ENV_ZZ_MIG_NAME)s\npassword=%(ENV_ZZ_MIG_SECRET)s\n" +
		"[unix_http_server]\nfile=/tmp/k.sock\nchown=%(ENV_ZZ_MIG_SECRET)s\n" +
		"[supervisord]\nidentifier=%(ENV_ZZ_MIG_SECRET)s\n" +
		"[program:a]\ncommand=/bin/a\nserverurl=http://u:%(ENV_ZZ_MIG_SECRET)s@h\nfrobnicate=%(ENV_ZZ_MIG_SECRET)s\n" +
		"[eventlistener:l]\ncommand=/bin/l\nevents=PROCESS_STATE\nresult_handler=%(ENV_ZZ_MIG_SECRET)s\n" +
		"[rpcinterface:x]\ntoken=%(ENV_ZZ_MIG_SECRET)s\n"
	r, err := MigrateReader(strings.NewReader(in), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.TOML, "s3cr3t-value") {
		t.Fatalf("the environment's secret reached the output:\n%s", r.TOML)
	}
	for _, want := range []string{
		`password = "%(ENV_ZZ_MIG_SECRET)s"`, // secret-scan: allow (a placeholder, not a credential)
		`username = "admin"`,
		"UNSUPPORTED: chown = %(ENV_ZZ_MIG_SECRET)s",
		"UNSUPPORTED: identifier = %(ENV_ZZ_MIG_SECRET)s",
		"UNSUPPORTED: serverurl = http://u:%(ENV_ZZ_MIG_SECRET)s@h",
		"UNSUPPORTED: frobnicate = %(ENV_ZZ_MIG_SECRET)s (unknown option)",
		"UNSUPPORTED: result_handler = %(ENV_ZZ_MIG_SECRET)s",
		"token = %(ENV_ZZ_MIG_SECRET)s",
	} {
		if !strings.Contains(r.TOML, want) {
			t.Errorf("output lacks %s:\n%s", want, r.TOML)
		}
	}
	w := strings.Join(r.Warnings, "\n")
	if want := "inet_http_server.password: %(ENV_ZZ_MIG_SECRET)s left as is: the password must be a bcrypt hash; create one with kahi hash-password"; !strings.Contains(w, want) {
		t.Errorf("warnings lack %q:\n%s", want, w)
	}
	if strings.Contains(w, "replaced with the value of ZZ_MIG_SECRET") {
		t.Errorf("a kept reference is reported as replaced:\n%s", w)
	}
	if !strings.Contains(w, "inet_http_server.username: %(ENV_ZZ_MIG_NAME)s replaced with the value of ZZ_MIG_NAME") {
		t.Errorf("the username substitution is not reported:\n%s", w)
	}
}

func TestEnvCarriedOver(t *testing.T) {
	for _, tc := range []struct {
		typ, key string
		want     bool
	}{
		{"program", "stopsignal", true},
		{"program", "serverurl", false},
		{"program", "nosuchkey", false},
		{"eventlistener", "events", true},
		{"eventlistener", "result_handler", false},
		{"eventlistener", "stopsignal", true},
		{"supervisord", "user", true},
		{"supervisord", "identifier", false},
		{"group", "programs", true},
		{"unix_http_server", "chmod", true},
		{"unix_http_server", "chown", false},
		{"inet_http_server", "username", true},
		{"inet_http_server", "password", false},
		{"rpcinterface", "x", false},
	} {
		if got := envCarriedOver(tc.typ, tc.key); got != tc.want {
			t.Errorf("envCarriedOver(%s, %s) = %v, want %v", tc.typ, tc.key, got, tc.want)
		}
	}
}

// A new output file is created 0600 whatever the umask; --force keeps an
// existing file's mode; nothing at the path is followed.
func TestWriteResultFileModes(t *testing.T) {
	dir := t.TempDir()
	res := &Result{TOML: "x = 1\n"}

	out := filepath.Join(dir, "new.toml")
	if err := WriteResult(res, Options{Output: out}, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Errorf("new output mode %#o, want 0600", fi.Mode().Perm())
	}
	if err := WriteResult(res, Options{Output: out}, nil); err == nil || !strings.Contains(err.Error(), "output file exists: "+out+" (use --force)") {
		t.Errorf("second write without --force: %v", err)
	}

	kept := filepath.Join(dir, "kept.toml")
	if err := os.WriteFile(kept, []byte("old contents that are longer\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(kept, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteResult(res, Options{Output: kept, Force: true}, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(kept); fi.Mode().Perm() != 0o640 {
		t.Errorf("--force changed the mode to %#o, want 0640 kept", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(kept); string(b) != "x = 1\n" {
		t.Errorf("--force content %q", b)
	}

	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.toml")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteResult(res, Options{Output: link, Force: true}, nil); err == nil {
		t.Error("--force wrote through a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
		t.Errorf("symlink target = %q", b)
	}
}
