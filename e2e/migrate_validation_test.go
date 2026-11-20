//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// kahi migrate validates what it generates the way the daemon will load it
// (FUNC-073, #245): a config it accepts starts; one it rejects is still
// written, so it can be fixed by hand.

// A valid supervisord config migrates with exit 0 and "wrote", and the
// daemon starts on the result.
func TestMigrateValidation_ValidConfigRuns(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	in := "[unix_http_server]\nfile=" + socket + "\n\n[program:web]\ncommand=/bin/sleep 300\nstartsecs=0\nstopwaitsecs=2\n"
	if err := os.WriteFile(filepath.Join(dir, "in.conf"), []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runKahi(t, dir, "migrate", "in.conf", "-o", "out.toml")
	if code != 0 || out != "wrote out.toml\n" || errOut != "" {
		t.Fatalf("migrate: exit %d, stdout %q, stderr %q; want exit 0 and \"wrote out.toml\"", code, out, errOut)
	}

	cmd := startDaemonCmd(t, kahiBinary, filepath.Join(dir, "out.toml"), dir)
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	waitForState(t, ctl.NewUnixClient(socket), "web", "RUNNING", 5*time.Second)
}

// A generated config that fails validation exits 1 with the problems on
// stderr, also those only the daemon's load used to find (#245), and the
// file is still written.
func TestMigrateValidation_FailureReported(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ in, problem string }{
		"process_name without process_num": {
			"[program:x]\ncommand=/bin/sleep 300\nnumprocs=2\nprocess_name=web\n",
			`programs.x: process_name "web" must contain %(process_num)d when numprocs > 1`,
		},
		"missing command": {
			"[program:x]\nautostart=true\n",
			"programs.x: command is required",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "in.conf"), []byte(tc.in), 0o644); err != nil {
				t.Fatal(err)
			}
			out, errOut, code := runKahi(t, dir, "migrate", "in.conf", "-o", "out.toml")
			want := "validation: generated config has validation errors: config validation failed in generated:\n  " +
				tc.problem + "\ngenerated config failed validation\n"
			if code != 1 || out != "wrote out.toml\n" || errOut != want {
				t.Fatalf("migrate: exit %d, stdout %q, stderr %q; want exit 1, \"wrote out.toml\" and stderr %q", code, out, errOut, want)
			}
			b, err := os.ReadFile(filepath.Join(dir, "out.toml"))
			if err != nil || !strings.Contains(string(b), "[programs.x]") {
				t.Fatalf("out.toml not written for hand-fixing: %v %q", err, b)
			}
		})
	}
}

// A secret kept out of supervisord.conf with %(ENV_X)s is never written into
// the migrated file: the HTTP password and unsupported keys keep the
// reference, and the new file is 0600 (decided 2026-11-20).
func TestMigrate_EnvSecretNotWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := "[inet_http_server]\nport=127.0.0.1:9001\nusername=admin\npassword=%(ENV_ZZ_E2E_SECRET)s\n\n" +
		"[program:web]\ncommand=/bin/sleep 300\nserverurl=http://u:%(ENV_ZZ_E2E_SECRET)s@h\n"
	if err := os.WriteFile(filepath.Join(dir, "in.conf"), []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "ZZ_E2E_SECRET=e2e-s3cr3t")
	_, stderr, _ := runKahiEnv(t, dir, env, "migrate", "in.conf", "-o", "out.toml")
	b, err := os.ReadFile(filepath.Join(dir, "out.toml"))
	if err != nil {
		t.Fatalf("no output written: %v; stderr %q", err, stderr)
	}
	if strings.Contains(string(b), "e2e-s3cr3t") {
		t.Fatalf("the secret reached the output:\n%s", b)
	}
	for _, want := range []string{`password = "%(ENV_ZZ_E2E_SECRET)s"`, "# UNSUPPORTED: serverurl = http://u:%(ENV_ZZ_E2E_SECRET)s@h"} { // secret-scan: allow (a placeholder, not a credential)
		if !strings.Contains(string(b), want) {
			t.Errorf("output lacks %s:\n%s", want, b)
		}
	}
	if want := "warning: line 4: inet_http_server.password: %(ENV_ZZ_E2E_SECRET)s left as is: the password must be a bcrypt hash; create one with kahi hash-password"; !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
	if fi, err := os.Stat(filepath.Join(dir, "out.toml")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("output %v %v; want mode 0600", fi, err)
	}
}
