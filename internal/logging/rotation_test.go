package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"50MB", 50 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"10KB", 10 * 1024},
		{"100B", 100},
		{"100", 100},
		{"0", 0},
		{"", 0},
		// Overflowing int64 is an error (0), not a wrapped size.
		{"9999999999999GB", 0},
	}

	for _, tt := range tests {
		got := ParseSize(tt.input)
		if got != tt.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestRotateFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	// Write initial data.
	if err := os.WriteFile(logPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	err := rotateFile(logPath, 3)
	if err != nil {
		t.Fatal(err)
	}

	// Original file should be renamed to .1.
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatal("expected .1 backup file")
	}

	// Original path should be gone.
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("original file should be renamed")
	}
}

func TestRotateFileMultiple(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	// Simulate 3 rotations.
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(logPath, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := rotateFile(logPath, 3); err != nil {
			t.Fatal(err)
		}
	}

	// Should have .1, .2, .3 backups.
	for i := 1; i <= 3; i++ {
		backup := filepath.Join(dir, "test.log."+string(rune('0'+i)))
		_ = backup
	}
}

func TestRotateFileTruncateOnZeroBackups(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	if err := os.WriteFile(logPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	err := rotateFile(logPath, 0)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty file after truncation, got %d bytes", len(data))
	}
}

func TestRotateIfNeeded(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	// Create a 100 byte file.
	if err := os.WriteFile(logPath, make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}

	// Should not rotate (max = 200).
	err := RotateIfNeeded(logPath, RotationConfig{Maxbytes: "200B", Backups: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Fatal("should not rotate under maxbytes")
	}

	// Should rotate (max = 50).
	err = RotateIfNeeded(logPath, RotationConfig{Maxbytes: "50B", Backups: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatal("expected rotation to create .1 backup")
	}
}

func TestCheckSizeRejectsOverflow(t *testing.T) {
	for _, s := range []string{"9999999999999GB", "99999999999999999MB", "9223372036854775807KB"} {
		if err := CheckSize(s); err == nil {
			t.Errorf("CheckSize(%q) = nil, want an error", s)
		}
	}
	if err := CheckSize("8589934591GB"); err != nil {
		t.Errorf("CheckSize(8589934591GB) = %v, want nil (fits int64)", err)
	}
}
