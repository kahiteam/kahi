package events

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func webhookLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWebhookDelivery(t *testing.T) {
	var received atomic.Bool
	var receivedBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		received.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	bus := NewBus(webhookLogger())
	wm := NewWebhookManager(bus, []WebhookConfig{
		{
			Name:   "test",
			URL:    ts.URL,
			Events: []EventType{ProcessStateFatal},
		},
	}, webhookLogger())
	defer wm.Stop()

	bus.Publish(Event{
		Type: ProcessStateFatal,
		Data: map[string]string{"name": "web", "group": "web"},
	})

	// Wait for async delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if received.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !received.Load() {
		t.Fatal("webhook not delivered")
	}

	// Verify the body is valid JSON.
	var payload map[string]any
	if err := json.Unmarshal([]byte(receivedBody), &payload); err != nil {
		t.Fatalf("invalid JSON payload: %s", receivedBody)
	}
	if payload["event"] != "PROCESS_STATE_FATAL" {
		t.Fatalf("expected event PROCESS_STATE_FATAL, got %v", payload["event"])
	}
}

func TestWebhookRetryOnFailure(t *testing.T) {
	var attempts atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	bus := NewBus(webhookLogger())
	wm := NewWebhookManager(bus, []WebhookConfig{
		{
			Name:       "retry-test",
			URL:        ts.URL,
			Events:     []EventType{ProcessStateFatal},
			MaxRetries: 5,
			Timeout:    time.Second,
		},
	}, webhookLogger())
	defer wm.Stop()

	bus.Publish(Event{
		Type: ProcessStateFatal,
		Data: map[string]string{"name": "web"},
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if attempts.Load() < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts.Load())
	}
}

func TestWebhookCircuitBreaker(t *testing.T) {
	var attempts atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	bus := NewBus(webhookLogger())
	wm := NewWebhookManager(bus, []WebhookConfig{
		{
			Name:       "cb-test",
			URL:        ts.URL,
			Events:     []EventType{ProcessStateFatal},
			MaxRetries: 1,
			Timeout:    time.Second,
		},
	}, webhookLogger())
	defer wm.Stop()

	// Send enough events to trip the circuit breaker (5 consecutive failures).
	for range 6 {
		bus.Publish(Event{
			Type: ProcessStateFatal,
			Data: map[string]string{"name": "web"},
		})
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	// After circuit breaker trips, attempts should stop.
	before := attempts.Load()
	bus.Publish(Event{
		Type: ProcessStateFatal,
		Data: map[string]string{"name": "web"},
	})
	time.Sleep(200 * time.Millisecond)
	after := attempts.Load()

	if after != before {
		t.Fatalf("circuit breaker should have stopped delivery, attempts: %d -> %d", before, after)
	}
}

func TestWebhookSlackTemplate(t *testing.T) {
	payload := buildPayload("slack", Event{
		Type:      ProcessStateFatal,
		Timestamp: time.Now(),
		Data:      map[string]string{"name": "web"},
	})

	var body map[string]string
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if body["text"] == "" {
		t.Fatal("expected text field in Slack payload")
	}
	if !strings.Contains(body["text"], "PROCESS_STATE_FATAL") {
		t.Fatalf("expected event type in text, got: %s", body["text"])
	}
}

func TestWebhookPagerDutyTemplate(t *testing.T) {
	payload := buildPayload("pagerduty", Event{
		Type:      ProcessStateFatal,
		Timestamp: time.Now(),
		Data:      map[string]string{"name": "web"},
	})

	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if body["event_action"] != "trigger" {
		t.Fatalf("expected event_action=trigger, got %v", body["event_action"])
	}
	pd := body["payload"].(map[string]any)
	if pd["severity"] != "critical" {
		t.Fatalf("expected severity=critical for FATAL, got %v", pd["severity"])
	}
}

func TestWebhookGenericTemplate(t *testing.T) {
	payload := buildPayload("generic", Event{
		Type:      ProcessStateRunning,
		Timestamp: time.Now(),
		Data:      map[string]string{"name": "web", "group": "web"},
	})

	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if body["event"] != "PROCESS_STATE_RUNNING" {
		t.Fatalf("expected event field, got %v", body["event"])
	}
	if body["process"] != "web" {
		t.Fatalf("expected process=web, got %v", body["process"])
	}
}

func TestWebhookUnknownTemplate(t *testing.T) {
	// Unknown template falls back to generic.
	payload := buildPayload("unknown", Event{
		Type: ProcessStateRunning,
		Data: map[string]string{"name": "web"},
	})

	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if body["event"] != "PROCESS_STATE_RUNNING" {
		t.Fatal("expected generic format for unknown template")
	}
}

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		url           string
		allowInsecure bool
		wantErr       bool
	}{
		{"https://hooks.slack.com/services/xxx", false, false},
		{"http://hooks.slack.com/services/xxx", false, true},
		{"http://hooks.slack.com/services/xxx", true, false},
		{"http://localhost:8080/webhook", false, false},
		{"http://127.0.0.1:8080/webhook", false, false},
		{"not-a-url", false, true},
		{"", false, true},
	}

	for _, tc := range tests {
		err := ValidateWebhookURL(tc.url, tc.allowInsecure)
		if tc.wantErr && err == nil {
			t.Errorf("ValidateWebhookURL(%q, %v): expected error", tc.url, tc.allowInsecure)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ValidateWebhookURL(%q, %v): unexpected error: %v", tc.url, tc.allowInsecure, err)
		}
	}
}

func TestExpandWebhookEnv(t *testing.T) {
	// Override lookupEnv for testing.
	origLookup := lookupEnv
	defer func() { lookupEnv = origLookup }()

	lookupEnv = func(key string) (string, bool) {
		env := map[string]string{
			"SLACK_URL": "https://hooks.slack.com/xxx",
			"API_TOKEN": "secret123",
		}
		v, ok := env[key]
		return v, ok
	}

	result, err := ExpandWebhookEnv("${SLACK_URL}")
	if err != nil {
		t.Fatal(err)
	}
	if result != "https://hooks.slack.com/xxx" {
		t.Fatalf("expected expanded URL, got %s", result)
	}

	result, err = ExpandWebhookEnv("Bearer ${API_TOKEN}")
	if err != nil {
		t.Fatal(err)
	}
	if result != "Bearer secret123" {
		t.Fatalf("expected expanded header, got %s", result)
	}

	_, err = ExpandWebhookEnv("${UNDEFINED_VAR}")
	if err == nil {
		t.Fatal("expected error for undefined var")
	}

	_, err = ExpandWebhookEnv("${UNCLOSED")
	if err == nil {
		t.Fatal("expected error for unclosed ${}")
	}
}

func TestWebhookNoMatchingEvent(t *testing.T) {
	var received atomic.Bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	bus := NewBus(webhookLogger())
	wm := NewWebhookManager(bus, []WebhookConfig{
		{
			Name:   "selective",
			URL:    ts.URL,
			Events: []EventType{ProcessStateFatal},
		},
	}, webhookLogger())
	defer wm.Stop()

	// Publish a non-matching event.
	bus.Publish(Event{
		Type: ProcessStateRunning,
		Data: map[string]string{"name": "web"},
	})

	time.Sleep(200 * time.Millisecond)
	if received.Load() {
		t.Fatal("webhook should not fire for non-matching event")
	}
}

func TestWebhookMultipleEvents(t *testing.T) {
	var count atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	bus := NewBus(webhookLogger())
	wm := NewWebhookManager(bus, []WebhookConfig{
		{
			Name:   "multi",
			URL:    ts.URL,
			Events: []EventType{ProcessStateFatal, ProcessStateExited},
		},
	}, webhookLogger())
	defer wm.Stop()

	bus.Publish(Event{Type: ProcessStateFatal, Data: map[string]string{"name": "a"}})
	bus.Publish(Event{Type: ProcessStateExited, Data: map[string]string{"name": "b"}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count.Load() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if count.Load() < 2 {
		t.Fatalf("expected 2 deliveries, got %d", count.Load())
	}
}

func TestWebhookHeaders(t *testing.T) {
	var receivedAuth atomic.Value

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	bus := NewBus(webhookLogger())
	wm := NewWebhookManager(bus, []WebhookConfig{
		{
			Name:    "auth-test",
			URL:     ts.URL,
			Events:  []EventType{ProcessStateFatal},
			Headers: map[string]string{"Authorization": "Bearer token123"},
		},
	}, webhookLogger())
	defer wm.Stop()

	bus.Publish(Event{Type: ProcessStateFatal, Data: map[string]string{"name": "web"}})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := receivedAuth.Load(); v != nil && v.(string) != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := ""
	if v := receivedAuth.Load(); v != nil {
		got = v.(string)
	}
	if got != "Bearer token123" {
		t.Fatalf("expected Authorization header, got %q", got)
	}
}

func TestWebhookTimeout(t *testing.T) {
	bus := NewBus(webhookLogger())
	wm := NewWebhookManager(bus, []WebhookConfig{
		{
			Name:    "timeout",
			URL:     "http://192.0.2.1/webhook", // Non-routable address.
			Events:  []EventType{ProcessStateFatal},
			Timeout: 100 * time.Millisecond,
		},
	}, webhookLogger())
	defer wm.Stop()

	// Verify default timeout is 5s when not configured.
	bus2 := NewBus(webhookLogger())
	wm2 := NewWebhookManager(bus2, []WebhookConfig{
		{Name: "default-timeout", URL: "https://example.com", Events: []EventType{ProcessStateFatal}},
	}, webhookLogger())
	defer wm2.Stop()

	if wm2.hooks[0].cfg.Timeout != 5*time.Second {
		t.Fatalf("expected default 5s timeout, got %v", wm2.hooks[0].cfg.Timeout)
	}
}

func TestPagerDutySeverity(t *testing.T) {
	tests := []struct {
		event    EventType
		expected string
	}{
		{ProcessStateFatal, "critical"},
		{ProcessStateExited, "error"},
		{ProcessStateBackoff, "warning"},
		{ProcessStateRunning, "info"},
		{SupervisorStateRunning, "info"},
	}

	for _, tc := range tests {
		if got := pagerDutySeverity(tc.event); got != tc.expected {
			t.Errorf("pagerDutySeverity(%s): expected %s, got %s", tc.event, tc.expected, got)
		}
	}
}
