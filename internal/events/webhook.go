package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// WebhookConfig describes a single webhook destination.
type WebhookConfig struct {
	Name          string
	URL           string
	Events        []EventType
	Headers       map[string]string
	Timeout       time.Duration
	MaxRetries    int
	Template      string // "generic", "slack", "pagerduty"
	AllowInsecure bool
}

// WebhookManager subscribes to events and delivers HTTP POST notifications.
type WebhookManager struct {
	bus    *Bus
	logger *slog.Logger
	hooks  []webhookEntry
	client *http.Client
	mu     sync.Mutex
	subIDs []uint64
}

type webhookEntry struct {
	cfg      WebhookConfig
	failures int
	tripped  bool // circuit breaker open
}

// NewWebhookManager creates a webhook manager and subscribes to events.
func NewWebhookManager(bus *Bus, configs []WebhookConfig, logger *slog.Logger) *WebhookManager {
	wm := &WebhookManager{
		bus:    bus,
		logger: logger,
		client: &http.Client{},
	}

	for _, cfg := range configs {
		if cfg.Timeout == 0 {
			cfg.Timeout = 5 * time.Second
		}
		if cfg.MaxRetries == 0 {
			cfg.MaxRetries = 3
		}
		if cfg.Template == "" {
			cfg.Template = "generic"
		}
		wm.hooks = append(wm.hooks, webhookEntry{cfg: cfg})
	}

	wm.subscribe()
	return wm
}

func (wm *WebhookManager) subscribe() {
	// Collect all unique event types across hooks.
	seen := make(map[EventType]bool)
	for _, h := range wm.hooks {
		for _, et := range h.cfg.Events {
			seen[et] = true
		}
	}

	for et := range seen {
		id := wm.bus.Subscribe(et, func(e Event) {
			wm.dispatch(e)
		})
		wm.subIDs = append(wm.subIDs, id)
	}
}

// Stop unsubscribes from all events.
func (wm *WebhookManager) Stop() {
	for _, id := range wm.subIDs {
		wm.bus.Unsubscribe(id)
	}
}

func (wm *WebhookManager) dispatch(e Event) {
	for i := range wm.hooks {
		h := &wm.hooks[i]
		if !h.matchesEvent(e.Type) {
			continue
		}
		// Deliver asynchronously to avoid blocking the event bus.
		go wm.deliver(h, e)
	}
}

func (h *webhookEntry) matchesEvent(et EventType) bool {
	for _, t := range h.cfg.Events {
		if t == et {
			return true
		}
	}
	return false
}

func (wm *WebhookManager) deliver(h *webhookEntry, e Event) {
	wm.mu.Lock()
	if h.tripped {
		wm.mu.Unlock()
		return
	}
	wm.mu.Unlock()

	payload := buildPayload(h.cfg.Template, e)

	var lastErr error
	for attempt := range h.cfg.MaxRetries {
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * time.Second
			time.Sleep(delay)
		}

		if err := wm.sendHTTP(h, payload); err != nil {
			lastErr = err
			continue
		}

		// Success: reset failures.
		wm.mu.Lock()
		h.failures = 0
		wm.mu.Unlock()
		return
	}

	// All retries exhausted.
	wm.mu.Lock()
	h.failures++
	if h.failures >= 5 {
		h.tripped = true
		wm.logger.Warn("webhook circuit breaker tripped",
			"name", h.cfg.Name, "url", h.cfg.URL)
	}
	wm.mu.Unlock()

	wm.logger.Error("webhook delivery failed",
		"name", h.cfg.Name,
		"url", h.cfg.URL,
		"error", lastErr,
	)
}

func (wm *WebhookManager) sendHTTP(h *webhookEntry, payload []byte) error {
	client := &http.Client{Timeout: h.cfg.Timeout}

	req, err := http.NewRequest("POST", h.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kahi-webhook/1.0")

	for k, v := range h.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// buildPayload generates the JSON body based on template name.
func buildPayload(template string, e Event) []byte {
	var payload any

	switch template {
	case "slack":
		text := fmt.Sprintf("[%s] %s", e.Type, formatEventData(e.Data))
		payload = map[string]string{"text": text}

	case "pagerduty":
		payload = map[string]any{
			"routing_key":  "",
			"event_action": "trigger",
			"payload": map[string]any{
				"summary":   fmt.Sprintf("%s: %s", e.Type, formatEventData(e.Data)),
				"source":    "kahi",
				"severity":  pagerDutySeverity(e.Type),
				"timestamp": e.Timestamp.Format(time.RFC3339),
			},
		}

	default: // "generic"
		payload = map[string]any{
			"event":     string(e.Type),
			"timestamp": e.Timestamp.Format(time.RFC3339),
			"process":   e.Data["name"],
			"group":     e.Data["group"],
			"details":   e.Data,
		}
	}

	data, _ := json.Marshal(payload)
	return data
}

func formatEventData(data map[string]string) string {
	parts := make([]string, 0, len(data))
	for k, v := range data {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, " ")
}

func pagerDutySeverity(et EventType) string {
	switch et {
	case ProcessStateFatal:
		return "critical"
	case ProcessStateExited:
		return "error"
	case ProcessStateBackoff:
		return "warning"
	default:
		return "info"
	}
}

// ValidateWebhookURL checks that a URL is valid and uses HTTPS
// unless allow_insecure is set or it's a localhost URL.
func ValidateWebhookURL(rawURL string, allowInsecure bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}

	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid webhook URL format: %s", rawURL)
	}

	if u.Scheme == "http" {
		host := u.Hostname()
		isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if !isLocal && !allowInsecure {
			return fmt.Errorf("webhook URL must use HTTPS: %s (set allow_insecure=true to override)", rawURL)
		}
	}

	return nil
}

// ExpandWebhookEnv resolves ${VAR} references in a string from environment.
func ExpandWebhookEnv(s string) (string, error) {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.Index(s[i:], "}")
			if end == -1 {
				return "", fmt.Errorf("unclosed ${} in %q", s)
			}
			varName := s[i+2 : i+end]
			val, ok := lookupEnv(varName)
			if !ok {
				return "", fmt.Errorf("undefined environment variable: %s", varName)
			}
			result.WriteString(val)
			i += end + 1
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String(), nil
}

// lookupEnv wraps os.LookupEnv for testability.
var lookupEnv = os.LookupEnv
