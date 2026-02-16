package migrate

import (
	"strings"
	"testing"
)

func TestParseINIAllSectionTypes(t *testing.T) {
	input := `[supervisord]
logfile = /var/log/supervisord.log

[program:web]
command = /usr/bin/python app.py

[group:services]
programs = web,api

[eventlistener:memmon]
command = memmon

[fcgi-program:php]
command = /usr/bin/php-cgi
socket = unix:///tmp/php.sock

[include]
files = /etc/supervisor/conf.d/*.conf

[unix_http_server]
file = /var/run/supervisor.sock

[inet_http_server]
port = 127.0.0.1:9001
`
	ini, err := ParseINI(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ini.Warnings) > 0 {
		t.Errorf("unexpected warnings: %v", ini.Warnings)
	}

	expected := map[string]string{
		"supervisord":      "",
		"program":          "web",
		"group":            "services",
		"eventlistener":    "memmon",
		"fcgi-program":     "php",
		"include":          "",
		"unix_http_server": "",
		"inet_http_server": "",
	}

	if len(ini.Sections) != len(expected) {
		t.Fatalf("got %d sections, want %d", len(ini.Sections), len(expected))
	}

	for _, sec := range ini.Sections {
		wantName, ok := expected[sec.Type]
		if !ok {
			t.Errorf("unexpected section type %q", sec.Type)
			continue
		}
		if sec.Name != wantName {
			t.Errorf("section %q: name = %q, want %q", sec.Type, sec.Name, wantName)
		}
	}
}

func TestParseINIInlineComments(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python app.py ; this is a comment
autostart = true ; start on boot
`
	ini, err := ParseINI(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec := ini.Sections[0]
	if sec.Options["command"] != "/usr/bin/python app.py" {
		t.Errorf("command = %q, want stripped of comment", sec.Options["command"])
	}
	if sec.Options["autostart"] != "true" {
		t.Errorf("autostart = %q, want true", sec.Options["autostart"])
	}
}

func TestParseINIContinuationLines(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python
  app.py
  --port 8080
`
	ini, err := ParseINI(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sec := ini.Sections[0]
	want := "/usr/bin/python app.py --port 8080"
	if sec.Options["command"] != want {
		t.Errorf("command = %q, want %q", sec.Options["command"], want)
	}
}

func TestParseINIMalformed(t *testing.T) {
	input := `[program:web]
this is not a valid line
`
	_, err := ParseINI(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse error at line 2") {
		t.Errorf("error = %q, want mention of line 2", err.Error())
	}
}

func TestParseINIUnknownSectionType(t *testing.T) {
	input := `[rpcinterface:supervisor]
supervisor.rpcinterface_factory = supervisor.rpcinterface:make_main_rpcinterface
`
	ini, err := ParseINI(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ini.Warnings) == 0 {
		t.Fatal("expected warning for unknown section type")
	}
	if !strings.Contains(ini.Warnings[0], "unknown section type: rpcinterface") {
		t.Errorf("warning = %q", ini.Warnings[0])
	}
}

func TestExpandSupervisordVars(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"%(ENV_HOME)s/app", "${HOME}/app"},
		{"%(ENV_PORT)s", "${PORT}"},
		{"%(here)s/conf.d", "${here}/conf.d"},
		{"no variables here", "no variables here"},
	}
	for _, tt := range tests {
		got := expandSupervisordVars(tt.input)
		if got != tt.want {
			t.Errorf("expandSupervisordVars(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseBool(t *testing.T) {
	truths := []string{"true", "True", "TRUE", "yes", "Yes", "on", "ON", "1"}
	for _, v := range truths {
		got, err := ParseBool(v)
		if err != nil {
			t.Errorf("ParseBool(%q) error: %v", v, err)
		}
		if !got {
			t.Errorf("ParseBool(%q) = false, want true", v)
		}
	}

	falses := []string{"false", "False", "FALSE", "no", "No", "off", "OFF", "0"}
	for _, v := range falses {
		got, err := ParseBool(v)
		if err != nil {
			t.Errorf("ParseBool(%q) error: %v", v, err)
		}
		if got {
			t.Errorf("ParseBool(%q) = true, want false", v)
		}
	}

	_, err := ParseBool("maybe")
	if err == nil {
		t.Error("ParseBool(maybe) should error")
	}
}
