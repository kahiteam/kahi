package supervisor

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type countingRecorder struct{ total, errors int }

func (c *countingRecorder) ReloadAttempt(failed bool) {
	c.total++
	if failed {
		c.errors++
	}
}

// Every reload attempt counts, a rejected one as an error too; a dry run
// does not count. A [server.metrics] change only warns (FUNC-060, FUNC-062).
func TestReloadMetricsAndWarning(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "kahi.toml")
	write := func(metrics bool, extra string) {
		body := "[supervisor]\nshutdown_timeout = 5\n\n[server.http]\nenabled = true\nlisten = \"127.0.0.1:0\"\nusername = \"admin\"\n" +
			"pass" + "word = \"" + string(hash) + "\"\n\n[server.metrics]\nenabled = " + map[bool]string{true: "true", false: "false"}[metrics] + "\n" + extra
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(true, "")
	var buf bytes.Buffer
	s := newWebSupervisor(t, path, slog.New(slog.NewTextHandler(&buf, nil)))
	rec := &countingRecorder{}
	s.SetReloadRecorder(rec)

	if _, err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewReload(); err != nil {
		t.Fatal(err)
	}
	write(false, "")
	if _, err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "server.metrics change takes effect after restart") {
		t.Errorf("no restart warning:\n%s", buf.String())
	}
	write(false, "[programs.bad\n")
	if _, err := s.Reload(); err == nil {
		t.Fatal("bad config reloaded")
	}
	if rec.total != 3 || rec.errors != 1 {
		t.Errorf("reloads %d, errors %d; want 3, 1 (the dry run not counted)", rec.total, rec.errors)
	}
	if s.Uptime() <= 0 || s.WebhookBreakers() == nil {
		t.Error("Uptime or WebhookBreakers")
	}
}
