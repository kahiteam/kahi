package api

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/kahiteam/kahi/internal/events"
)

// kahi ctl attach (FUNC-034, ADR-044): POST /api/v1/processes/{name}/attach
// with Upgrade: kahi-attach answers 101 and hijacks the connection. Frames
// in both directions are [1 byte type][4 byte big-endian length][data].
const (
	// AttachUpgrade is the Upgrade protocol name.
	AttachUpgrade = "kahi-attach"

	FrameStdin  byte = 0 // client to daemon: input bytes
	FrameStdout byte = 1 // daemon to client
	FrameStderr byte = 2
	FrameExited byte = 3 // the process exited; data is its state event data (JSON)
)

// maxAttachFrame bounds an input frame.
const maxAttachFrame = 1 << 20

// WriteFrame writes one attach frame.
func WriteFrame(w io.Writer, typ byte, data []byte) error {
	var head [5]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:], uint32(len(data)))
	if _, err := w.Write(head[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ReadFrame reads one attach frame.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxAttachFrame {
		return 0, nil, errors.New("attach frame too large")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, nil, err
	}
	return head[0], data, nil
}

// attachConns are the hijacked connections, closed on Stop: the HTTP
// servers no longer track them.
type attachConns struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func (a *attachConns) add(c net.Conn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conns == nil {
		a.conns = map[net.Conn]struct{}{}
	}
	a.conns[c] = struct{}{}
}

func (a *attachConns) remove(c net.Conn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.conns, c)
}

func (a *attachConns) closeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for c := range a.conns {
		_ = c.Close()
	}
}

func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !headerHasToken(r.Header, "Upgrade", AttachUpgrade) || !headerHasToken(r.Header, "Connection", "upgrade") {
		writeError(w, http.StatusBadRequest, "attach requires Upgrade: kahi-attach", errorCode(http.StatusBadRequest))
		return
	}
	if _, err := s.processes.Get(name); err != nil {
		attachError(w, err)
		return
	}
	session, err := s.processes.Attach(name)
	if err != nil {
		attachError(w, err)
		return
	}

	// Subscribe before the 101, so no output after it is missed. Output
	// comes live from the broadcaster (ADR-040); there is no replay.
	type frame struct {
		typ  byte
		data []byte
	}
	frames := make(chan frame, 256)
	done := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { close(done) }) }
	send := func(f frame) {
		select {
		case frames <- f:
		case <-done:
		default: // a client this far behind loses output, never the capture lock
		}
	}
	var cancels []func()
	for _, st := range []struct {
		name string
		typ  byte
	}{{"stdout", FrameStdout}, {"stderr", FrameStderr}} {
		typ := st.typ
		cancel, err := s.processes.SubscribeLog(name, st.name, func(data []byte, _ string) {
			send(frame{typ, append([]byte(nil), data...)})
		})
		if err == nil {
			cancels = append(cancels, cancel)
		}
	}
	exited := make(chan []byte, 1)
	var busID uint64
	if s.bus != nil {
		busID = s.bus.SubscribeQueue("attach "+name,
			[]events.EventType{events.ProcessStateExited, events.ProcessStateStopped, events.ProcessStateBackoff, events.ProcessStateFatal},
			16, func(e events.Event) {
				if e.Data["name"] != name {
					return
				}
				b, _ := json.Marshal(e.Data)
				select {
				case exited <- b:
				default:
				}
			}, nil)
	}
	cleanup := func() {
		for _, c := range cancels {
			c()
		}
		if s.bus != nil {
			s.bus.Unsubscribe(busID)
		}
		session.Release()
	}

	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		cleanup()
		writeError(w, http.StatusInternalServerError, "attach: "+err.Error(), errorCode(http.StatusInternalServerError))
		return
	}
	s.attach.add(conn)
	defer func() {
		end()
		cleanup()
		s.attach.remove(conn)
		_ = conn.Close()
	}()
	if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: "+AttachUpgrade+"\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		return
	}

	// Input: frames from the client go to stdin as they arrive. The
	// client closing its end ends the session.
	go func() {
		defer end()
		in := bufio.NewReader(io.MultiReader(brw.Reader, conn))
		for {
			typ, data, err := ReadFrame(in)
			if err != nil {
				return
			}
			if typ == FrameStdin && len(data) > 0 {
				if err := session.Write(data); err != nil {
					s.logger.Warn("attach input", "process", name, "error", err)
				}
			}
		}
	}()

	for {
		select {
		case f := <-frames:
			if WriteFrame(conn, f.typ, f.data) != nil {
				return
			}
		case data := <-exited:
			// Output the process wrote before it exited goes first.
			for {
				select {
				case f := <-frames:
					if WriteFrame(conn, f.typ, f.data) != nil {
						return
					}
					continue
				default:
				}
				break
			}
			_ = WriteFrame(conn, FrameExited, data)
			return
		case <-done:
			return
		}
	}
}

func attachError(w http.ResponseWriter, err error) {
	status := classifyError(err)
	writeError(w, status, err.Error(), errorCode(status))
}
