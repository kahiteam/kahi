package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstanceNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    ProgramConfig
		want []string
	}{
		{"single", ProgramConfig{Numprocs: 1}, []string{"web"}},
		{"single with process_name", ProgramConfig{Numprocs: 1, ProcessName: "front"}, []string{"front"}},
		{"template", ProgramConfig{Numprocs: 2, ProcessName: "w-%(process_num)d"}, []string{"w-0", "w-1"}},
		{"start offset", ProgramConfig{Numprocs: 2, NumprocsStart: 10, ProcessName: "w-%(process_num)d"}, []string{"w-10", "w-11"}},
		{"auto names", ProgramConfig{Numprocs: 2}, []string{"web_0", "web_1"}},
		{"all variables", ProgramConfig{Numprocs: 2, ProcessName: "%(program_name)s-%(group_name)s-%(process_num)d-of-%(numprocs)d"}, []string{"web-web-0-of-2", "web-web-1-of-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := InstanceNames("web", tc.p); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("InstanceNames = %v, want %v", got, tc.want)
			}
		})
	}
}

// loadConfig writes main (and any extra files) to a temp dir and loads it
// with includes, as the daemon does.
func loadConfig(t *testing.T, main string, files map[string]string) error {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadWithIncludes(path)
	return err
}

func TestValidateProcessNames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		main  string
		files map[string]string
		want  string // "" means the config is valid
	}{
		{
			name: "numprocs with fixed process_name",
			main: "[programs.w]\ncommand = \"/bin/true\"\nnumprocs = 2\nprocess_name = \"w\"\n",
			want: `programs.w: process_name "w" must contain %(process_num)d when numprocs > 1`,
		},
		{
			name: "two programs share a process_name",
			main: "[programs.a]\ncommand = \"/bin/true\"\nprocess_name = \"x\"\n\n[programs.b]\ncommand = \"/bin/true\"\nprocess_name = \"x\"\n",
			want: "duplicate process name: x (programs.a and programs.b)",
		},
		{
			name: "process_name collides with another program",
			main: "[programs.a]\ncommand = \"/bin/true\"\nprocess_name = \"b\"\n\n[programs.b]\ncommand = \"/bin/true\"\n",
			want: "duplicate process name: b (programs.a and programs.b)",
		},
		{
			name: "instances collide with another program",
			main: "[programs.w]\ncommand = \"/bin/true\"\nnumprocs = 2\n\n[programs.x]\ncommand = \"/bin/true\"\nprocess_name = \"w_1\"\n",
			want: "duplicate process name: w_1 (programs.w and programs.x)",
		},
		{
			name:  "collision with an included program",
			main:  "include = [\"more.toml\"]\n\n[programs.a]\ncommand = \"/bin/true\"\nprocess_name = \"x\"\n",
			files: map[string]string{"more.toml": "[programs.b]\ncommand = \"/bin/true\"\nprocess_name = \"x\"\n"},
			want:  "duplicate process name: x (programs.a and programs.b)",
		},
		{
			name: "auto-named instances",
			main: "[programs.w]\ncommand = \"/bin/true\"\nnumprocs = 3\n",
		},
		{
			name: "templated instances of two programs",
			main: "[programs.a]\ncommand = \"/bin/true\"\nnumprocs = 2\nprocess_name = \"%(program_name)s-%(process_num)d\"\n\n[programs.b]\ncommand = \"/bin/true\"\nnumprocs = 2\nprocess_name = \"%(program_name)s-%(process_num)d\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadConfig(t, tc.main, tc.files)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
