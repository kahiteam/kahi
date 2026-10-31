//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

// kahi migrate (FUNC-070, FUNC-071) run as the binary on supervisord.conf
// files.

// migrate writes conf to a file and runs kahi migrate on it.
func migrate(t *testing.T, conf string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisord.conf")
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return runKahi(t, dir, "migrate", path)
}

func wantLines(t *testing.T, what, out string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(out, l) {
			t.Errorf("%s lacks %q:\n%s", what, l, out)
		}
	}
}

// Every supervisord section type is recognised; the ones Kahi has no
// equivalent for are kept as comments with a warning.
func TestMigrate_SectionTypes(t *testing.T) {
	out, stderr, code := migrate(t, `[supervisord]
loglevel=debug

[unix_http_server]
file=/tmp/kahi-migrate-test.sock

[inet_http_server]
port=127.0.0.1:9001

[program:web]
command=/bin/sleep 300

[group:site]
programs=web

[eventlistener:watch]
command=/bin/cat
events=PROCESS_STATE

[fcgi-program:php]
command=/usr/bin/php-cgi
socket=tcp://127.0.0.1:9000

[include]
files=conf.d/*.conf

[rpcinterface:supervisor]
supervisor.rpcinterface_factory=x
`)
	// The inet server has no credentials, so the generated config fails
	// validation; the TOML is printed all the same.
	if code != 1 {
		t.Fatalf("exit %d, want 1 for the credential-less http server; stderr %q", code, stderr)
	}
	wantLines(t, "stdout", out,
		"[supervisor]\n", `log_level = "debug" # renamed from "loglevel"`,
		"[server.unix]\n", `file = "/tmp/kahi-migrate-test.sock"`,
		"[server.http]\n", `listen = "127.0.0.1:9001"`,
		"[programs.web]\n", `command = "/bin/sleep 300"`,
		"[groups.site]\n", `programs = ["web"]`,
		"# UNSUPPORTED SECTION: [eventlistener:watch]",
		"# UNSUPPORTED SECTION: [fcgi-program:php]",
		`# include = ["conf.d/*.conf"]`,
	)
	wantLines(t, "stderr", stderr,
		"warning: unsupported section type: eventlistener",
		"warning: unsupported section type: fcgi-program",
		"warning: unknown section type: rpcinterface",
		"http listener on 127.0.0.1:9001 requires username/password",
	)
	for _, typ := range []string{"supervisord", "program", "group", "include", "unix_http_server", "inet_http_server", "eventlistener", "fcgi-program"} {
		if strings.Contains(stderr, "unknown section type: "+typ+"\n") {
			t.Errorf("%s reported as unknown", typ)
		}
	}
}

// Values: comments, continuation lines, variables, booleans, numbers, sizes,
// signals, renamed and unsupported options.
func TestMigrate_Values(t *testing.T) {
	out, stderr, code := migrate(t, `[program:web]
; a whole-line comment
command=/bin/sh -c "echo a;echo b" ; inline comment
directory=%(ENV_HOME)s/app
startsecs=10 ; ten seconds
autostart=yes
autorestart=unexpected
redirect_stderr=on
stopasgroup=1
killasgroup=1
exitcodes=0,2
stopsignal=sigint
stdout_logfile_maxbytes=50MB
environment=A="1",
  B="two"
stdout_syslog=true
frobnicate=42

[program:other]
command=/bin/echo x;y
autostart=no
startretries=0
redirect_stderr=off
stopasgroup=false
killasgroup=0
`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	wantLines(t, "stdout", out,
		`command = "/bin/sh -c \"echo a;echo b\""`,
		`command = "/bin/echo x;y"`,
		`directory = "${HOME}/app"`,
		"startsecs = 10\n",
		"autostart = true\n", "autostart = false\n",
		`autorestart = "unexpected"`,
		"redirect_stderr = true\n", "stopasgroup = true\n", "killasgroup = true\n",
		"exitcodes = [0, 2]\n",
		"redirect_stderr = false\n", "stopasgroup = false\n", "killasgroup = false\n",
		`stopsignal = "SIGINT"`,
		`stdout_logfile_maxbytes = "50MB"`,
		`A = "1"`, `B = "two"`,
		"# UNSUPPORTED: stdout_syslog = true\n",
		"# UNSUPPORTED: frobnicate = 42 (unknown option)\n",
	)
	if strings.Contains(out, "comment") || strings.Contains(out, "ten seconds") {
		t.Errorf("a comment reached the output:\n%s", out)
	}
	wantLines(t, "stderr", stderr, `programs.web: unsupported option "stdout_syslog"`, `programs.web: unsupported option "frobnicate"`)

	// The output is a valid Kahi config with the same values.
	cfg, _, err := config.LoadBytes([]byte(out), "migrated.toml")
	if err != nil {
		t.Fatalf("migrated config does not load: %v", err)
	}
	web := cfg.Programs["web"]
	if web.Startsecs != 10 || web.Stopsignal != "SIGINT" || !web.RedirectStderr || web.Environment["B"] != "two" {
		t.Errorf("loaded web = %+v", web)
	}
}

// A malformed line is an error naming its line number.
func TestMigrate_ParseErrorLine(t *testing.T) {
	_, stderr, code := migrate(t, "[program:web]\ncommand=/bin/sleep 300\nthis is not a key value\n")
	if code != 1 || !strings.Contains(stderr, `parse error at line 3: expected key=value, got "this is not a key value"`) {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

// supervisord's template variables are kept where Kahi has them, so the
// migrated config runs: %(program_name)s and %(process_num)02d name the
// numprocs instances (the padding is dropped with a warning).
func TestMigrate_TemplateVariablesRun(t *testing.T) {
	dir := shortTempDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	conf := `[unix_http_server]
file=` + socket + `

[program:web]
command=/bin/sleep 300
process_name=%(program_name)s_%(process_num)02d
numprocs=2
startsecs=0
`
	if err := os.WriteFile(filepath.Join(dir, "supervisord.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "migrate", "-o", "kahi.toml", "supervisord.conf")
	if code != 0 {
		t.Fatalf("migrate: exit %d, stderr %q", code, stderr)
	}
	wantLines(t, "stderr", stderr, "%(process_num)02d: Kahi has no number padding; wrote %(process_num)d")

	cmd := startDaemonCmd(t, kahiBinary, filepath.Join(dir, "kahi.toml"), dir)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForSocket(t, socket, 5*time.Second)
	for _, name := range []string{"web_0", "web_1"} {
		out, _, code := runKahi(t, dir, "ctl", "-s", socket, "status", name)
		if code != 0 || !strings.Contains(out, name+" ") {
			t.Errorf("status %s: exit %d, %q", name, code, out)
		}
	}
}
