package logging

import (
	"errors"
	"fmt"
	"log/slog"
	"log/syslog"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Syslog forwarding of process output (FUNC-020, ADR-050).

// syslogMaxLine is the longest message body; longer lines are split.
const syslogMaxLine = 2048

// syslogQueue is how many lines wait per connection; the oldest is dropped
// when it is full.
const syslogQueue = 1000

// Reconnect backoff bounds; tests shorten them.
var (
	syslogBackoffMin = time.Second
	syslogBackoffMax = 60 * time.Second
)

var syslogFacilities = map[string]syslog.Priority{
	"KERN": syslog.LOG_KERN, "USER": syslog.LOG_USER, "MAIL": syslog.LOG_MAIL,
	"DAEMON": syslog.LOG_DAEMON, "AUTH": syslog.LOG_AUTH, "SYSLOG": syslog.LOG_SYSLOG,
	"LPR": syslog.LOG_LPR, "NEWS": syslog.LOG_NEWS, "UUCP": syslog.LOG_UUCP,
	"CRON": syslog.LOG_CRON, "AUTHPRIV": syslog.LOG_AUTHPRIV, "FTP": syslog.LOG_FTP,
	"LOCAL0": syslog.LOG_LOCAL0, "LOCAL1": syslog.LOG_LOCAL1, "LOCAL2": syslog.LOG_LOCAL2,
	"LOCAL3": syslog.LOG_LOCAL3, "LOCAL4": syslog.LOG_LOCAL4, "LOCAL5": syslog.LOG_LOCAL5,
	"LOCAL6": syslog.LOG_LOCAL6, "LOCAL7": syslog.LOG_LOCAL7,
}

var syslogSeverities = map[string]syslog.Priority{
	"EMERG": syslog.LOG_EMERG, "ALERT": syslog.LOG_ALERT, "CRIT": syslog.LOG_CRIT,
	"ERR": syslog.LOG_ERR, "WARNING": syslog.LOG_WARNING, "NOTICE": syslog.LOG_NOTICE,
	"INFO": syslog.LOG_INFO, "DEBUG": syslog.LOG_DEBUG,
}

// ValidSyslogFacility reports whether name ("" = LOCAL0) is a facility.
func ValidSyslogFacility(name string) bool {
	_, ok := syslogFacilities[strings.ToUpper(orDefault(name, "LOCAL0"))]
	return ok
}

// ValidSyslogPriority reports whether name ("" = INFO) is a severity.
func ValidSyslogPriority(name string) bool {
	_, ok := syslogSeverities[strings.ToUpper(orDefault(name, "INFO"))]
	return ok
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ParseSyslogAddress splits a syslog_address: "" is the local syslog
// socket; unix:///path, udp://host:port and tcp://host:port name one.
func ParseSyslogAddress(s string) (network, addr string, err error) {
	if s == "" {
		return "", "", nil
	}
	scheme, rest, ok := strings.Cut(s, "://")
	switch {
	case ok && scheme == "unix" && strings.HasPrefix(rest, "/"):
		return "unix", rest, nil
	case ok && (scheme == "udp" || scheme == "tcp"):
		if _, _, err := net.SplitHostPort(rest); err == nil {
			return scheme, rest, nil
		}
	}
	return "", "", fmt.Errorf("invalid address %s", s)
}

// SyslogConfig configures one forwarded stream of one process.
type SyslogConfig struct {
	Address  string // a valid syslog_address
	Facility string // "" = LOCAL0
	Priority string // "" = INFO
	Process  string // the tag
	Stream   string // stdout or stderr, for the log messages
	Logger   *slog.Logger
}

// SyslogSink forwards lines to syslog, one message per line, on its own
// goroutine with a bounded queue, so a syslog outage never blocks or fails
// the process. A failed dial or write starts an outage: queued and new lines
// are dropped, the connection is retried with a 1..60 s backoff, one WARN is
// logged per outage and one INFO on recovery.
type SyslogSink struct {
	cfg  SyslogConfig
	pri  syslog.Priority
	dial func() (*syslogConn, error)

	mu     sync.Mutex
	cond   *sync.Cond
	queue  []string
	closed bool
	stop   chan struct{}
	done   chan struct{}
}

// NewSyslogSink starts forwarding; the first dial happens on the sink's own
// goroutine.
func NewSyslogSink(cfg SyslogConfig) *SyslogSink {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	pri := syslogFacilities[strings.ToUpper(orDefault(cfg.Facility, "LOCAL0"))] |
		syslogSeverities[strings.ToUpper(orDefault(cfg.Priority, "INFO"))]
	s := &SyslogSink{cfg: cfg, pri: pri, stop: make(chan struct{}), done: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	network, addr, _ := ParseSyslogAddress(cfg.Address)
	s.dial = func() (*syslogConn, error) { return dialSyslog(network, addr, pri, cfg.Process) }
	go s.run()
	return s
}

// Send queues one line (without its newline); it never blocks.
func (s *SyslogSink) Send(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(s.queue) >= syslogQueue {
		s.queue = s.queue[1:]
	}
	s.queue = append(s.queue, string(line))
	s.cond.Signal()
}

// Close stops the sink; queued lines are discarded. It does not wait for a
// dial or write in progress (each is bounded by a 5 s timeout), so a caller
// holding a lock is never stalled by an unreachable syslog.
func (s *SyslogSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.stop)
	s.cond.Broadcast()
}

// next waits for the next line; false when the sink is closed.
func (s *SyslogSink) next() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		return "", false
	}
	line := s.queue[0]
	s.queue = s.queue[1:]
	return line, true
}

func (s *SyslogSink) dropQueued() {
	s.mu.Lock()
	s.queue = nil
	s.mu.Unlock()
}

func (s *SyslogSink) run() {
	defer close(s.done)
	var w *syslogConn
	defer func() {
		if w != nil {
			_ = w.conn.Close()
		}
	}()
	outage := false
	backoff := syslogBackoffMin
	fail := func(err error) bool {
		if !outage {
			outage = true
			s.cfg.Logger.Warn(fmt.Sprintf("syslog connection failed for %s %s: %v", s.cfg.Process, s.cfg.Stream, err))
		}
		s.dropQueued()
		select {
		case <-s.stop:
			return false
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, syslogBackoffMax)
		s.dropQueued() // lines written while disconnected are dropped
		return true
	}
	for {
		if w == nil {
			var err error
			if w, err = s.dial(); err != nil {
				w = nil
				if !fail(err) {
					return
				}
				continue
			}
			if outage {
				outage = false
				s.cfg.Logger.Info(fmt.Sprintf("syslog connection restored for %s %s", s.cfg.Process, s.cfg.Stream))
			}
			backoff = syslogBackoffMin
		}
		line, ok := s.next()
		if !ok {
			return
		}
		if err := w.writeSplit(line); err != nil {
			_ = w.conn.Close()
			w = nil
			if !fail(err) {
				return
			}
		}
	}
}

// syslogConn writes RFC 3164 messages in log/syslog's format: the local
// form ("<PRI>Stamp tag[pid]: msg") on Unix sockets, with an RFC 3339 time
// and the hostname over the network, each ending in a newline. It is its own
// writer, not log/syslog's, so the datagram send buffer can be raised: macOS
// refuses Unix datagrams over 2048 bytes by default.
type syslogConn struct {
	conn     net.Conn
	local    bool
	hostname string
	tag      string
	pri      syslog.Priority
}

// localSyslogPaths are the local syslog sockets, as log/syslog tries them.
var localSyslogPaths = []string{"/dev/log", "/var/run/syslog", "/var/run/log"}

func dialSyslog(network, addr string, pri syslog.Priority, tag string) (*syslogConn, error) {
	c := &syslogConn{tag: tag, pri: pri}
	var err error
	switch network {
	case "":
		c.local = true
		for _, path := range localSyslogPaths {
			if c.conn, err = dialUnix(path); err == nil {
				return c, nil
			}
		}
		return nil, fmt.Errorf("no local syslog socket: %w", err)
	case "unix":
		c.local = true
		if c.conn, err = dialUnix(addr); err != nil {
			return nil, err
		}
		return c, nil
	default:
		if c.conn, err = net.DialTimeout(network, addr, 5*time.Second); err != nil {
			return nil, err
		}
		c.hostname, _ = os.Hostname()
		return c, nil
	}
}

// dialUnix tries a datagram socket, as syslogd listens, then a stream one.
func dialUnix(path string) (net.Conn, error) {
	if c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"}); err == nil {
		_ = c.SetWriteBuffer(64 << 10)
		return c, nil
	}
	return net.DialTimeout("unix", path, 5*time.Second)
}

func (c *syslogConn) write(msg string) error {
	var line string
	if c.local {
		line = fmt.Sprintf("<%d>%s %s[%d]: %s\n", c.pri, time.Now().Format(time.Stamp), c.tag, os.Getpid(), msg)
	} else {
		line = fmt.Sprintf("<%d>%s %s %s[%d]: %s\n", c.pri, time.Now().Format(time.RFC3339), c.hostname, c.tag, os.Getpid(), msg)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Write([]byte(line))
	// A Unix datagram socket whose reader is behind answers ENOBUFS: the
	// connection is fine, the reader needs a moment. Wait for it (up to
	// about a second) rather than starting an outage.
	for i := 0; i < 100 && errors.Is(err, syscall.ENOBUFS); i++ {
		time.Sleep(10 * time.Millisecond)
		_, err = c.conn.Write([]byte(line))
	}
	return err
}

// writeSplit writes line as messages of at most syslogMaxLine bytes.
func (c *syslogConn) writeSplit(line string) error {
	for {
		chunk := line
		if len(chunk) > syslogMaxLine {
			chunk = line[:syslogMaxLine]
		}
		if err := c.write(chunk); err != nil {
			return err
		}
		line = line[len(chunk):]
		if line == "" {
			return nil
		}
	}
}
