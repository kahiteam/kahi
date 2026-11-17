package logging

import (
	"bytes"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type logSink struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// listenGram binds a datagram socket and returns its path and the messages
// it receives, with the one trailing newline log/syslog adds removed.
func listenGram(t *testing.T, path string) <-chan string {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch := make(chan string, 100)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			ch <- strings.TrimSuffix(string(buf[:n]), "\n")
		}
	}()
	return ch
}

func sockDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "ks-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no syslog message")
	}
	return ""
}

func TestParseSyslogAddressAndNames(t *testing.T) {
	for in, want := range map[string]string{"": "|", "unix:///dev/log": "unix|/dev/log", "udp://127.0.0.1:514": "udp|127.0.0.1:514", "tcp://h:6514": "tcp|h:6514"} {
		n, a, err := ParseSyslogAddress(in)
		if err != nil || n+"|"+a != want {
			t.Errorf("%q: %s|%s %v", in, n, a, err)
		}
	}
	for _, bad := range []string{"ftp://x", "unix://rel", "udp://nohost", "/dev/log"} {
		if _, _, err := ParseSyslogAddress(bad); err == nil || err.Error() != "invalid address "+bad {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if !ValidSyslogFacility("") || !ValidSyslogFacility("local3") || ValidSyslogFacility("BOGUS") {
		t.Error("facility names")
	}
	if !ValidSyslogPriority("") || !ValidSyslogPriority("notice") || ValidSyslogPriority("BOGUS") {
		t.Error("priority names")
	}
}

func TestSyslogSinkDeliversAndSplits(t *testing.T) {
	path := filepath.Join(sockDir(t), "log.sock")
	ch := listenGram(t, path)
	s := NewSyslogSink(SyslogConfig{Address: "unix://" + path, Facility: "LOCAL3", Priority: "NOTICE", Process: "web", Stream: "stdout"})
	defer s.Close()
	s.Send([]byte("hello"))
	m := recv(t, ch)
	if !strings.HasPrefix(m, "<157>") || !strings.Contains(m, " web[") || !strings.HasSuffix(m, ": hello") {
		t.Fatalf("message %q", m)
	}
	s.Send([]byte(strings.Repeat("x", 5000)))
	var sizes []int
	for range 3 {
		m := recv(t, ch)
		sizes = append(sizes, len(m[strings.Index(m, "]: ")+3:]))
	}
	if sizes[0] != 2048 || sizes[1] != 2048 || sizes[2] != 904 {
		t.Errorf("chunks %v", sizes)
	}
}

// An outage logs one WARN, drops lines, and recovers with one INFO.
func TestSyslogSinkOutageAndRecovery(t *testing.T) {
	oldMin, oldMax := syslogBackoffMin, syslogBackoffMax
	syslogBackoffMin, syslogBackoffMax = 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { syslogBackoffMin, syslogBackoffMax = oldMin, oldMax })
	path := filepath.Join(sockDir(t), "log.sock")
	logs := &logSink{}
	s := NewSyslogSink(SyslogConfig{Address: "unix://" + path, Process: "p", Stream: "stdout", Logger: slog.New(slog.NewTextHandler(logs, nil))})
	defer s.Close()
	for i := 0; i < 10; i++ {
		s.Send([]byte("lost"))
		time.Sleep(20 * time.Millisecond)
	}
	if n := strings.Count(logs.String(), "syslog connection failed for p stdout"); n != 1 {
		t.Fatalf("%d failure warnings:\n%s", n, logs.String())
	}
	ch := listenGram(t, path)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "syslog connection restored for p stdout") {
		if time.Now().After(deadline) {
			t.Fatalf("not restored:\n%s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Send([]byte("back"))
	if m := recv(t, ch); !strings.HasSuffix(m, ": back") {
		t.Fatalf("after recovery %q (lines from the outage must be dropped)", m)
	}
}

// A unix stream listener is reached through the fallback.
func TestSyslogSinkUnixStreamFallback(t *testing.T) {
	path := filepath.Join(sockDir(t), "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 1024)
		n, _ := c.Read(buf)
		got <- string(buf[:n])
	}()
	s := NewSyslogSink(SyslogConfig{Address: "unix://" + path, Process: "p", Stream: "stdout"})
	defer s.Close()
	s.Send([]byte("hello"))
	select {
	case m := <-got:
		if !strings.HasPrefix(m, "<134>") || !strings.Contains(m, "hello") {
			t.Errorf("message %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing over the stream socket")
	}
}

// Send never blocks, even with a full queue and nothing to dial.
func TestSyslogSinkQueueBounded(t *testing.T) {
	s := NewSyslogSink(SyslogConfig{Address: "unix:///nonexistent/kahi-test.sock", Process: "p", Stream: "stdout"})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5000; i++ {
			s.Send([]byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Send blocked")
	}
	s.Close()
	s.Close()
	s.Send([]byte("after close"))
}
