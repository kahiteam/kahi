//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
	"github.com/kahiteam/kahi/internal/testutil"
)

// Prometheus metrics (FUNC-060..062, ADR-049, #153).

const metricsUser, metricsPass = "admin", "testpass"

// metricsHeader is the [server.http] and [server.metrics] part of a config.
func metricsHeader(t *testing.T, enabled bool) (string, string) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	return fmt.Sprintf("[server.http]\nenabled = true\nlisten = %q\nusername = %q\npassword = %q\n\n[server.metrics]\nenabled = %v\n\n",
		addr, metricsUser, hashPassword(t, metricsPass), enabled), addr
}

func scrapeUnix(t *testing.T, socket string) string {
	t.Helper()
	r := apiDo(t, socket, "GET", "/metrics", "")
	if r.status != http.StatusOK {
		t.Fatalf("GET /metrics over the socket: %d %s", r.status, r.body)
	}
	return string(r.body)
}

func tcpGet(t *testing.T, addr string, auth bool) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
	if auth {
		req.SetBasicAuth(metricsUser, metricsPass)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

// value returns the value of the sample whose name and labels start with
// series, or -1 when there is none.
func value(body, series string) float64 {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, series+" ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, series)), 64)
			if err == nil {
				return v
			}
		}
	}
	return -1
}

func waitValue(t *testing.T, socket, series string, want float64) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		body := scrapeUnix(t, socket)
		if value(body, series) == want {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %v, want %v", series, value(body, series), want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// FUNC-060 steps 1-3, 6 and 7: auth on TCP only, the format, Go collector,
// naming.
func TestMetrics_EndpointAndAuth(t *testing.T) {
	t.Parallel()
	head, addr := metricsHeader(t, true)
	_, socket, _ := startDaemonOut(t, head+"[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n")
	code, ctype, body := tcpGet(t, addr, true)
	if code != 200 || !strings.HasPrefix(ctype, "text/plain") || !strings.Contains(body, "# TYPE") {
		t.Fatalf("TCP with auth: %d %q", code, ctype)
	}
	if code, _, _ := tcpGet(t, addr, false); code != http.StatusUnauthorized {
		t.Errorf("TCP without credentials: %d", code)
	}
	body = scrapeUnix(t, socket)
	if !strings.Contains(body, "go_goroutines") {
		t.Error("no go_goroutines")
	}
	if _, err := os.Stat("/proc/self"); err == nil && !strings.Contains(body, "process_resident_memory_bytes") {
		t.Error("no process_resident_memory_bytes on Linux")
	}
	for _, m := range regexp.MustCompile(`(?m)^# TYPE (\S+) (\S+)$`).FindAllStringSubmatch(body, -1) {
		name, typ := m[1], m[2]
		if strings.HasPrefix(name, "kahi_") && !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(name) {
			t.Errorf("%s is not snake_case", name)
		}
		if typ == "counter" && strings.HasPrefix(name, "kahi_") && !regexp.MustCompile(`^kahi_[a-z0-9_]+_total$`).MatchString(name) {
			t.Errorf("counter %s does not end in _total", name)
		}
	}
}

// FUNC-060 steps 4 and 5: config error without http; 404 when off.
func TestMetrics_DisabledAndConfigError(t *testing.T) {
	t.Parallel()
	expectRefused(t, "[server.metrics]\nenabled = true\n", "server.metrics.enabled requires server.http.enabled")
	head, _ := metricsHeader(t, false)
	_, socket, _ := startDaemonOut(t, head)
	r := apiDo(t, socket, "GET", "/metrics", "")
	if r.status != http.StatusNotFound || !strings.Contains(string(r.body), `"error":"metrics not enabled"`) {
		t.Errorf("disabled: %d %s", r.status, r.body)
	}
}

// FUNC-060 steps 8 and 9: pool drops and breaker series (named, never the
// URL); a [server.metrics] change on reload only warns.
func TestMetrics_PoolBreakerAndReloadWarning(t *testing.T) {
	t.Parallel()
	head, _ := metricsHeader(t, true)
	body := head + "[eventlisteners.l]\ncommand = \"/bin/cat\"\nevents = [\"PROCESS_STATE_EXITED\"]\nstartsecs = 0\nstopwaitsecs = 2\n\n" +
		"[webhooks.t]\nurl = \"https://example.invalid/MARKER-secret\"\nevents = [\"PROCESS_STATE_FATAL\"]\n\n"
	_, socket, out := startDaemonOut(t, body)
	m := scrapeUnix(t, socket)
	if value(m, `kahi_event_buffer_drops_total{pool="l"}`) != 0 {
		t.Errorf("no drops series for l:\n%s", m)
	}
	if value(m, `kahi_webhook_circuit_breaker{status="closed",webhook="t"}`) != 1 || value(m, `kahi_webhook_circuit_breaker{status="open",webhook="t"}`) != 0 {
		t.Errorf("no breaker series for t")
	}
	if strings.Contains(m, "MARKER") {
		t.Error("the webhook URL leaked into /metrics")
	}
	rewriteConfig(t, socket, strings.Replace(body, "[server.metrics]\nenabled = true", "[server.metrics]\nenabled = false", 1))
	if _, err := ctl.NewUnixClient(socket).Reload(); err != nil {
		t.Fatal(err)
	}
	waitOutputFile(t, out, "server.metrics change takes effect after restart", 5*time.Second)
	scrapeUnix(t, socket) // still served: no rebind
}

// FUNC-061 steps 1, 3, 6 and 9: state, exits, uptime.
func TestMetrics_ProcessStateExitsUptime(t *testing.T) {
	t.Parallel()
	head, _ := metricsHeader(t, true)
	dir := realDir(t)
	exit3 := writeScript(t, dir, "exit3.sh", "sleep 1\nexit 3\n")
	exit1 := writeScript(t, dir, "exit1.sh", "sleep 1\nexit 1\n")
	client, socket, _ := startDaemonOut(t, head+
		"[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n\n"+
		fmt.Sprintf("[programs.three]\ncommand = %q\nstartsecs = 0\nautorestart = false\nexitcodes = [0, 3]\n\n", exit3)+
		fmt.Sprintf("[programs.one]\ncommand = %q\nstartsecs = 0\nautorestart = false\n", exit1))
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	m := waitValue(t, socket, `kahi_process_state{group="web",name="web"}`, 20)
	u1 := value(m, `kahi_process_uptime_seconds{name="web"}`)
	waitValue(t, socket, `kahi_process_exit_total{expected="true",name="three"}`, 1)
	waitValue(t, socket, `kahi_process_exit_total{expected="false",name="one"}`, 1)
	time.Sleep(2 * time.Second)
	m = scrapeUnix(t, socket)
	if u2 := value(m, `kahi_process_uptime_seconds{name="web"}`); u2-u1 < 1 {
		t.Errorf("uptime %v then %v, want +1 over 2s", u1, u2)
	}
	if err := client.Stop("web"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "web", "STOPPED", 5*time.Second)
	m = waitValue(t, socket, `kahi_process_exit_total{expected="true",name="web"}`, 1)
	if v := value(m, `kahi_process_uptime_seconds{name="web"}`); v != 0 {
		t.Errorf("uptime of STOPPED web = %v", v)
	}
	if v := value(m, `kahi_process_state{group="web",name="web"}`); v != 0 {
		t.Errorf("state of STOPPED web = %v", v)
	}
}

// FUNC-061 steps 2 and 7: every spawn counts, failed spawns too.
func TestMetrics_StartCounts(t *testing.T) {
	t.Parallel()
	head, _ := metricsHeader(t, true)
	dir := realDir(t)
	exit1 := writeScript(t, dir, "fail.sh", "exit 1\n")
	client, socket, _ := startDaemonOut(t, head+
		fmt.Sprintf("[programs.crash]\ncommand = %q\nstartsecs = 1\nstartretries = 4\n\n", exit1)+
		fmt.Sprintf("[programs.missing]\ncommand = %q\nstartretries = 2\n", filepath.Join(dir, "nonexistent")))
	waitForState(t, client, "crash", "FATAL", 30*time.Second)
	waitForState(t, client, "missing", "FATAL", 30*time.Second)
	m := waitValue(t, socket, `kahi_process_start_total{name="crash"}`, 5)
	if v := value(m, `kahi_process_start_total{name="missing"}`); v != 3 {
		t.Errorf("missing start_total %v, want 3", v)
	}
	if v := value(m, `kahi_process_exit_total{expected="false",name="missing"}`); v != 3 {
		t.Errorf("missing exit_total{expected=false} %v, want 3", v)
	}
}

// FUNC-061 steps 4, 5 and 8: counters survive a change, removed processes
// lose their series.
func TestMetrics_ReloadKeepsAndRemoves(t *testing.T) {
	t.Parallel()
	head, _ := metricsHeader(t, true)
	prog := func(env string, n int) string {
		return fmt.Sprintf("[programs.p]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\nenvironment = { V = %q }\n\n"+
			"[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\nnumprocs = %d\nprocess_name = \"web_%%(process_num)d\"\n\n", env, n)
	}
	client, socket, _ := startDaemonOut(t, head+prog("1", 2))
	waitForState(t, client, "p", "RUNNING", 5*time.Second)
	waitForState(t, client, "web_1", "RUNNING", 5*time.Second)
	m := waitValue(t, socket, `kahi_process_start_total{name="p"}`, 1)
	_ = m

	rewriteConfig(t, socket, head+prog("2", 1))
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "p", "RUNNING", 10*time.Second)
	m = waitValue(t, socket, `kahi_process_start_total{name="p"}`, 2)
	if strings.Contains(m, `name="web_1"`) {
		t.Error("web_1 series left after numprocs 2 -> 1")
	}
	if !strings.Contains(m, `name="web_0"`) {
		t.Error("web_0 series gone")
	}

	rewriteConfig(t, socket, head+"[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\nnumprocs = 1\nprocess_name = \"web_%(process_num)d\"\n")
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for strings.Contains(scrapeUnix(t, socket), `name="p"`) {
		if time.Now().After(deadline) {
			t.Fatal("series for removed p left")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// FUNC-062: uptime, per-state counts, reload counters, kahi_info.
func TestMetrics_Supervisor(t *testing.T) {
	t.Parallel()
	head, _ := metricsHeader(t, true)
	body := head + "[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n"
	client, socket, _ := startDaemonOut(t, body)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	m := waitValue(t, socket, `kahi_supervisor_processes{state="running"}`, 1)
	for _, st := range []string{"stopped", "starting", "backoff", "stopping", "exited", "fatal"} {
		if value(m, fmt.Sprintf(`kahi_supervisor_processes{state=%q}`, st)) != 0 {
			t.Errorf("state %s missing or nonzero", st)
		}
	}
	u1 := value(m, "kahi_supervisor_uptime_seconds")
	time.Sleep(2 * time.Second)
	m = scrapeUnix(t, socket)
	if value(m, "kahi_supervisor_uptime_seconds")-u1 < 1 {
		t.Error("supervisor uptime did not grow")
	}
	if value(m, "kahi_supervisor_config_reload_total") != 0 {
		t.Fatalf("reload_total before any reload: %v", value(m, "kahi_supervisor_config_reload_total"))
	}

	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	m = scrapeUnix(t, socket)
	if value(m, "kahi_supervisor_config_reload_total") != 1 || value(m, "kahi_supervisor_config_reload_errors_total") != 0 {
		t.Errorf("after a good reload: %v / %v", value(m, "kahi_supervisor_config_reload_total"), value(m, "kahi_supervisor_config_reload_errors_total"))
	}
	dir := filepath.Dir(socket)
	if _, _, code := runKahi(t, dir, "ctl", "-s", socket, "reread"); code != 0 {
		t.Fatal("reread failed")
	}
	if _, _, code := runKahi(t, dir, "ctl", "-s", socket, "update", "web"); code != 0 {
		t.Fatal("update web failed")
	}
	rewriteConfig(t, socket, body+"[programs.bad\n")
	_, _ = client.Reload()
	m = scrapeUnix(t, socket)
	if value(m, "kahi_supervisor_config_reload_total") != 3 || value(m, "kahi_supervisor_config_reload_errors_total") != 1 {
		t.Errorf("after update + bad reload (reread not counted): %v / %v", value(m, "kahi_supervisor_config_reload_total"), value(m, "kahi_supervisor_config_reload_errors_total"))
	}

	stdout, _, _ := runKahi(t, dir, "version")
	ver := strings.Fields(strings.SplitN(stdout, "\n", 2)[0])
	info := regexp.MustCompile(`kahi_info\{fips="([^"]*)",go_version="([^"]*)",version="([^"]*)"\} 1`).FindStringSubmatch(m)
	if info == nil || len(ver) < 2 || !strings.Contains(stdout, info[2]) || !strings.Contains(ver[1], strings.TrimPrefix(info[3], "v")) {
		t.Errorf("kahi_info %v does not match kahi version %q", info, stdout)
	}
}
