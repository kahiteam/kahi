package logging

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"
)

// logFileOpenFlags are the flags used to open process and daemon log files.
// O_NOFOLLOW refuses to open the file when its final path component is a
// symlink. On unix platforms openLogFile additionally walks every parent
// component with O_NOFOLLOW (SEC-019 / ADR-022) so a symlink swapped into a
// parent directory cannot redirect (a possibly privileged) daemon's log writes
// to an attacker-chosen file.
const logFileOpenFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NOFOLLOW

// CaptureConfig configures process output capture.
type CaptureConfig struct {
	ProcessName    string
	Stream         string // "stdout" or "stderr"
	Logfile        string // path, empty for container mode
	RedirectStderr bool
	StripAnsi      bool
	MaxBytes       string // max file size before rotation (e.g. "10KB")
	Backups        int    // number of rotated backup files to keep
	Logger         *slog.Logger
}

// CaptureWriter captures process output and routes it to configured destinations.
type CaptureWriter struct {
	mu       sync.Mutex
	config   CaptureConfig
	file     *os.File
	handlers []func(name string, data []byte)
	ringBuf  *RingBuffer
}

// NewCaptureWriter creates a capture writer for a process stream.
func NewCaptureWriter(cfg CaptureConfig) (*CaptureWriter, error) {
	cw := &CaptureWriter{
		config:  cfg,
		ringBuf: NewRingBuffer(64 * 1024), // 64KB ring buffer
	}

	if cfg.Logfile != "" {
		f, err := openLogFile(cfg.Logfile, 0644)
		if err != nil {
			return nil, fmt.Errorf("cannot open log file: %s: %w", cfg.Logfile, err)
		}
		cw.file = f
	}

	return cw, nil
}

// Write implements io.Writer.
func (cw *CaptureWriter) Write(p []byte) (int, error) {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	data := p
	if cw.config.StripAnsi {
		data = StripANSI(data)
	}

	// Write to ring buffer.
	cw.ringBuf.Write(data)

	// Write to file if configured.
	if cw.file != nil {
		if _, err := cw.file.Write(data); err != nil {
			if cw.config.Logger != nil {
				cw.config.Logger.Error("log write failed", "file", cw.config.Logfile, "error", err)
			}
		}
		cw.rotateIfNeeded()
	}

	// Call handlers.
	for _, h := range cw.handlers {
		h(cw.config.ProcessName, data)
	}

	return len(p), nil
}

// AddHandler adds a callback for captured data.
func (cw *CaptureWriter) AddHandler(h func(name string, data []byte)) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.handlers = append(cw.handlers, h)
}

// ReadTail returns the last n bytes from the ring buffer.
func (cw *CaptureWriter) ReadTail(n int) []byte {
	return cw.ringBuf.Read(n)
}

// Close closes the log file if open.
func (cw *CaptureWriter) Close() error {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if cw.file != nil {
		return cw.file.Close()
	}
	return nil
}

// rotateIfNeeded checks if the log file exceeds MaxBytes and rotates it.
// Must be called with mu held.
func (cw *CaptureWriter) rotateIfNeeded() {
	if cw.file == nil || cw.config.MaxBytes == "" {
		return
	}
	maxBytes := ParseSize(cw.config.MaxBytes)
	if maxBytes == 0 {
		return
	}
	info, err := cw.file.Stat()
	if err != nil || info.Size() < maxBytes {
		return
	}
	// Close current file before rotating.
	cw.file.Close()
	_ = rotateFile(cw.config.Logfile, cw.config.Backups)
	// Reopen a fresh file.
	f, err := openLogFile(cw.config.Logfile, 0644)
	if err != nil {
		cw.file = nil
		return
	}
	cw.file = f
}

// Reopen closes and reopens the log file (for log rotation tools).
func (cw *CaptureWriter) Reopen() error {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.file == nil || cw.config.Logfile == "" {
		return nil
	}

	cw.file.Close()
	f, err := openLogFile(cw.config.Logfile, 0644)
	if err != nil {
		return fmt.Errorf("cannot reopen log file: %s: %w", cw.config.Logfile, err)
	}
	cw.file = f
	return nil
}

// FormatJSONLine formats a line of process output as a JSON log entry.
func FormatJSONLine(processName, stream, line string) []byte {
	entry := map[string]string{
		"time":    time.Now().Format(time.RFC3339),
		"process": processName,
		"stream":  stream,
		"log":     line,
	}
	data, _ := json.Marshal(entry)
	return append(data, '\n')
}

// PipeToWriter reads from a pipe and writes to a CaptureWriter,
// formatting as JSON lines for container stdout mode.
func PipeToWriter(r io.ReadCloser, cw *CaptureWriter, containerStdout io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Write raw to capture writer (file + ring buffer + handlers).
		lineBytes := []byte(line + "\n")
		_, _ = cw.Write(lineBytes)

		// Write formatted JSON to container stdout if no file configured.
		if containerStdout != nil && cw.config.Logfile == "" {
			jsonLine := FormatJSONLine(cw.config.ProcessName, cw.config.Stream, line)
			_, _ = containerStdout.Write(jsonLine)
		}
	}
}
