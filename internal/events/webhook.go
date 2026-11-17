package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Delivery timings (ADR-043). Package variables so tests can shorten them;
// no test waits minutes.
var (
	webhookBackoffBase     = time.Second
	webhookBackoffCap      = 60 * time.Second
	webhookRetryAfterCap   = 60 * time.Second
	webhookBreakerInterval = 5 * time.Minute
)

const (
	// webhookQueueSize bounds each webhook's queue (FUNC-056); the event
	// being delivered is held outside it.
	webhookQueueSize = 100
	// breakerThreshold consecutive failed deliveries open the breaker.
	breakerThreshold = 5
)

// WebhookConfig is one webhook as the daemon runs it (FUNC-056).
type WebhookConfig struct {
	Name       string // the config table name; the only identifier in logs
	URL        string
	Events     []EventType
	Headers    map[string]string
	Timeout    time.Duration // per POST
	MaxRetries int           // retries after the first attempt
	Template   string        // "generic" (or ""), "slack", "pagerduty"
	RoutingKey string        // PagerDuty integration key
}

// WebhookManager runs the configured webhooks: one bus subscription per
// webhook with a bounded queue, drained serially by its own goroutine, so
// delivery never blocks the bus or the supervisor (ADR-041, ADR-043).
type WebhookManager struct {
	bus      *Bus
	logger   *slog.Logger
	client   *http.Client
	hostname string

	mu    sync.Mutex
	hooks map[string]*webhook
}

// NewWebhookManager creates a manager with no webhooks; Apply sets them.
func NewWebhookManager(bus *Bus, logger *slog.Logger) *WebhookManager {
	host, _ := os.Hostname()
	return &WebhookManager{
		bus:    bus,
		logger: logger,
		// One client for all webhooks. Redirects are not followed, so
		// headers and routing keys are never re-sent elsewhere; a 3xx is a
		// failure. TLS verification uses the system trust store.
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		hostname: host,
		hooks:    map[string]*webhook{},
	}
}

// Apply rebuilds the webhook set by name (a nil manager does nothing): added webhooks start, removed ones
// stop after an in-flight delivery finishes, changed ones restart with a
// closed breaker; unchanged ones keep running (FUNC-056).
func (m *WebhookManager) Apply(cfgs []WebhookConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[string]WebhookConfig, len(cfgs))
	for _, c := range cfgs {
		want[c.Name] = c
	}
	for name, w := range m.hooks {
		if c, ok := want[name]; !ok || !reflect.DeepEqual(c, w.cfg) {
			w.stop(false)
			delete(m.hooks, name)
		}
	}
	for name, c := range want {
		if _, ok := m.hooks[name]; !ok {
			m.hooks[name] = m.start(c)
		}
	}
}

// Breakers returns, per webhook name, whether its circuit breaker is open
// (FUNC-060). Webhooks are named, never shown by URL.
func (m *WebhookManager) Breakers() map[string]bool {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool, len(m.hooks))
	for name, w := range m.hooks {
		out[name] = w.openState.Load()
	}
	return out
}

// Stop ends every webhook, aborting in-flight requests, and waits for the
// workers to return.
func (m *WebhookManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	hooks := m.hooks
	m.hooks = map[string]*webhook{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, w := range hooks {
		wg.Go(func() { w.stop(true) })
	}
	wg.Wait()
}

type webhook struct {
	m      *WebhookManager
	cfg    WebhookConfig
	id     uint64
	done   chan struct{} // closed on stop: no further retries
	ctx    context.Context
	cancel context.CancelFunc

	// Only the worker goroutine touches these.
	failures  int
	open      bool
	nextProbe time.Time

	// openState mirrors open for the metrics (FUNC-060).
	openState atomic.Bool
}

func (m *WebhookManager) start(c WebhookConfig) *webhook {
	ctx, cancel := context.WithCancel(context.Background())
	w := &webhook{m: m, cfg: c, done: make(chan struct{}), ctx: ctx, cancel: cancel}
	w.id = m.bus.SubscribeQueue("webhook "+c.Name, c.Events, webhookQueueSize, w.handle, func(dropped int) {
		m.logger.Warn(fmt.Sprintf("webhook queue full for %s, dropped %d oldest events", c.Name, dropped))
	})
	return w
}

// stop ends the webhook. A graceful stop (reload) lets an in-flight request
// finish without waiting for it; abort (shutdown) cancels it and waits.
func (w *webhook) stop(abort bool) {
	close(w.done)
	if abort {
		w.cancel()
		w.m.bus.Unsubscribe(w.id)
		return
	}
	go func() {
		w.m.bus.Unsubscribe(w.id)
		w.cancel()
	}()
}

func (w *webhook) stopped() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// handle delivers one event; it runs on the webhook's own bus goroutine, so
// deliveries are serial.
func (w *webhook) handle(e Event) {
	if w.stopped() {
		return
	}
	name, log := w.cfg.Name, w.m.logger
	if w.open {
		// While open, the first event after each interval is the probe: a
		// single POST without retries.
		if time.Now().Before(w.nextProbe) {
			log.Debug("webhook circuit breaker open, event dropped", "webhook", name, "event", string(e.Type))
			return
		}
		if r := w.post(e); r.ok {
			w.open, w.failures = false, 0
			w.openState.Store(false)
			log.Info("webhook circuit breaker closed for " + name)
		} else {
			w.nextProbe = time.Now().Add(webhookBreakerInterval)
			log.Debug("webhook probe failed", "webhook", name, "error", r.err)
		}
		return
	}
	for attempt := 0; ; attempt++ {
		r := w.post(e)
		if r.ok {
			w.failures = 0
			return
		}
		if w.stopped() {
			return
		}
		if !r.retryable || attempt >= w.cfg.MaxRetries {
			log.Error("webhook delivery failed permanently for "+name, "error", r.err)
			w.failed()
			return
		}
		log.Warn(fmt.Sprintf("webhook delivery failed for %s: %s, retrying", name, r.err))
		select {
		case <-time.After(retryDelay(attempt, r.retryAfter)):
		case <-w.done:
			return
		}
	}
}

func (w *webhook) failed() {
	w.failures++
	if w.failures >= breakerThreshold && !w.open {
		w.open = true
		w.openState.Store(true)
		w.nextProbe = time.Now().Add(webhookBreakerInterval)
		w.m.logger.Info("webhook circuit breaker open for " + w.cfg.Name)
	}
}

type postResult struct {
	ok         bool
	retryable  bool
	retryAfter time.Duration
	err        string // the cause or the status; never the URL or headers
}

func (w *webhook) post(e Event) postResult {
	body, err := json.Marshal(w.m.payload(w.cfg, e))
	if err != nil {
		return postResult{err: err.Error()}
	}
	ctx, cancel := context.WithTimeout(w.ctx, w.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.URL, bytes.NewReader(body))
	if err != nil {
		// The error would quote the URL.
		return postResult{err: "invalid request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kahi-webhook/1.0")
	for k, v := range w.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := w.m.client.Do(req)
	if err != nil {
		// A *url.Error formats the request URL; log only its cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return postResult{retryable: true, err: err.Error()}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	r := postResult{err: "status " + strconv.Itoa(resp.StatusCode)}
	switch c := resp.StatusCode; {
	case c >= 200 && c < 300:
		r.ok = true
	case c == http.StatusRequestTimeout || c == http.StatusTooManyRequests:
		r.retryable = true
		r.retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	case c >= 500:
		r.retryable = true
	}
	return r
}

// retryDelay is min(base*2^attempt, cap), or Retry-After when longer (capped
// at its own cap), with +/-10% jitter.
func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	d := webhookBackoffBase
	for i := 0; i < attempt && d < webhookBackoffCap; i++ {
		d *= 2
	}
	d = max(min(d, webhookBackoffCap), min(retryAfter, webhookRetryAfterCap))
	return time.Duration(float64(d) * (0.9 + 0.2*rand.Float64()))
}

// parseRetryAfter reads Retry-After in seconds or HTTP-date form.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

// payload builds the POST body for the webhook's template (FUNC-057).
func (m *WebhookManager) payload(c WebhookConfig, e Event) any {
	name, group := e.Data["name"], e.Data["group"]
	switch c.Template {
	case "slack":
		return map[string]string{"text": m.summary(e)}
	case "pagerduty":
		return map[string]any{
			"routing_key":  c.RoutingKey,
			"event_action": "trigger",
			"dedup_key":    "kahi-" + m.hostname + "-" + subject(e),
			"payload": map[string]string{
				"summary":  m.summary(e),
				"source":   m.hostname,
				"severity": "critical",
			},
		}
	default:
		details := make(map[string]string, len(e.Data))
		for k, v := range e.Data {
			if k != "name" && k != "group" {
				details[k] = v
			}
		}
		return map[string]any{
			"event":     string(e.Type),
			"process":   name,
			"group":     group,
			"timestamp": e.Timestamp.UTC().Format(time.RFC3339),
			"details":   details,
		}
	}
}

// summary is the Slack text, also the PagerDuty summary.
func (m *WebhookManager) summary(e Event) string {
	if e.Type == ProcessStateFatal {
		return fmt.Sprintf("Process %s entered FATAL state on %s", subject(e), m.hostname)
	}
	return fmt.Sprintf("%s for %s on %s", e.Type, subject(e), m.hostname)
}

// subject is what an event is about: the process, else its group, else the
// supervisor itself.
func subject(e Event) string {
	if n := e.Data["name"]; n != "" {
		return n
	}
	if g := e.Data["group"]; g != "" {
		return g
	}
	return "kahi"
}
