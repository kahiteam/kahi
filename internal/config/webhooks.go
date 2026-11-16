package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// WebhookTemplates are the built-in payload templates (FUNC-057).
var WebhookTemplates = map[string]bool{"": true, "generic": true, "slack": true, "pagerduty": true}

func webhookNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Webhooks))
	for name := range cfg.Webhooks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// validateWebhooks checks what does not depend on ${VAR} expansion
// (FUNC-056, FUNC-057, FUNC-051).
func validateWebhooks(cfg *Config) []error {
	var errs []error
	for _, name := range webhookNames(cfg) {
		w := cfg.Webhooks[name]
		if w.Timeout < 1 {
			errs = append(errs, fmt.Errorf("webhooks.%s.timeout must be >= 1", name))
		}
		if w.MaxRetries < 0 {
			errs = append(errs, fmt.Errorf("webhooks.%s.max_retries must be >= 0", name))
		}
		if !WebhookTemplates[w.Template] {
			errs = append(errs, fmt.Errorf("unknown webhook template: %s", w.Template))
		}
		if w.Template == "pagerduty" && w.RoutingKey == "" {
			errs = append(errs, fmt.Errorf("webhooks.%s: template pagerduty requires routing_key", name))
		}
		for _, e := range w.Events {
			// One POST per output chunk would flood the receiver.
			if strings.HasPrefix(e, "PROCESS_LOG") {
				errs = append(errs, fmt.Errorf("webhooks.%s.events: PROCESS_LOG events cannot be sent to webhooks", name))
				break
			}
		}
	}
	return errs
}

// validateWebhookURLs checks the expanded URLs (FUNC-059): http(s) with a
// host, HTTPS unless allow_insecure is set or the host is loopback. The URL
// itself is never printed: it often carries the credential.
func validateWebhookURLs(cfg *Config) []error {
	var errs []error
	for _, name := range webhookNames(cfg) {
		w := cfg.Webhooks[name]
		u, err := url.Parse(w.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Hostname() == "" {
			errs = append(errs, fmt.Errorf("invalid webhook URL: %s", name))
			continue
		}
		if u.Scheme == "http" && !w.AllowInsecure && !LoopbackHost(u.Hostname()) {
			errs = append(errs, fmt.Errorf("webhooks.%s: URL must use HTTPS (set allow_insecure = true to override)", name))
		}
	}
	return errs
}

// LoopbackHost reports a host exempt from the TLS requirement.
func LoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// webhookWarnings flags the deprecated retries key and plain HTTP to a
// non-loopback host.
func webhookWarnings(cfg *Config) []string {
	var warnings []string
	for _, name := range webhookNames(cfg) {
		w := cfg.Webhooks[name]
		if w.RetriesSet {
			warnings = append(warnings, fmt.Sprintf("webhooks.%s.retries is deprecated; use max_retries", name))
		}
		if u, err := url.Parse(w.URL); err == nil && u.Scheme == "http" && w.AllowInsecure && !LoopbackHost(u.Hostname()) {
			warnings = append(warnings, fmt.Sprintf("webhooks.%s: allow_insecure set, sending over plain HTTP", name))
		}
	}
	return warnings
}

// expandWebhooks expands ${VAR} in url, header values and routing_key at
// load time (FUNC-058); errors carry the key path.
func expandWebhooks(cfg *Config, ctx ExpandContext) error {
	for _, name := range webhookNames(cfg) {
		w := cfg.Webhooks[name]
		var err error
		if w.URL, err = expandString(w.URL, ctx); err != nil {
			return fmt.Errorf("webhooks.%s.url: %w", name, err)
		}
		if w.RoutingKey, err = expandString(w.RoutingKey, ctx); err != nil {
			return fmt.Errorf("webhooks.%s.routing_key: %w", name, err)
		}
		if w.Headers != nil {
			headers := make(map[string]string, len(w.Headers))
			for k, v := range w.Headers {
				if headers[k], err = expandString(v, ctx); err != nil {
					return fmt.Errorf("webhooks.%s.headers.%s: %w", name, k, err)
				}
			}
			w.Headers = headers
		}
		cfg.Webhooks[name] = w
	}
	return nil
}

// redactWebhookURL keeps the scheme and host and masks the rest: a
// Slack-style URL carries its secret in the path, others in the userinfo or
// query. An unparseable URL is masked entirely.
func redactWebhookURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return RedactMask
	}
	return u.Scheme + "://" + u.Host + "/" + RedactMask
}
