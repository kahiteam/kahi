//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// hookServer is a webhook receiver: it answers with the next status of a
// script (the last one repeats) and records every request.
type hookServer struct {
	*httptest.Server
	mu       sync.Mutex
	statuses []int
	header   http.Header
	stall    chan struct{} // when set, requests block until it is closed
	bodies   [][]byte
	headers  []http.Header
	times    []time.Time
}

func newHookServer(t *testing.T, statuses ...int) *hookServer {
	t.Helper()
	h := &hookServer{statuses: statuses}
	h.Server = httptest.NewServer(h)
	t.Cleanup(h.Close)
	return h
}

func (h *hookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	h.bodies = append(h.bodies, b)
	h.headers = append(h.headers, r.Header.Clone())
	h.times = append(h.times, time.Now())
	status := http.StatusOK
	if n := len(h.bodies) - 1; len(h.statuses) > 0 {
		status = h.statuses[min(n, len(h.statuses)-1)]
	}
	for k, v := range h.header {
		w.Header()[k] = v
	}
	stall := h.stall
	h.mu.Unlock()
	if stall != nil {
		select {
		case <-stall:
		case <-r.Context().Done():
		}
		return
	}
	if status == http.StatusFound {
		w.Header().Set("Location", h.header.Get("X-Redirect-To"))
	}
	w.WriteHeader(status)
}

func (h *hookServer) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.bodies)
}

func (h *hookServer) body(i int) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(h.bodies[i], &m)
	return m
}

func (h *hookServer) waitCount(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for h.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d webhook requests after %v, want %d", h.count(), timeout, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fatalProg is a program that goes FATAL at once: it exits before startsecs
// and has no retries.
func fatalProg(name string, autostart bool) string {
	return fmt.Sprintf("[programs.%s]\ncommand = \"/usr/bin/false\"\nstartsecs = 1\nstartretries = 0\nautorestart = \"false\"\nautostart = %v\n\n", name, autostart)
}

func webhookTOML(name, url, extra string) string {
	return fmt.Sprintf("[webhooks.%s]\nurl = %q\nevents = [\"PROCESS_STATE_FATAL\"]\n%s\n", name, url, extra)
}

func readOut(f *os.File) string {
	b, _ := os.ReadFile(f.Name())
	return string(b)
}

// FUNC-056/057: a FATAL is POSTed to every webhook on the event, once, as
// the generic JSON object.
func TestWebhook_GenericAndMultiple(t *testing.T) {
	t.Parallel()
	a, b, run := newHookServer(t), newHookServer(t), newHookServer(t)
	client, _, out := startDaemonOut(t, webhookTOML("a", a.URL, "")+webhookTOML("b", b.URL, "")+
		"[webhooks.run]\nurl = \""+run.URL+"\"\nevents = [\"SUPERVISOR_STATE_RUNNING\"]\n\n"+
		"[webhooks.typo]\nurl = \""+run.URL+"\"\nevents = [\"process_state\"]\n\n"+
		fatalProg("p", true)+fatalProg("q", false))
	waitForState(t, client, "p", "FATAL", 10*time.Second)
	// Webhooks subscribe before the supervisor announces it runs.
	run.waitCount(t, 1, 5*time.Second)
	if got := run.body(0); got["event"] != "SUPERVISOR_STATE_RUNNING" || got["process"] != "" {
		t.Errorf("run body = %v", got)
	}
	// An unknown event name only warns: the daemon runs, the name is ignored.
	if o := readOut(out); !strings.Contains(o, "webhooks.typo.events: unknown event type: process_state (valid: ") {
		t.Errorf("no unknown-event warning:\n%s", o)
	}
	a.waitCount(t, 1, 5*time.Second)
	b.waitCount(t, 1, 5*time.Second)
	// Sentinel: q's FATAL arrives after any duplicate of p's would have.
	// A start of a program that goes FATAL reports the failure; the event
	// is what matters.
	_ = client.Start("q")
	a.waitCount(t, 2, 10*time.Second)
	b.waitCount(t, 2, 10*time.Second)
	for name, h := range map[string]*hookServer{"a": a, "b": b} {
		if h.count() != 2 || h.body(0)["process"] != "p" || h.body(1)["process"] != "q" {
			t.Errorf("%s: %d requests, processes %v, %v; want p then q once each", name, h.count(), h.body(0)["process"], h.body(1)["process"])
		}
	}
	body := a.body(0)
	if len(body) != 5 || body["event"] != "PROCESS_STATE_FATAL" || body["group"] != "p" {
		t.Errorf("body = %v, want event, process, group, timestamp, details", body)
	}
	for _, h := range []*hookServer{a, run} {
		ts, err := time.Parse(time.RFC3339, fmt.Sprint(h.body(0)["timestamp"]))
		if err != nil || time.Since(ts) > time.Minute || time.Since(ts) < -time.Minute {
			t.Errorf("timestamp %v (%v), want about now", h.body(0)["timestamp"], err)
		}
	}
	if run.count() != 1 {
		t.Errorf("run got %d POSTs, want 1 (typo's unknown event never fires)", run.count())
	}
	details, _ := body["details"].(map[string]any)
	if _, ok := details["name"]; ok || details["state"] != "FATAL" {
		t.Errorf("details = %v, want state and no name", details)
	}
	if _, ok := details["group"]; ok {
		t.Errorf("details has group: %v", details)
	}
}

// 500, 500, 200: retries after about 1 s and 2 s, each logged.
func TestWebhook_RetryBackoff(t *testing.T) {
	t.Parallel()
	h := newHookServer(t, 500, 500, 200)
	client, _, out := startDaemonOut(t, webhookTOML("t", h.URL, "")+fatalProg("p", true))
	waitForState(t, client, "p", "FATAL", 10*time.Second)
	h.waitCount(t, 3, 15*time.Second)
	gap1, gap2 := h.times[1].Sub(h.times[0]), h.times[2].Sub(h.times[1])
	if gap1 < 900*time.Millisecond || gap2 < 1800*time.Millisecond || gap2 <= gap1 {
		t.Errorf("gaps %v, %v; want >= 0.9 s, >= 1.8 s and growing", gap1, gap2)
	}
	if n := linesWith(readOut(out), "webhook delivery failed for t: ", ", retrying"); n != 2 {
		t.Errorf("%d retry lines, want 2:\n%s", n, readOut(out))
	}
}

// Failure handling on one FATAL: retries exhausted, a 404 not retried, a
// redirect not followed, 408 retried, 429 waiting for Retry-After, the
// deprecated retries alias, and an unreachable URL whose path never reaches
// the log.
func TestWebhook_FailureModes(t *testing.T) {
	t.Parallel()
	t1 := newHookServer(t, 500)
	t2 := newHookServer(t, 404)
	target := newHookServer(t)
	t3 := newHookServer(t, http.StatusFound)
	t3.header = http.Header{"X-Redirect-To": {target.URL}}
	t4 := newHookServer(t, 408, 200)
	t5 := newHookServer(t, 429, 200)
	t5.header = http.Header{"Retry-After": {"3"}}
	t7 := newHookServer(t, 500)
	client, _, out := startDaemonOut(t,
		webhookTOML("t1", t1.URL, "max_retries = 1")+
			webhookTOML("t2", t2.URL, "")+
			webhookTOML("t3", t3.URL, "max_retries = 0")+
			webhookTOML("t4", t4.URL, "")+
			webhookTOML("t5", t5.URL, "max_retries = 1")+
			webhookTOML("t6", "http://127.0.0.1:1/MARKER123", "max_retries = 0")+
			webhookTOML("t7", t7.URL, "retries = 1")+
			fatalProg("p", true))
	waitForState(t, client, "p", "FATAL", 10*time.Second)
	for _, name := range []string{"t1", "t2", "t3", "t6", "t7"} {
		waitOutputFile(t, out, "webhook delivery failed permanently for "+name, 15*time.Second)
	}
	t4.waitCount(t, 2, 10*time.Second)
	t5.waitCount(t, 2, 10*time.Second)
	if t1.count() != 2 {
		t.Errorf("t1 (max_retries = 1, always 500): %d POSTs, want 2", t1.count())
	}
	if t2.count() != 1 {
		t.Errorf("t2 (404): %d POSTs, want 1", t2.count())
	}
	if target.count() != 0 {
		t.Errorf("t3 redirect followed: the target got %d requests", target.count())
	}
	if gap := t5.times[1].Sub(t5.times[0]); gap < 2700*time.Millisecond {
		t.Errorf("t5 (429, Retry-After 3): gap %v, want >= 2.7 s", gap)
	}
	if t7.count() != 2 {
		t.Errorf("t7 (retries = 1): %d POSTs, want 2", t7.count())
	}
	o := readOut(out)
	if !strings.Contains(o, "webhooks.t7.retries is deprecated; use max_retries") {
		t.Errorf("no deprecation warning:\n%s", o)
	}
	if strings.Contains(o, "MARKER123") {
		t.Errorf("the daemon output contains the URL path MARKER123:\n%s", o)
	}
}

// Five failed deliveries open the breaker: a sixth FATAL reaches the
// sentinel u, not t. Changing t and reloading restarts it with a closed
// breaker; a webhook added by reload receives events, a removed one stops.
func TestWebhook_BreakerAndReload(t *testing.T) {
	t.Parallel()
	tsrv, u, n := newHookServer(t, 500), newHookServer(t), newHookServer(t)
	programs := ""
	for i := 1; i <= 8; i++ {
		programs += fatalProg(fmt.Sprintf("f%d", i), i <= 5)
	}
	hooks := func(tExtra string, withN bool) string {
		s := webhookTOML("t", tsrv.URL, "max_retries = 0\n"+tExtra) + webhookTOML("u", u.URL, "")
		if withN {
			s += webhookTOML("n", n.URL, "")
		}
		return s
	}
	client, socket, out := startDaemonOut(t, hooks("", false)+programs)
	waitOutputFile(t, out, "webhook circuit breaker open for t", 20*time.Second)
	u.waitCount(t, 5, 10*time.Second)
	// A start of a program that goes FATAL reports the failure; the event
	// is what matters.
	_ = client.Start("f6")
	u.waitCount(t, 6, 10*time.Second)
	if tsrv.count() != 5 {
		t.Errorf("t got %d POSTs with its breaker open, want 5", tsrv.count())
	}

	pid := daemonPID(t, client)
	rewriteConfig(t, socket, hooks("timeout = 6", true)+programs)
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	_ = client.Start("f7")
	tsrv.waitCount(t, 6, 10*time.Second)
	n.waitCount(t, 1, 10*time.Second)
	if daemonPID(t, client) != pid {
		t.Error("the daemon restarted on reload")
	}

	rewriteConfig(t, socket, hooks("timeout = 6", false)+programs)
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	_ = client.Start("f8")
	u.waitCount(t, 8, 10*time.Second)
	if n.count() != 1 {
		t.Errorf("removed webhook n got %d POSTs, want 1", n.count())
	}
}

// A receiver that never answers holds one delivery; supervision is not
// blocked, and the bounded queue reports what it drops.
func TestWebhook_StalledReceiver(t *testing.T) {
	t.Parallel()
	h := newHookServer(t)
	h.stall = make(chan struct{})
	t.Cleanup(func() { close(h.stall) })
	client, _, out := startDaemonOut(t, webhookTOML("t", h.URL, "timeout = 30")+
		"[webhooks.s]\nurl = \""+h.URL+"\"\nevents = [\"PROCESS_STATE_STARTING\"]\ntimeout = 30\n\n"+
		fatalProg("p", true)+
		"[programs.q]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n\n"+
		"[programs.flap]\ncommand = \"/bin/sleep 0.05\"\nstartsecs = 0\nautorestart = \"true\"\nautostart = false\n\n")
	waitForState(t, client, "p", "FATAL", 10*time.Second)
	h.waitCount(t, 1, 10*time.Second)
	waitForState(t, client, "q", "RUNNING", 5*time.Second)
	for _, step := range []struct {
		op    func(string) error
		state string
	}{{client.Stop, "STOPPED"}, {client.Start, "RUNNING"}} {
		start := time.Now()
		if err := step.op("q"); err != nil {
			t.Fatal(err)
		}
		waitForState(t, client, "q", step.state, 3*time.Second)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("q reached %s after %v with a delivery in flight, want < 3 s", step.state, d)
		}
	}

	// STARTING events from a flapping program overflow s's queue of 100
	// (the first is held by the stalled delivery).
	if err := client.Start("flap"); err != nil {
		t.Fatal(err)
	}
	waitOutputFile(t, out, "webhook queue full for s, dropped", 30*time.Second)
	_ = client.Stop("flap")
}

// FUNC-057 templates and FUNC-058 expansion: url, a header and routing_key
// from the daemon environment; the config API masks them.
func TestWebhook_TemplatesAndExpansion(t *testing.T) {
	t.Parallel()
	slack, pd := newHookServer(t), newHookServer(t)
	host, _ := os.Hostname()
	client, socket, _ := startDaemonOutEnv(t, fmt.Sprintf(`[webhooks.slack]
url = "${KAHI_E2E_HOOK_URL}"
events = ["PROCESS_STATE_FATAL", "PROCESS_STATE_EXITED"]
template = "slack"
headers = { Authorization = "Bearer ${KAHI_E2E_TOKEN}" }

[webhooks.pd]
url = %q
events = ["PROCESS_STATE_FATAL"]
template = "pagerduty"
routing_key = "${KAHI_E2E_RK}"

`, pd.URL)+fatalProg("p", true)+"[programs.done]\ncommand = \"/usr/bin/true\"\nstartsecs = 0\nautorestart = \"false\"\nautostart = false\n",
		[]string{"KAHI_E2E_HOOK_URL=" + slack.URL, "KAHI_E2E_TOKEN=tok-e2e-secret", "KAHI_E2E_RK=rk-e2e-secret"})
	waitForState(t, client, "p", "FATAL", 10*time.Second)
	slack.waitCount(t, 1, 10*time.Second)
	if got := slack.body(0); got["text"] != "Process p entered FATAL state on "+host || len(got) != 1 {
		t.Errorf("slack body = %v", got)
	}
	if got := slack.headers[0].Get("Authorization"); got != "Bearer tok-e2e-secret" {
		t.Errorf("Authorization = %q", got)
	}
	pd.waitCount(t, 1, 10*time.Second)
	b := pd.body(0)
	payload, _ := b["payload"].(map[string]any)
	if b["routing_key"] != "rk-e2e-secret" || b["event_action"] != "trigger" || b["dedup_key"] != "kahi-"+host+"-p" ||
		payload["severity"] != "critical" || payload["source"] != host {
		t.Errorf("pagerduty body = %v", b)
	}

	_ = client.Start("done")
	slack.waitCount(t, 2, 10*time.Second)
	if got := slack.body(1)["text"]; got != "PROCESS_STATE_EXITED for done on "+host {
		t.Errorf("slack EXITED text = %v", got)
	}

	r := apiDo(t, socket, "GET", "/api/v1/config", "")
	var cfg struct {
		Webhooks map[string]struct {
			URL        string
			Headers    map[string]string
			RoutingKey string
		}
	}
	if err := json.Unmarshal(r.body, &cfg); err != nil {
		t.Fatalf("config: %v: %s", err, r.body)
	}
	s, p := cfg.Webhooks["slack"], cfg.Webhooks["pd"]
	if !strings.HasSuffix(s.URL, "/***") || s.Headers["Authorization"] != "***" || p.RoutingKey != "***" {
		t.Errorf("config view: slack %+v, pd %+v", s, p)
	}
	for _, secret := range []string{"tok-e2e-secret", "rk-e2e-secret"} {
		if strings.Contains(string(r.body), secret) {
			t.Errorf("GET /api/v1/config contains %s", secret)
		}
	}
}

// FUNC-059: plain HTTP is refused except for loopback or with
// allow_insecure; a certificate the system does not trust fails delivery.
func TestWebhook_TLS(t *testing.T) {
	t.Parallel()
	lo := newHookServer(t)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the TLS server got a request through an untrusted certificate")
	}))
	t.Cleanup(tlsSrv.Close)
	extra := ""
	var lan *hookServer
	if ip := nonLoopbackIPv4(); ip != "" {
		ln, err := net.Listen("tcp", "0.0.0.0:0")
		if err == nil {
			lan = &hookServer{}
			lan.Server = httptest.NewUnstartedServer(lan)
			lan.Listener = ln
			lan.Start()
			t.Cleanup(lan.Close)
			port := ln.Addr().(*net.TCPAddr).Port
			extra = webhookTOML("lan", fmt.Sprintf("http://%s:%d/h", ip, port), "allow_insecure = true")
		}
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(lo.URL, "http://"))
	client, _, out := startDaemonOut(t,
		webhookTOML("lo", lo.URL, "")+
			webhookTOML("lh", "http://localhost:"+port+"/h", "")+
			webhookTOML("v6", "http://[::1]:1/h", "max_retries = 0")+
			webhookTOML("tls", tlsSrv.URL, "max_retries = 1")+
			extra+fatalProg("p", true))
	waitForState(t, client, "p", "FATAL", 10*time.Second)
	lo.waitCount(t, 1, 5*time.Second)
	waitOutputFile(t, out, "webhook delivery failed permanently for tls", 15*time.Second)
	o := readOut(out)
	if n := linesWith(o, "webhook delivery failed for tls: ", "certificate"); n == 0 {
		t.Errorf("no certificate failure for tls:\n%s", o)
	}
	for _, name := range []string{"lo", "lh", "v6"} {
		if strings.Contains(o, "webhooks."+name+": allow_insecure") {
			t.Errorf("allow_insecure warning for loopback webhook %s", name)
		}
	}
	if lan != nil {
		if !strings.Contains(o, "webhooks.lan: allow_insecure set, sending over plain HTTP") {
			t.Errorf("no allow_insecure warning for lan:\n%s", o)
		}
		lan.waitCount(t, 1, 5*time.Second)
	}
}

func nonLoopbackIPv4() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}

// Config errors refuse to start; an invalid URL is not printed.
func TestWebhook_ConfigErrors(t *testing.T) {
	t.Parallel()
	ev := "events = [\"PROCESS_STATE_FATAL\"]\n"
	for body, want := range map[string]string{
		"url = \"https://h.example.com/x\"\n" + ev + "timeout = 0\n":              "webhooks.t.timeout must be >= 1",
		"url = \"https://h.example.com/x\"\n" + ev + "max_retries = -1\n":         "webhooks.t.max_retries must be >= 0",
		"url = \"https://h.example.com/x\"\nevents = [\"PROCESS_LOG_STDOUT\"]\n":  "webhooks.t.events: PROCESS_LOG events cannot be sent to webhooks",
		"url = \"https://h.example.com/x\"\n" + ev + "template = \"pagerduty\"\n": "webhooks.t: template pagerduty requires routing_key",
		"url = \"https://h.example.com/x\"\n" + ev + "template = \"bogus\"\n":     "unknown webhook template: bogus",
		"url = \"http://example.com/hook\"\n" + ev:                                "webhooks.t: URL must use HTTPS (set allow_insecure = true to override)",
		"url = \"ftp://x\"\n" + ev:                                                "invalid webhook URL: t",
		"url = \"https://\"\n" + ev:                                               "invalid webhook URL: t",
		"url = \"${KAHI_E2E_NOPE}\"\n" + ev:                                       "webhooks.t.url: undefined environment variable: ${KAHI_E2E_NOPE}",
	} {
		stderr := expectRefused(t, "[webhooks.t]\n"+body, want)
		if strings.Contains(body, "ftp://x") && strings.Contains(stderr, "ftp://x") {
			t.Errorf("the invalid URL is printed: %s", stderr)
		}
	}
}
