package logging

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func newTestCapture(t *testing.T, strip bool) *CaptureWriter {
	t.Helper()
	cw, err := NewCaptureWriter(CaptureConfig{ProcessName: "web", Stream: "stdout", StripAnsi: strip})
	if err != nil {
		t.Fatal(err)
	}
	return cw
}

// Ids are "{generation}-{end offset}", offsets count captured (stripped)
// bytes, and ReadFrom returns what the buffer still holds (ADR-033).
func TestCaptureOffsetsAndReadFrom(t *testing.T) {
	cw := newTestCapture(t, true)
	var ids []string
	publish := func(_ []byte, id string) { ids = append(ids, id) }
	cw.CaptureAndPublish([]byte("hello "), publish)
	cw.CaptureAndPublish([]byte("\x1b[31mworld\x1b[0m"), publish)
	gen := cw.Generation()
	if strings.Join(ids, ",") != gen+"-6,"+gen+"-11" {
		t.Fatalf("ids %v, want offsets 6 and 11 after stripping", ids)
	}

	data, start, total := cw.ReadFrom(6)
	if string(data) != "world" || start != 6 || total != 11 {
		t.Fatalf("ReadFrom(6) = %q, %d, %d", data, start, total)
	}
	if data, start, _ := cw.ReadFrom(11); data != nil || start != 11 {
		t.Fatalf("ReadFrom(11) = %q, %d, want nothing", data, start)
	}

	// Past 64 KB the oldest bytes are gone: the replay starts later.
	cw.Capture(bytes.Repeat([]byte("a"), 70000))
	data, start, total = cw.ReadFrom(0)
	if total != 70011 || start != 70011-65536 || len(data) != 65536 {
		t.Fatalf("ReadFrom(0) after 70011 bytes: len %d, start %d, total %d", len(data), start, total)
	}
}

func TestCaptureGenerationsDiffer(t *testing.T) {
	if a, b := newTestCapture(t, false).Generation(), newTestCapture(t, false).Generation(); a == b {
		t.Fatalf("two writers share generation %s", a)
	}
}

// Two goroutines writing one stream publish in offset order, because the id
// and the publish happen under the writer's lock.
func TestCapturePublishOrder(t *testing.T) {
	cw := newTestCapture(t, false)
	var mu sync.Mutex
	var ends []int64
	publish := func(_ []byte, id string) {
		mu.Lock()
		defer mu.Unlock()
		_, off, _ := strings.Cut(id, "-")
		n, _ := strconv.ParseInt(off, 10, 64)
		ends = append(ends, n)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				cw.CaptureAndPublish([]byte("xy"), publish)
			}
		}()
	}
	wg.Wait()
	for i := 1; i < len(ends); i++ {
		if ends[i] <= ends[i-1] {
			t.Fatalf("id %d out of order: %d after %d", i, ends[i], ends[i-1])
		}
	}
	if len(ends) != 1000 || ends[len(ends)-1] != 2000 {
		t.Fatalf("%d ids ending at %d, want 1000 ending at 2000", len(ends), ends[len(ends)-1])
	}
}
