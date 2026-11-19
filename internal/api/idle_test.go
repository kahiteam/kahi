package api

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A kept-alive connection that sends nothing after an unauthenticated
// /healthz is closed after idleTimeout instead of being held forever.
func TestTCPIdleKeepAliveClosed(t *testing.T) {
	original := idleTimeout
	idleTimeout = 200 * time.Millisecond
	defer func() { idleTimeout = original }()

	srv, _, _ := testServer()
	srv.authUser = "admin"
	srv.authPass = testPassHash
	if err := srv.StartTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()

	conn, err := net.Dial("tcp", srv.TCPAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("GET /healthz HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, err = io.ReadAll(br)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("idle keep-alive connection held for 5s")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("closed after %v, want about 200ms", elapsed)
	}
}

// A 401 closes the connection, so a client that trickles a request body
// cannot keep it open without credentials.
func TestTCPUnauthorizedClosesConnection(t *testing.T) {
	srv, _, _ := testServer()
	srv.authUser = "admin"
	srv.authPass = testPassHash
	if err := srv.StartTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()

	conn, err := net.Dial("tcp", srv.TCPAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("POST /api/v1/config/reload HTTP/1.1\r\nHost: x\r\nContent-Length: 200000\r\n\r\nabc")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !resp.Close {
		t.Fatalf("status %d, close %v", resp.StatusCode, resp.Close)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(br); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatal("connection kept open after the 401 while the body trickles")
		}
	}
}
