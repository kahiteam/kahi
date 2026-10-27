//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/testutil"
)

// TCP listener (FUNC-025), fail-closed auth (SEC-015) and bcrypt-only
// passwords (SEC-018), against the real binary.

// expectRefused runs the daemon with configTOML and checks it exits non-zero
// with every want on stderr.
func expectRefused(t *testing.T, configTOML string, want ...string) string {
	t.Helper()
	stderr, err := runDaemonExpectFailure(t, configTOML)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("daemon err = %v, want a non-zero exit; stderr:\n%s", err, stderr)
	}
	for _, w := range want {
		if !strings.Contains(stderr, w) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, w)
		}
	}
	return stderr
}

// With credentials the listener serves the API on the configured address:
// valid Basic auth is processed, a missing or wrong one is 401 with a JSON
// body and a Basic challenge. The Unix socket needs no credentials.
func TestTCP_ServesAPIWithCreds(t *testing.T) {
	d := startWebUIDaemon(t, false)

	resp, body := webRequest(t, webClient, http.MethodGet, d.base+"/api/v1/processes", true)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"sleeper"`) {
		t.Fatalf("with credentials: %d %s, want 200 listing sleeper", resp.StatusCode, body)
	}

	for _, tc := range []struct {
		name, user, pass, want string
	}{
		{"no credentials", "", "", "authentication required"},
		{"wrong password", "admin", "wrong", "invalid credentials"},
		{"wrong user", "root", "secret", "invalid credentials"},
	} {
		req, err := http.NewRequest(http.MethodGet, d.base+"/api/v1/processes", nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.user != "" {
			req.SetBasicAuth(tc.user, tc.pass)
		}
		resp, err := webClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var e struct{ Error, Code string }
		if resp.StatusCode != http.StatusUnauthorized || json.Unmarshal(b, &e) != nil || e.Error != tc.want || e.Code != "UNAUTHORIZED" {
			t.Errorf("%s: %d %s, want 401 with error %q and code UNAUTHORIZED", tc.name, resp.StatusCode, b, tc.want)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != `Basic realm="kahi"` {
			t.Errorf("%s: WWW-Authenticate %q", tc.name, got)
		}
		if got := resp.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: Content-Type %q", tc.name, got)
		}
	}

	// The CLI defaults to the Unix socket and needs no credentials there,
	// with the TCP listener enabled.
	out, stderr, code := runKahi(t, t.TempDir(), "ctl", "-s", d.socket, "status", "sleeper")
	if code != 0 || !strings.Contains(out, "RUNNING") {
		t.Fatalf("ctl status over the socket: code %d, stdout %q, stderr %q", code, out, stderr)
	}
}

// Without credentials the daemon refuses to start, on loopback as on all
// interfaces; an all-interfaces address is also warned about.
func TestTCP_RefusedWithoutCredentials(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:9876", "0.0.0.0:9876"} {
		t.Run(listen, func(t *testing.T) {
			stderr := expectRefused(t, fmt.Sprintf("[server.http]\nenabled = true\nlisten = %q\n", listen),
				"http listener on "+listen+" requires username/password")
			warning := "warning: server.http.listen " + listen + " binds all interfaces"
			if all := strings.HasPrefix(listen, "0.0.0.0"); all != strings.Contains(stderr, warning) {
				t.Errorf("stderr = %q; all-interfaces warning present = %v, want %v", stderr, !all, all)
			}
		})
	}
}

// A plaintext password is refused: only bcrypt hashes are accepted.
func TestTCP_PlaintextPasswordRefused(t *testing.T) {
	expectRefused(t, fmt.Sprintf("[server.http]\nenabled = true\nlisten = \"127.0.0.1:9876\"\nusername = \"admin\"\n%s = \"secret\"\n", "pass"+"word"),
		"http.password must be a bcrypt hash")
}

// A malformed listen address is a config error (#176).
func TestTCP_InvalidListenAddress(t *testing.T) {
	hash := hashPassword(t, "secret")
	for _, listen := range []string{"notanaddr", "127.0.0.1:99999"} {
		t.Run(listen, func(t *testing.T) {
			expectRefused(t, fmt.Sprintf("[server.http]\nenabled = true\nlisten = %q\nusername = \"admin\"\n%s = %q\n", listen, "pass"+"word", hash),
				fmt.Sprintf("invalid listen address: %q", listen))
		})
	}
}

// A port that is already in use stops the daemon with the bind error.
func TestTCP_PortInUse(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	expectRefused(t, fmt.Sprintf("[server.http]\nenabled = true\nlisten = %q\nusername = \"admin\"\n%s = %q\n", addr, "pass"+"word", hashPassword(t, "secret")),
		"cannot bind "+addr+": ")
}
