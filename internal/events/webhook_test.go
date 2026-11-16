package events

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a log sink safe to read while the worker writes.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// recorder is a webhook receiver that answers with the next status of a
// script (the last one repeats) and records each request.
type recorder struct {
	mu       sync.Mutex
	statuses []int
	header   http.Header
	bodies   [][]byte
	headers  []http.Header
	times    []time.Time
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.bodies = append(r.bodies, b)
	r.headers = append(r.headers, req.Header.Clone())
	r.times = append(r.times, time.Now())
	status := http.StatusOK
	if n := len(r.bodies) - 1; len(r.statuses) > 0 {
		status = r.statuses[min(n, len(r.statuses)-1)]
	}
	for k, v := range r.header {
		w.Header()[k] = v
	}
	r.mu.Unlock()
	w.WriteHeader(status)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *recorder) body(i int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(r.bodies[i], &m)
	return m
}

func (r *recorder) waitCount(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for r.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d requests after %v, want %d", r.count(), timeout, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// fastTimers shortens the backoff and breaker interval for a test.
func fastTimers(t *testing.T) {
	t.Helper()
	base, limit, interval := webhookBackoffBase, webhookBackoffCap, webhookBreakerInterval
	webhookBackoffBase, webhookBackoffCap, webhookBreakerInterval = 10*time.Millisecond, 100*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { webhookBackoffBase, webhookBackoffCap, webhookBreakerInterval = base, limit, interval })
}

func newTestManager(t *testing.T, cfgs ...WebhookConfig) (*Bus, *WebhookManager, *syncBuf) {
	t.Helper()
	logs := &syncBuf{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bus := NewBus(logger)
	m := NewWebhookManager(bus, logger)
	m.hostname = "host1"
	m.Apply(cfgs)
	t.Cleanup(m.Stop)
	return bus, m, logs
}

func hook(name, url string) WebhookConfig {
	return WebhookConfig{Name: name, URL: url, Events: []EventType{ProcessStateFatal}, Timeout: 5 * time.Second, MaxRetries: 3}
}

func fatal(bus *Bus, name string) {
	bus.Publish(Event{Type: ProcessStateFatal, Timestamp: time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC),
		Data: map[string]string{"name": name, "group": "g", "state": "FATAL", "from_state": "BACKOFF"}})
}

func waitLog(t *testing.T, logs *syncBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log lacks %q:\n%s", want, logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// FUNC-057 generic: event, process, group, timestamp and details without
// name and group; the configured headers are sent.
func TestWebhookGenericPayload(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	h := hook("t", srv.URL)
	h.Headers = map[string]string{"X-Team": "ops"}
	bus, _, _ := newTestManager(t, h)
	fatal(bus, "p")
	rec.waitCount(t, 1, 5*time.Second)
	b := rec.body(0)
	if b["event"] != "PROCESS_STATE_FATAL" || b["process"] != "p" || b["group"] != "g" || b["timestamp"] != "2026-11-16T01:02:03Z" {
		t.Errorf("body = %v", b)
	}
	details, _ := b["details"].(map[string]any)
	if _, ok := details["name"]; ok || details["from_state"] != "BACKOFF" {
		t.Errorf("details = %v, want from_state and no name", details)
	}
	if _, ok := details["group"]; ok {
		t.Errorf("details has group: %v", details)
	}
	if len(b) != 5 {
		t.Errorf("keys = %v, want exactly 5", b)
	}
	if got := rec.headers[0].Get("X-Team"); got != "ops" {
		t.Errorf("X-Team = %q", got)
	}
	if got := rec.headers[0].Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestWebhookSlackAndPagerDutyPayloads(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	slack := hook("s", srv.URL)
	slack.Template = "slack"
	slack.Events = []EventType{ProcessStateFatal, ProcessStateExited}
	bus, _, _ := newTestManager(t, slack)
	fatal(bus, "p")
	rec.waitCount(t, 1, 5*time.Second)
	if got := rec.body(0); got["text"] != "Process p entered FATAL state on host1" || len(got) != 1 {
		t.Errorf("slack FATAL = %v", got)
	}
	bus.Publish(Event{Type: ProcessStateExited, Data: map[string]string{"name": "p", "group": "g"}})
	rec.waitCount(t, 2, 5*time.Second)
	if got := rec.body(1); got["text"] != "PROCESS_STATE_EXITED for p on host1" {
		t.Errorf("slack EXITED = %v", got)
	}

	rec2 := &recorder{}
	srv2 := httptest.NewServer(rec2)
	defer srv2.Close()
	pd := hook("pd", srv2.URL)
	pd.Template, pd.RoutingKey = "pagerduty", "rk"
	bus2, _, _ := newTestManager(t, pd)
	fatal(bus2, "p")
	rec2.waitCount(t, 1, 5*time.Second)
	b := rec2.body(0)
	payload, _ := b["payload"].(map[string]any)
	if b["routing_key"] != "rk" || b["event_action"] != "trigger" || b["dedup_key"] != "kahi-host1-p" ||
		payload["severity"] != "critical" || payload["source"] != "host1" || payload["summary"] != "Process p entered FATAL state on host1" {
		t.Errorf("pagerduty = %v", b)
	}
}

// 500, 500, 200: two retries with growing delays, logged as retrying.
func TestWebhookRetriesThenSucceeds(t *testing.T) {
	fastTimers(t)
	rec := &recorder{statuses: []int{500, 500, 200}}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	bus, _, logs := newTestManager(t, hook("t", srv.URL))
	fatal(bus, "p")
	rec.waitCount(t, 3, 5*time.Second)
	if n := strings.Count(logs.String(), "webhook delivery failed for t: status 500, retrying"); n != 2 {
		t.Errorf("%d retry lines, want 2:\n%s", n, logs.String())
	}
	time.Sleep(50 * time.Millisecond)
	if rec.count() != 3 || strings.Contains(logs.String(), "permanently") {
		t.Errorf("%d requests, log:\n%s", rec.count(), logs.String())
	}
}

// max_retries counts retries after the first attempt.
func TestWebhookRetriesExhausted(t *testing.T) {
	fastTimers(t)
	rec := &recorder{statuses: []int{500}}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	h := hook("t", srv.URL)
	h.MaxRetries = 1
	bus, _, logs := newTestManager(t, h)
	fatal(bus, "p")
	waitLog(t, logs, "webhook delivery failed permanently for t")
	if rec.count() != 2 {
		t.Errorf("%d requests, want 2", rec.count())
	}
}

// A 4xx other than 408/429 and a 3xx fail at once; a redirect is not
// followed, so headers never go elsewhere.
func TestWebhookNoRetryAndNoRedirect(t *testing.T) {
	fastTimers(t)
	rec := &recorder{statuses: []int{404}}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	bus, _, logs := newTestManager(t, hook("t", srv.URL))
	fatal(bus, "p")
	waitLog(t, logs, "webhook delivery failed permanently for t")
	if rec.count() != 1 {
		t.Errorf("404: %d requests, want 1", rec.count())
	}

	target := &recorder{}
	tsrv := httptest.NewServer(target)
	defer tsrv.Close()
	redir := httptest.NewServer(http.RedirectHandler(tsrv.URL, http.StatusFound))
	defer redir.Close()
	bus2, _, logs2 := newTestManager(t, hook("r", redir.URL))
	fatal(bus2, "p")
	waitLog(t, logs2, "webhook delivery failed permanently for r")
	if target.count() != 0 {
		t.Errorf("redirect followed: %d requests at the target", target.count())
	}
}

// 408 and 429 are retried; Retry-After (seconds or HTTP date) sets a lower
// bound on the wait.
func TestWebhookRetryAfter(t *testing.T) {
	fastTimers(t)
	// The HTTP date has whole seconds, so now+3s is between 2 and 3 s away.
	for name, tc := range map[string]struct {
		value func() string
		min   time.Duration
	}{
		"seconds": {func() string { return "1" }, 900 * time.Millisecond},
		"date":    {func() string { return time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat) }, 1500 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{statuses: []int{429, 200}, header: http.Header{"Retry-After": {tc.value()}}}
			srv := httptest.NewServer(rec)
			defer srv.Close()
			bus, _, _ := newTestManager(t, hook("t", srv.URL))
			fatal(bus, "p")
			rec.waitCount(t, 2, 6*time.Second)
			if gap := rec.times[1].Sub(rec.times[0]); gap < tc.min {
				t.Errorf("gap %v, want >= %v", gap, tc.min)
			}
		})
	}
	rec := &recorder{statuses: []int{408, 200}}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	bus, _, _ := newTestManager(t, hook("t", srv.URL))
	fatal(bus, "p")
	rec.waitCount(t, 2, 5*time.Second)
}

// Five failed deliveries open the breaker; events are dropped until the
// interval passes; a successful probe closes it, a failed one keeps it open.
func TestWebhookCircuitBreaker(t *testing.T) {
	fastTimers(t)
	rec := &recorder{statuses: []int{500}}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	h := hook("t", srv.URL)
	h.MaxRetries = 0
	bus, _, logs := newTestManager(t, h)
	for i := 0; i < 5; i++ {
		fatal(bus, "p")
		bus.Flush()
	}
	waitLog(t, logs, "webhook circuit breaker open for t")
	fatal(bus, "p") // dropped while open
	bus.Flush()
	if rec.count() != 5 {
		t.Fatalf("%d requests while open, want 5", rec.count())
	}

	// A failed probe keeps it open for another interval.
	time.Sleep(webhookBreakerInterval + 20*time.Millisecond)
	fatal(bus, "p")
	bus.Flush()
	fatal(bus, "p")
	bus.Flush()
	if rec.count() != 6 {
		t.Fatalf("%d requests after a failed probe, want 6 (probe only)", rec.count())
	}

	rec.mu.Lock()
	rec.statuses = []int{200}
	rec.mu.Unlock()
	time.Sleep(webhookBreakerInterval + 20*time.Millisecond)
	fatal(bus, "p")
	waitLog(t, logs, "webhook circuit breaker closed for t")
	fatal(bus, "p")
	bus.Flush()
	if rec.count() != 8 {
		t.Errorf("%d requests after closing, want 8", rec.count())
	}
}

// The URL and header values never reach the log.
func TestWebhookLogsNoURL(t *testing.T) {
	fastTimers(t)
	h := hook("t", "http://127.0.0.1:1/MARKER123?token=MARKER456")
	h.MaxRetries = 1
	h.Headers = map[string]string{"Authorization": "Bearer MARKER789"}
	bus, _, logs := newTestManager(t, h)
	fatal(bus, "p")
	waitLog(t, logs, "webhook delivery failed permanently for t")
	for _, m := range []string{"MARKER123", "MARKER456", "MARKER789"} {
		if strings.Contains(logs.String(), m) {
			t.Errorf("log contains %s:\n%s", m, logs.String())
		}
	}
}

// A stalled receiver: the queue keeps 100 events and reports the drops.
func TestWebhookQueueFull(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	h := hook("t", srv.URL)
	h.Timeout = 30 * time.Second
	bus, _, logs := newTestManager(t, h)
	for i := 0; i < 150; i++ {
		fatal(bus, "p")
	}
	waitLogWithin(t, logs, "webhook queue full for t, dropped", 15*time.Second)
}

func waitLogWithin(t *testing.T, logs *syncBuf, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !strings.Contains(logs.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log lacks %q:\n%s", want, logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Apply rebuilds the set by name: added webhooks start, removed ones stop,
// a changed one restarts with a closed breaker.
func TestWebhookApply(t *testing.T) {
	fastTimers(t)
	a, n := &recorder{}, &recorder{}
	sa, sn := httptest.NewServer(a), httptest.NewServer(n)
	defer sa.Close()
	defer sn.Close()
	bus, m, _ := newTestManager(t, hook("a", sa.URL))
	m.Apply([]WebhookConfig{hook("a", sa.URL), hook("n", sn.URL)})
	fatal(bus, "p")
	a.waitCount(t, 1, 5*time.Second)
	n.waitCount(t, 1, 5*time.Second)
	m.Apply([]WebhookConfig{hook("a", sa.URL)})
	fatal(bus, "p")
	a.waitCount(t, 2, 5*time.Second)
	bus.Flush()
	if n.count() != 1 {
		t.Errorf("removed webhook got %d requests, want 1", n.count())
	}

	// Open a's breaker, then change it: the next event is delivered.
	a.mu.Lock()
	a.statuses = []int{500}
	a.mu.Unlock()
	failing := hook("a", sa.URL)
	failing.MaxRetries = 0
	m.Apply([]WebhookConfig{failing})
	for i := 0; i < 5; i++ {
		fatal(bus, "p")
		bus.Flush()
	}
	before := a.count()
	fatal(bus, "p")
	bus.Flush()
	if a.count() != before {
		t.Fatalf("breaker not open: %d -> %d", before, a.count())
	}
	changed := failing
	changed.Timeout = 6 * time.Second
	m.Apply([]WebhookConfig{changed})
	fatal(bus, "p")
	a.waitCount(t, before+1, 5*time.Second)
}
