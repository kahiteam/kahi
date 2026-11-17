package ctl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kahiteam/kahi/internal/api"
)

// AttachIdle is how long a session whose input ended (not a terminal) keeps
// streaming without output before it ends (FUNC-034). Tests shorten it.
var AttachIdle = time.Second

// DetachKey is Ctrl+] (0x1d), the only detach key on a terminal.
const DetachKey = 0x1d

// AttachEnd is why a session ended without an error.
type AttachEnd int

const (
	AttachDetached     AttachEnd = iota // Ctrl+] on a terminal
	AttachExited                        // the process exited
	AttachIdleAfterEOF                  // input ended, then no output for AttachIdle
	AttachInterrupted                   // ctx was cancelled (a signal to the client)
)

// AttachOptions configure a session.
type AttachOptions struct {
	// TTY: input is a terminal, so Ctrl+] detaches and end of input is not
	// expected; otherwise every byte is forwarded and the idle rule applies.
	TTY bool
	// OnAttached runs once the daemon has accepted the session (the 101),
	// before any output; the CLI prints its banner and enters raw mode there.
	OnAttached func()
}

// Attach opens a kahi ctl attach session (FUNC-034, ADR-044): in goes to the
// process's stdin as it arrives, its stdout and stderr come to stdout and
// stderr. Errors before the session carry the daemon's message ("process
// not running: web", "process web: already attached").
func (c *Client) Attach(ctx context.Context, name string, in io.Reader, stdout, stderr io.Writer, opts AttachOptions) (AttachEnd, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return 0, fmt.Errorf("connection failed: %w", err)
	}
	defer func() { _ = conn.Close() }()

	req, _ := http.NewRequest("POST", c.baseURL+"/api/v1/processes/"+url.PathEscape(name)+"/attach", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", api.AttachUpgrade)
	if c.username != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.username+":"+c.password)))
	}
	if err := req.Write(conn); err != nil {
		return 0, fmt.Errorf("connection failed: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return 0, fmt.Errorf("connection failed: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer func() { _ = resp.Body.Close() }()
		var e struct {
			Error string `json:"error"`
		}
		body, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return 0, errors.New(e.Error)
		}
		return 0, fmt.Errorf("attach failed: HTTP %d", resp.StatusCode)
	}
	if opts.OnAttached != nil {
		opts.OnAttached()
	}

	type frame struct {
		typ  byte
		data []byte
		err  error
	}
	frames := make(chan frame, 16)
	go func() {
		for {
			typ, data, err := api.ReadFrame(br)
			frames <- frame{typ, data, err}
			if err != nil {
				return
			}
		}
	}()

	detached := make(chan struct{})
	eof := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			data := buf[:n]
			if opts.TTY {
				if i := bytes.IndexByte(data, DetachKey); i >= 0 {
					if i > 0 {
						_ = api.WriteFrame(conn, api.FrameStdin, data[:i])
					}
					close(detached)
					return
				}
			}
			if n > 0 {
				if api.WriteFrame(conn, api.FrameStdin, append([]byte(nil), data...)) != nil {
					return
				}
			}
			if err != nil {
				// End of input never closes the process's stdin.
				close(eof)
				return
			}
		}
	}()

	var idle <-chan time.Time
	var idleTimer *time.Timer
	resetIdle := func() {
		if idleTimer != nil {
			idleTimer.Reset(AttachIdle)
		}
	}
	for {
		select {
		case f := <-frames:
			if f.err != nil {
				return 0, fmt.Errorf("attach to %s lost: %v", name, f.err)
			}
			switch f.typ {
			case api.FrameStdout:
				_, _ = stdout.Write(f.data)
				resetIdle()
			case api.FrameStderr:
				_, _ = stderr.Write(f.data)
				resetIdle()
			case api.FrameExited:
				return AttachExited, nil
			}
		case <-detached:
			return AttachDetached, nil
		case <-eof:
			eof = nil
			if opts.TTY {
				// A terminal that reports end of input has gone away.
				return AttachInterrupted, nil
			}
			{
				idleTimer = time.NewTimer(AttachIdle)
				defer idleTimer.Stop()
				idle = idleTimer.C
			}
		case <-idle:
			return AttachIdleAfterEOF, nil
		case <-ctx.Done():
			return AttachInterrupted, nil
		}
	}
}
