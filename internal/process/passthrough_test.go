package process

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// lines decodes the JSON lines in buf as "stream:log" pairs.
func decodeLines(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var v struct{ Process, Stream, Log string }
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		out = append(out, v.Process+"/"+v.Stream+":"+v.Log)
	}
	return out
}

func TestPassthroughHandlerRouting(t *testing.T) {
	for _, tc := range []struct {
		name             string
		cfg              config.ProgramConfig
		wantOut, wantErr []string
		wantNilHandler   bool
	}{
		{
			name:    "no log files",
			cfg:     config.ProgramConfig{},
			wantOut: []string{"web/stdout:o"},
			wantErr: []string{"web/stderr:e"},
		},
		{
			name:    "stdout to a file",
			cfg:     config.ProgramConfig{StdoutLogfile: "/tmp/x.log"},
			wantErr: []string{"web/stderr:e"},
		},
		{
			name:    "redirect_stderr",
			cfg:     config.ProgramConfig{RedirectStderr: true},
			wantOut: []string{"web/stdout:o", "web/stdout:e"},
		},
		{
			name:           "both to files",
			cfg:            config.ProgramConfig{StdoutLogfile: "/tmp/o.log", StderrLogfile: "/tmp/e.log"},
			wantNilHandler: true,
		},
		{
			name:    "strip_ansi",
			cfg:     config.ProgramConfig{StripAnsi: true, StderrLogfile: "/tmp/e.log"},
			wantOut: []string{"web/stdout:o"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			m := NewManager(nil, nil, nil)
			m.SetOutput(&out, &errb)
			h := m.passthroughHandler(tc.cfg)
			if tc.wantNilHandler {
				if h != nil {
					t.Fatal("handler set, want nil when every stream has a log file")
				}
				return
			}
			stdoutLine := "o"
			if tc.cfg.StripAnsi {
				stdoutLine = "\x1b[31mo\x1b[0m"
			}
			h("web", "stdout", []byte(stdoutLine))
			h("web", "stderr", []byte("e"))
			if got := decodeLines(t, &out); strings.Join(got, ",") != strings.Join(tc.wantOut, ",") {
				t.Errorf("stdout = %q, want %q", got, tc.wantOut)
			}
			if got := decodeLines(t, &errb); strings.Join(got, ",") != strings.Join(tc.wantErr, ",") {
				t.Errorf("stderr = %q, want %q", got, tc.wantErr)
			}
		})
	}
}

// Without output writers nothing is forwarded.
func TestPassthroughHandlerWithoutOutput(t *testing.T) {
	if h := NewManager(nil, nil, nil).passthroughHandler(config.ProgramConfig{}); h != nil {
		t.Fatal("handler set without output writers")
	}
}
