//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Signal names follow <signal.h> in every output; input accepts the name with
// or without the SIG prefix (ADR-038, #215).

// stopsignal "TERM" and "SIGTERM" both stop the program with SIGTERM.
func TestSignalNames_StopsignalBothForms(t *testing.T) {
	dir := t.TempDir()
	programs := ""
	for _, form := range []string{"TERM", "SIGTERM"} {
		name := "p" + strings.ToLower(form)
		script := writeScript(t, dir, name+".sh",
			"trap 'echo TERM > "+filepath.Join(dir, name+".got")+"; exit 0' TERM\n"+
				"echo ready > "+filepath.Join(dir, name+".ready")+"\nwhile true; do sleep 0.1; done")
		programs += "[programs." + name + "]\ncommand = \"" + script + "\"\nstartsecs = 0\nstopwaitsecs = 10\nstopsignal = \"" + form + "\"\n\n"
	}
	client, _ := startDaemon(t, programs)
	for _, name := range []string{"pterm", "psigterm"} {
		waitForState(t, client, name, "RUNNING", 5*time.Second)
		waitForMarker(t, filepath.Join(dir, name+".ready"), "ready", 5*time.Second)
		if err := client.Stop(name); err != nil {
			t.Fatalf("stop %s: %v", name, err)
		}
		waitForState(t, client, name, "STOPPED", 5*time.Second)
		waitForMarker(t, filepath.Join(dir, name+".got"), "TERM", time.Second)
	}
}

// An unknown stopsignal, and STOP (allowed for the signal API only), are
// config errors.
func TestSignalNames_StopsignalRefused(t *testing.T) {
	for _, value := range []string{"BOGUS", "STOP"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "kahi.toml")
			body := "[server.unix]\nfile = \"" + filepath.Join(shortTempDir(t), "k.sock") + "\"\n\n" +
				"[programs.web]\ncommand = \"/bin/sleep 300\"\nstopsignal = \"" + value + "\"\n"
			if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
			want := `programs.web: invalid stopsignal "` + value + `"`
			if code != 1 || !strings.Contains(stderr, want) {
				t.Errorf("exit %d, stderr %q; want 1 and %q", code, stderr, want)
			}
		})
	}
}

// The signal API and kahi ctl signal accept both forms and answer with the
// <signal.h> name; STOP and CONT stay available there.
func TestSignalNames_SignalAPI(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "got")
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "hup.sh",
		"trap 'echo HUP >> "+marker+"' HUP\necho ready > "+ready+"\nwhile true; do sleep 0.1; done")
	client, socket := startDaemon(t, `
[programs.web]
command = "`+script+`"
startsecs = 0
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForMarker(t, ready, "ready", 5*time.Second)

	// sh runs a trap once for signals that arrive together, so each signal
	// is sent only after the previous one reached the child.
	sent := 0
	waitHUPs := func() {
		t.Helper()
		sent++
		deadline := time.Now().Add(5 * time.Second)
		for {
			b, _ := os.ReadFile(marker)
			if strings.Count(string(b), "HUP") == sent {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("marker %q, want %d HUP lines", b, sent)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	for _, input := range []string{"HUP", "SIGHUP", "sighup"} {
		var got map[string]string
		apiDo(t, socket, "POST", "/api/v1/processes/web/signal", `{"signal":"`+input+`"}`).
			jsonBody(t, "signal "+input, http.StatusOK, &got)
		if got["signal"] != "SIGHUP" {
			t.Errorf("signal %s: response %v, want signal SIGHUP", input, got)
		}
		waitHUPs()
	}
	for _, input := range []string{"HUP", "SIGHUP"} {
		out, stderr, code := runKahi(t, dir, "ctl", "-s", socket, "signal", input, "web")
		if code != 0 || strings.TrimSpace(out) != "web: signaled SIGHUP" {
			t.Errorf("ctl signal %s: exit %d, out %q, stderr %q; want 'web: signaled SIGHUP'", input, code, out, stderr)
		}
		waitHUPs()
	}

	apiDo(t, socket, "POST", "/api/v1/processes/web/signal", `{"signal":"BOGUS"}`).
		wantError(t, "signal BOGUS", http.StatusBadRequest, `process web: invalid signal "BOGUS"`, "BAD_REQUEST")

	for _, input := range []string{"STOP", "CONT"} {
		var got map[string]string
		apiDo(t, socket, "POST", "/api/v1/processes/web/signal", `{"signal":"`+input+`"}`).
			jsonBody(t, "signal "+input, http.StatusOK, &got)
		if got["signal"] != "SIG"+input {
			t.Errorf("signal %s: response %v, want SIG%s", input, got, input)
		}
	}
}

// The default stopsignal and the kahi init template use the SIG form.
func TestSignalNames_Defaults(t *testing.T) {
	_, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
`)
	resp := apiDo(t, socket, "GET", "/api/v1/config", "")
	if resp.status != http.StatusOK || !strings.Contains(string(resp.body), `"SIGTERM"`) {
		t.Errorf("GET /api/v1/config: status %d, body %s; want the default stopsignal SIGTERM", resp.status, resp.body)
	}

	out, _, code := runKahi(t, t.TempDir(), "init", "--stdout")
	if code != 0 || !strings.Contains(out, `# stopsignal = "SIGTERM"`) ||
		!strings.Contains(out, "(SIGTERM, SIGHUP, SIGINT, SIGQUIT, SIGKILL, SIGUSR1, SIGUSR2)") {
		t.Errorf("kahi init --stdout: exit %d, want the SIG form in the stopsignal comment:\n%s", code, out)
	}
}

// The daemon logs received signals by their <signal.h> name.
func TestSignalNames_ReceivedSignalLog(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "kahi.sock")
	logPath := filepath.Join(dir, "kahi.log")
	configPath := filepath.Join(dir, "kahi.toml")
	cfg := "[supervisor]\nlogfile = \"" + logPath + "\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = \"" + socketPath + "\"\n"
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := startDaemonCmd(t, kahiBinary, configPath, dir)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForSocket(t, socketPath, 5*time.Second)
	waitForHealth(t, ctl.NewUnixClient(socketPath), 3*time.Second)

	if err := syscall.Kill(cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon still running 10s after SIGTERM")
	}
	b, _ := os.ReadFile(logPath)
	if !strings.Contains(string(b), `"msg":"received signal","signal":"SIGTERM"`) {
		t.Errorf("daemon log lacks the received signal as SIGTERM:\n%s", b)
	}
}

// The README and the sample config show the SIG form.
func TestSignalNames_Docs(t *testing.T) {
	for _, path := range []string{"../README.md", "../cmd/kahi/kahi.toml"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `# stopsignal = "SIGTERM"`) || strings.Contains(string(b), `stopsignal = "TERM"`) {
			t.Errorf("%s: want stopsignal shown as \"SIGTERM\"", path)
		}
	}
}

// kahi migrate writes stopsignal in the SIG form and its stopwaitsecs = 0
// warning names SIGKILL.
func TestSignalNames_Migrate(t *testing.T) {
	out, stderr, code := migrate(t, `[program:a]
command = /bin/sleep 300
stopsignal = TERM

[program:b]
command = /bin/sleep 300
stopsignal = SIGINT
stopwaitsecs = 0
`)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{`stopsignal = "SIGTERM"`, `stopsignal = "SIGINT"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr, "use stopsignal = SIGKILL for an immediate kill") {
		t.Errorf("stderr %q, want the stopwaitsecs warning to name SIGKILL", stderr)
	}
}
