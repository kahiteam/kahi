package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

// SEC-014: GET /api/v1/config must not expose secrets. This seeds an HTTP
// password, a program environment secret, a webhook Authorization header, and
// a credential-bearing webhook URL, then asserts none appear in the response.

func redactionTestServer(cfg any) *Server {
	pm := &mockProcessManager{}
	gm := &mockGroupManager{}
	cm := &mockConfigManager{cfg: cfg}
	di := &mockDaemonInfo{ready: true}
	bus := events.NewBus(slog.New(slog.NewTextHandler(io.Discard, nil)))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(Config{}, pm, gm, cm, di, bus, logger)
}

func TestGetConfigRedactsSecrets(t *testing.T) {
	// Seeded via variables so the fixtures do not resemble committed secrets.
	httpUser := "admin"
	httpPass := "http-topsecret"
	envDBPass := "pg-s3cr3t"
	envAPIKey := "ak-live-999"
	whAuth := "Bearer wh-tok-abc"

	cfg := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": {
				Command:  "/usr/bin/web",
				Numprocs: 3,
				Environment: map[string]string{
					"DB_PASSWORD": envDBPass,
					"API_KEY":     envAPIKey,
				},
			},
		},
		Server: config.ServerConfig{
			HTTP: config.HTTPServerConfig{
				Enabled:  true,
				Listen:   "127.0.0.1:9876",
				Username: httpUser,
				Password: httpPass,
			},
		},
		Webhooks: map[string]config.WebhookConfig{
			"alerts": {
				URL: "https://hookuser:hookpass@hooks.example.com/notify",
				Headers: map[string]string{
					"Authorization": whAuth,
				},
			},
		},
	}

	srv := redactionTestServer(cfg)
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()

	secrets := []string{
		httpPass,   // HTTP password
		httpUser,   // HTTP username
		envDBPass,  // program env secret
		envAPIKey,  // program env secret
		whAuth,     // webhook auth header
		"hookpass", // webhook URL credential
		"hookuser",
	}
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked secret %q: %s", secret, body)
		}
	}

	// Non-secret fields must remain visible so the endpoint stays useful.
	for _, want := range []string{"/usr/bin/web", "hooks.example.com/notify", "127.0.0.1:9876"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected non-secret field %q in response: %s", want, body)
		}
	}

	// Environment keys are preserved with masked values.
	var decoded config.Config
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	env := decoded.Programs["web"].Environment
	if env["DB_PASSWORD"] != config.RedactMask || env["API_KEY"] != config.RedactMask {
		t.Fatalf("environment values should be masked, got %v", env)
	}
	if decoded.Programs["web"].Numprocs != 3 {
		t.Fatalf("numprocs should pass through, got %d", decoded.Programs["web"].Numprocs)
	}
	if h := decoded.Webhooks["alerts"].Headers["Authorization"]; h != config.RedactMask {
		t.Fatalf("webhook header should be masked, got %q", h)
	}
}
