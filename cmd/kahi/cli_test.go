package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCommandHelp(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, sub := range []string{"daemon", "ctl", "migrate", "version", "init", "hash-password", "completion"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing subcommand %q", sub)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, want := range []string{"kahi", "commit:", "built:", "go:", "os/arch:", "fips:"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q", want)
		}
	}
}

func TestUnknownSubcommand(t *testing.T) {
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"nonexistent"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestDaemonCommand(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"daemon"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestCtlCommand(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"ctl"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCommand(t *testing.T) {
	// Create a temp supervisord.conf to migrate.
	dir := t.TempDir()
	conf := filepath.Join(dir, "supervisord.conf")
	if err := os.WriteFile(conf, []byte("[program:web]\ncommand = /bin/echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"migrate", conf})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "[programs.web]") {
		t.Error("migrate output should contain [programs.web]")
	}
}

func TestMigrateCommandNoArgs(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"migrate"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for migrate with no args")
	}
}

func TestMigrateCommandNonexistent(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"migrate", "/nonexistent/file.conf"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestHashPasswordCommand(t *testing.T) {
	// Pipe a password via stdin.
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	go func() {
		_, _ = w.Write([]byte("testpassword\n"))
		w.Close()
	}()

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"hash-password"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	output := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(output, "$2") {
		t.Fatalf("expected bcrypt hash starting with $2, got: %s", output)
	}
}

func TestCompletionBash(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"completion", "bash"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionZsh(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"completion", "zsh"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionFish(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"completion", "fish"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionPowershell(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"completion", "powershell"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestInitCommandStdout(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "[supervisor]") {
		t.Error("init stdout should contain TOML config")
	}
}

func TestInitCommandWriteFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "kahi.toml")

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init", "-o", out})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[supervisor]") {
		t.Error("written file should contain TOML config")
	}
}

func TestInitCommandNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(out, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "-o", out})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when file exists without --force")
	}
}

func TestInitCommandForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(out, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"init", "-o", out, "--force"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "existing") {
		t.Error("file should have been overwritten")
	}
}
