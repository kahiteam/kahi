package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// Ring buffer sizes for *_capture_maxbytes (FUNC-086).
const (
	DefaultBufferSize = 64 << 10
	MinBufferSize     = 1 << 10
	MaxBufferSize     = 16 << 20
)

// ParseBufferSize parses a *_capture_maxbytes value, which takes the
// *_logfile_maxbytes syntax; unset ("") returns 0, the default (FUNC-086).
func ParseBufferSize(s string) (int, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	n, err := parseSize(s)
	if err != nil {
		return 0, fmt.Errorf("invalid size %s", s)
	}
	if n < MinBufferSize || n > MaxBufferSize {
		return 0, errors.New("must be between 1KB and 16MB")
	}
	return int(n), nil
}

// bufferSize returns the ring buffer size for a configured size; unset (0)
// means the default.
func bufferSize(n int) int {
	if n <= 0 {
		return DefaultBufferSize
	}
	return n
}

// CaptureConfig configures process output capture.
type CaptureConfig struct {
	ProcessName    string
	Stream         string // "stdout" or "stderr"
	Logfile        string // path, empty for container mode
	RedirectStderr bool
	StripAnsi      bool
	MaxBytes       string // max file size before rotation (e.g. "10KB")
	BufferSize     int    // ring buffer size in bytes; 0 means DefaultBufferSize
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
	stripper ANSIStripper

	// total counts the bytes captured (after ANSI stripping) since the
	// writer was created; generation identifies the writer, so an offset
	// from an earlier process of the same name is recognised (ADR-033).
	total      int64
	generation string

	// rotateRetryAt is when a rotation that failed may be tried again; a
	// failed rename is not retried, and logged, on every write (#299).
	rotateRetryAt time.Time
}

// generations makes each writer's generation unique even when two are
// created in the same nanosecond.
var generations atomic.Uint64

// NewCaptureWriter creates a capture writer for a process stream. If the log
// file cannot be opened it returns the error together with a writer that
// keeps only the ring buffer; EnsureOpen retries the file (#171).
func NewCaptureWriter(cfg CaptureConfig) (*CaptureWriter, error) {
	cw := &CaptureWriter{
		config:     cfg,
		ringBuf:    NewRingBuffer(bufferSize(cfg.BufferSize)),
		generation: strconv.FormatUint(uint64(time.Now().UnixNano())+generations.Add(1), 10),
	}
	if err := cw.EnsureOpen(); err != nil {
		return cw, fmt.Errorf("cannot open log file: %w", err)
	}
	return cw, nil
}

// EnsureOpen opens the configured log file if it is not open yet. The error
// names the path.
func (cw *CaptureWriter) EnsureOpen() error {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if cw.config.Logfile == "" || cw.file != nil {
		return nil
	}
	f, err := openLogFile(cw.config.Logfile, 0644)
	if err != nil {
		return fmt.Errorf("%s: %w", cw.config.Logfile, err)
	}
	cw.file = f
	return nil
}

// Write implements io.Writer.
func (cw *CaptureWriter) Write(p []byte) (int, error) {
	cw.Capture(p)
	return len(p), nil
}

// Capture writes p like Write and returns the data as captured, with ANSI
// sequences stripped when strip_ansi is set, so the event stream carries the
// same output as the log file (#186).
func (cw *CaptureWriter) Capture(p []byte) []byte {
	return cw.CaptureAndPublish(p, nil)
}

// CaptureAndPublish is Capture that also calls publish, while still holding
// the writer's lock, with the captured data and its stream id
// "{generation}-{end offset}" (ADR-033). Holding the lock keeps ids in
// offset order when two pipes write the same stream; publish must not call
// back into the writer.
func (cw *CaptureWriter) CaptureAndPublish(p []byte, publish func(data []byte, id string)) []byte {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	data := p
	if cw.config.StripAnsi {
		data = cw.stripper.Strip(data)
	}

	// Write to ring buffer.
	cw.ringBuf.Write(data)

	// Write to file if configured, rotating first when this write would
	// take the file past maxbytes (FUNC-019).
	if cw.file != nil {
		cw.rotateIfNeeded(int64(len(data)))
	}
	if cw.file != nil {
		if _, err := cw.file.Write(data); err != nil {
			if cw.config.Logger != nil {
				cw.config.Logger.Error("log write failed", "file", cw.config.Logfile, "error", err)
			}
		}
	}

	// Call handlers.
	for _, h := range cw.handlers {
		h(cw.config.ProcessName, data)
	}

	cw.total += int64(len(data))
	if publish != nil && len(data) > 0 {
		publish(data, cw.idLocked(cw.total))
	}
	return data
}

// Generation returns the writer's generation, the first part of its ids.
func (cw *CaptureWriter) Generation() string {
	return cw.generation
}

func (cw *CaptureWriter) idLocked(end int64) string {
	return cw.generation + "-" + strconv.FormatInt(end, 10)
}

// ReadFrom returns what the ring buffer still holds after offset, the offset
// that data starts at (later than offset when bytes were overwritten), and
// the total captured so far. An offset beyond the total returns nothing.
func (cw *CaptureWriter) ReadFrom(offset int64) (data []byte, start, total int64) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	oldest := cw.total - int64(cw.ringBuf.Len())
	start = max(offset, oldest)
	if start >= cw.total {
		return nil, cw.total, cw.total
	}
	return cw.ringBuf.Read(int(cw.total - start)), start, cw.total
}

// AddHandler adds a callback for captured data.
func (cw *CaptureWriter) AddHandler(h func(name string, data []byte)) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.handlers = append(cw.handlers, h)
}

// ReadTail returns the last n bytes from the ring buffer.
func (cw *CaptureWriter) ReadTail(n int) []byte {
	data, _ := cw.Tail(n)
	return data
}

// Tail returns the last n bytes from the ring buffer and whether they start
// after the beginning of the captured output, where the first line may be
// cut (FUNC-079).
func (cw *CaptureWriter) Tail(n int) (data []byte, truncated bool) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	data = cw.ringBuf.Read(n)
	return data, int64(len(data)) < cw.total
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

// rotateRetry is how long a failed rotation waits before it is tried again.
var rotateRetry = time.Minute

// rotateIfNeeded rotates the log file before a write of n bytes that would
// take it past MaxBytes (FUNC-019: rotate first, then write). An empty file
// is not rotated, so a single write larger than MaxBytes still goes into a
// file of its own. Must be called with mu held.
func (cw *CaptureWriter) rotateIfNeeded(n int64) {
	if cw.file == nil || cw.config.MaxBytes == "" {
		return
	}
	maxBytes := ParseSize(cw.config.MaxBytes)
	if maxBytes == 0 {
		return
	}
	info, err := cw.file.Stat()
	if err != nil || info.Size() == 0 || info.Size()+n <= maxBytes {
		return
	}
	if time.Now().Before(cw.rotateRetryAt) {
		return
	}
	// With no backups to keep, empty the file in place. Truncating the open
	// handle, not the path, means a symlink swapped in for the log file
	// cannot redirect the truncate to another file (SEC-012/019).
	if cw.config.Backups == 0 {
		_ = cw.file.Truncate(0)
		return
	}
	// Close current file before rotating. If the rename fails the reopen
	// below opens the same file again, so writing goes on there (#187).
	_ = cw.file.Close()
	if err := rotateFile(cw.config.Logfile, cw.config.Backups); err != nil {
		cw.rotateRetryAt = time.Now().Add(rotateRetry)
		cw.logError(fmt.Sprintf("log rotation failed; writing on to the current file, next try in %s", rotateRetry), err)
	} else {
		cw.rotateRetryAt = time.Time{}
	}
	f, err := openLogFile(cw.config.Logfile, 0644)
	if err != nil {
		// The next start retries the file (EnsureOpen).
		cw.logError("cannot reopen log file after rotation; output goes to the in-memory buffer only", err)
		cw.file = nil
		return
	}
	cw.file = f
}

func (cw *CaptureWriter) logError(msg string, err error) {
	if cw.config.Logger != nil {
		cw.config.Logger.Error(msg, "process", cw.config.ProcessName, "file", cw.config.Logfile, "error", err)
	}
}

// Reopen opens the log file path again and switches to it (for log rotation
// tools). On failure it returns the error and keeps the old file.
func (cw *CaptureWriter) Reopen() error {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.config.Logfile == "" {
		return nil
	}

	// Open the new file before closing the old one: if the open fails, output
	// keeps going to the old file and the next SIGUSR2 retries (#230). A
	// writer that lost its file (a failed reopen after rotation) gets it
	// back here too, not only at the next start (#299).
	f, err := openLogFile(cw.config.Logfile, 0644)
	if err != nil {
		return fmt.Errorf("cannot reopen log file: %s: %w", cw.config.Logfile, err)
	}
	if cw.file != nil {
		_ = cw.file.Close()
	}
	cw.file = f
	cw.rotateRetryAt = time.Time{}
	return nil
}

// jsonLine is one line of process output in container mode (FUNC-018). The
// field order matches the documented format.
type jsonLine struct {
	Time    string `json:"time"`
	Process string `json:"process"`
	Stream  string `json:"stream"`
	Log     string `json:"log"`
}

// FormatJSONLine formats a line of process output as a JSON log entry:
// {"time":"<RFC3339>","process":"<name>","stream":"<stream>","log":"<line>"}.
func FormatJSONLine(processName, stream, line string) []byte {
	data, _ := json.Marshal(jsonLine{
		Time:    time.Now().Format(time.RFC3339),
		Process: processName,
		Stream:  stream,
		Log:     line,
	})
	return append(data, '\n')
}
