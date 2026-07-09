package config

import (
	"net/url"
	"strings"
)

// RedactMask is the fixed placeholder substituted for secret values in the
// sanitized config view returned by the API.
const RedactMask = "***"

// Sanitized returns a copy of c safe for exposure via the config API. It masks
// every secret value while preserving structure and non-secret fields:
//
//   - HTTPServerConfig.Password and Username carry json:"-" and never serialize.
//   - Each program's Environment value is replaced with RedactMask (keys kept).
//   - Each webhook's Headers value is replaced with RedactMask (keys kept).
//   - Userinfo (user:password@) is stripped from each webhook URL.
//
// Non-secret fields (program names, commands, numprocs, listen addresses) pass
// through unchanged so the endpoint stays useful. The original config is not
// mutated.
//
// NOTE: adding a new secret-bearing config field requires updating this
// function; that friction is intentional so secrets cannot silently leak.
func Sanitized(c *Config) *Config {
	if c == nil {
		return nil
	}

	out := *c

	if c.Programs != nil {
		programs := make(map[string]ProgramConfig, len(c.Programs))
		for name, p := range c.Programs {
			p.Environment = maskValues(p.Environment)
			programs[name] = p
		}
		out.Programs = programs
	}

	if c.Webhooks != nil {
		webhooks := make(map[string]WebhookConfig, len(c.Webhooks))
		for name, w := range c.Webhooks {
			w.Headers = maskValues(w.Headers)
			w.URL = stripURLUserinfo(w.URL)
			webhooks[name] = w
		}
		out.Webhooks = webhooks
	}

	return &out
}

// maskValues returns a copy of m with every value replaced by RedactMask. Keys
// are preserved so operators can still see which variables or headers are set.
func maskValues(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	masked := make(map[string]string, len(m))
	for k := range m {
		masked[k] = RedactMask
	}
	return masked
}

// stripURLUserinfo removes any user:password@ component from raw. If raw cannot
// be parsed but contains an '@', the entire value is masked rather than risk
// leaking embedded credentials.
func stripURLUserinfo(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "@") {
			return RedactMask
		}
		return raw
	}
	if u.User != nil {
		u.User = nil
	}
	return u.String()
}
