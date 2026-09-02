package logging

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RotationConfig configures size-based log rotation.
type RotationConfig struct {
	Maxbytes string // e.g., "50MB", "0" means unlimited
	Backups  int    // number of backup files to keep
}

// RotateIfNeeded checks the file size and rotates if necessary.
func RotateIfNeeded(path string, cfg RotationConfig) error {
	maxBytes := ParseSize(cfg.Maxbytes)
	if maxBytes == 0 {
		return nil // unlimited
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil // file doesn't exist yet
	}

	if info.Size() < int64(maxBytes) {
		return nil // not yet at max
	}

	return rotateFile(path, cfg.Backups)
}

func rotateFile(path string, backups int) error {
	if backups == 0 {
		// Truncate the file.
		return os.Truncate(path, 0)
	}

	// Rotate: .N-1 -> .N, ... , .1 -> .2, file -> .1
	// Remove the oldest backup.
	oldest := fmt.Sprintf("%s.%d", path, backups)
	_ = os.Remove(oldest)

	// Shift existing backups (missing intermediates are expected).
	for i := backups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", path, i)
		dst := fmt.Sprintf("%s.%d", path, i+1)
		_ = os.Rename(src, dst)
	}

	// Rename current file to .1.
	return os.Rename(path, path+".1")
}

// ParseSize parses a human-readable size string to bytes.
// Supports B, KB, MB, GB suffixes. Defaults to bytes if no suffix.
func ParseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}

	s = strings.ToUpper(s)
	var multiplier int64 = 1

	switch {
	case strings.HasSuffix(s, "GB"):
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}

	s = strings.TrimSpace(s)
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return val * multiplier
}

// CleanupStaleLogs removes stale log files on daemon startup.
func CleanupStaleLogs(logDir string, patterns []string) error {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		// Remove the log file itself if it exists and is empty.
		info, err := os.Stat(pattern)
		if err != nil {
			continue
		}
		if info.Size() == 0 {
			_ = os.Remove(pattern)
		}
	}
	return nil
}
