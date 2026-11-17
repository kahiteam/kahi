package ctl

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/api"
	"golang.org/x/sys/unix"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// attachServer upgrades and hands the connection to session.
func attachServer(t *testing.T, session func(conn net.Conn, in *bufio.Reader)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/processes/gone/attach" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"process gone: already attached"}`)
			return
		}
		if r.Header.Get("Upgrade") != api.AttachUpgrade {
			w.WriteHeader(400)
			return
		}
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: kahi-attach\r\nConnection: Upgrade\r\n\r\n")
		session(conn, brw.Reader)
	}))
	t.Cleanup(srv.Close)
	return NewTCPClient(strings.TrimPrefix(srv.URL, "http://"), "u", "p")
}

func TestAttachErrorBeforeUpgrade(t *testing.T) {
	c := attachServer(t, func(net.Conn, *bufio.Reader) {})
	_, err := c.Attach(context.Background(), "gone", strings.NewReader(""), io.Discard, io.Discard, AttachOptions{})
	if err == nil || err.Error() != "process gone: already attached" {
		t.Fatalf("err %v", err)
	}
	if _, err := NewTCPClient("127.0.0.1:1", "", "").Attach(context.Background(), "x", nil, io.Discard, io.Discard, AttachOptions{}); err == nil {
		t.Fatal("dial error expected")
	}
}

// Input is forwarded as it arrives; stdout and stderr frames go to their
// writers; an exit frame ends the session.
func TestAttachStreamsAndExit(t *testing.T) {
	got := make(chan string, 1)
	c := attachServer(t, func(conn net.Conn, in *bufio.Reader) {
		typ, data, err := api.ReadFrame(in)
		if err != nil || typ != api.FrameStdin {
			t.Errorf("frame %d %v", typ, err)
		}
		got <- string(data)
		_ = api.WriteFrame(conn, api.FrameStdout, []byte("OUT\n"))
		_ = api.WriteFrame(conn, api.FrameStderr, []byte("ERR\n"))
		_ = api.WriteFrame(conn, api.FrameExited, []byte(`{"name":"p"}`))
		time.Sleep(100 * time.Millisecond)
		_ = conn.Close()
	})
	var out, errb lockedBuf
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	attached := false
	done := make(chan struct{})
	var end AttachEnd
	var err error
	go func() {
		end, err = c.Attach(context.Background(), "p", pr, &out, &errb, AttachOptions{OnAttached: func() { attached = true }})
		close(done)
	}()
	_, _ = pw.Write([]byte("line\n"))
	if s := <-got; s != "line\n" {
		t.Errorf("input %q", s)
	}
	<-done
	if err != nil || end != AttachExited || !attached {
		t.Fatalf("end %v err %v attached %v", end, err, attached)
	}
	if out.String() != "OUT\n" || errb.String() != "ERR\n" {
		t.Errorf("stdout %q stderr %q", out.String(), errb.String())
	}
}

// On a terminal Ctrl+] detaches; bytes before it are sent, 0x03 is data.
func TestAttachDetachKey(t *testing.T) {
	got := make(chan string, 1)
	c := attachServer(t, func(conn net.Conn, in *bufio.Reader) {
		_, data, _ := api.ReadFrame(in)
		got <- string(data)
		_, _ = io.Copy(io.Discard, in)
	})
	end, err := c.Attach(context.Background(), "p", strings.NewReader("a\x03b\x1dafter"), io.Discard, io.Discard, AttachOptions{TTY: true})
	if err != nil || end != AttachDetached {
		t.Fatalf("end %v err %v", end, err)
	}
	if s := <-got; s != "a\x03b" {
		t.Errorf("sent %q", s)
	}
}

// Without a terminal 0x1d is data; after the input ends the session lasts
// until AttachIdle passes without output.
func TestAttachIdleAfterEOF(t *testing.T) {
	old := AttachIdle
	AttachIdle = 100 * time.Millisecond
	t.Cleanup(func() { AttachIdle = old })
	got := make(chan string, 1)
	c := attachServer(t, func(conn net.Conn, in *bufio.Reader) {
		_, data, _ := api.ReadFrame(in)
		got <- string(data)
		for i := 0; i < 3; i++ {
			time.Sleep(50 * time.Millisecond)
			_ = api.WriteFrame(conn, api.FrameStdout, []byte("tick\n"))
		}
		_, _ = io.Copy(io.Discard, in)
	})
	var out lockedBuf
	start := time.Now()
	end, err := c.Attach(context.Background(), "p", strings.NewReader("x\x1dy"), &out, io.Discard, AttachOptions{})
	if err != nil || end != AttachIdleAfterEOF {
		t.Fatalf("end %v err %v", end, err)
	}
	if s := <-got; s != "x\x1dy" {
		t.Errorf("sent %q", s)
	}
	if strings.Count(out.String(), "tick") != 3 || time.Since(start) < 200*time.Millisecond {
		t.Errorf("output %q after %v: the idle timer must restart on output", out.String(), time.Since(start))
	}
}

func TestAttachLostAndInterrupted(t *testing.T) {
	c := attachServer(t, func(conn net.Conn, _ *bufio.Reader) { _ = conn.Close() })
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	if _, err := c.Attach(context.Background(), "p", pr, io.Discard, io.Discard, AttachOptions{}); err == nil || !strings.HasPrefix(err.Error(), "attach to p lost: ") {
		t.Fatalf("err %v", err)
	}
	c2 := attachServer(t, func(_ net.Conn, in *bufio.Reader) { _, _ = io.Copy(io.Discard, in) })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	pr2, pw2 := io.Pipe()
	defer func() { _ = pw2.Close() }()
	if end, err := c2.Attach(ctx, "p", pr2, io.Discard, io.Discard, AttachOptions{TTY: true}); err != nil || end != AttachInterrupted {
		t.Fatalf("end %v err %v", end, err)
	}
}

type fakeTerm struct {
	t   unix.Termios
	set []unix.Termios
}

func (f *fakeTerm) Get() (*unix.Termios, error) { c := f.t; return &c, nil }
func (f *fakeTerm) Set(t *unix.Termios) error   { f.t = *t; f.set = append(f.set, *t); return nil }

// Only the input side goes raw; OPOST stays; restore brings back the old
// state (ADR-044).
func TestRawInput(t *testing.T) {
	f := &fakeTerm{}
	f.t.Lflag = unix.ICANON | unix.ECHO | unix.ISIG | unix.IEXTEN
	f.t.Oflag = unix.OPOST
	orig := f.t
	restore, err := RawInput(f)
	if err != nil {
		t.Fatal(err)
	}
	if f.t.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 || f.t.Lflag&unix.IEXTEN == 0 {
		t.Errorf("lflag %x", f.t.Lflag)
	}
	if f.t.Oflag&unix.OPOST == 0 || f.t.Cc[unix.VMIN] != 1 {
		t.Errorf("oflag %x vmin %d", f.t.Oflag, f.t.Cc[unix.VMIN])
	}
	restore()
	if f.t != orig {
		t.Error("not restored")
	}
}
