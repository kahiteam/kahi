//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Log rotation (FUNC-020) and ANSI stripping (FUNC-019) against the binary.

// writerProgram returns a program that writes n 1 KB lines of x to stdout,
// pausing pause between lines, then sleeps.
func writerProgram(t *testing.T, dir string, n int, pause string) string {
	t.Helper()
	return writeScript(t, dir, "writer.sh",
		"i=0\nwhile [ $i -lt "+strconv.Itoa(n)+" ]; do printf '%01023d\\n' 0; i=$((i+1)); sleep "+pause+"; done\nexec sleep 300")
}

// syncBuffer is a bytes.Buffer safe for a writer goroutine and a reader.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(fi.Sys().(*syscall.Stat_t).Ino)
}

// Rotation keeps exactly backups files, numbered .1 (newest) to .N, and moves
// the file by rename, so the first log file's inode lives on as a backup.
func TestLog_RotationKeepsNBackups(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.Mkdir(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(logs, "out.log")
	client, _ := startDaemon(t, `
[programs.writer]
command = "`+writerProgram(t, dir, 40, "0.02")+`"
startsecs = 0
stdout_logfile = "`+log+`"
stdout_logfile_maxbytes = "4KB"
stdout_logfile_backups = 3
`)
	waitForState(t, client, "writer", "RUNNING", 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if fi, err := os.Stat(log); err == nil && fi.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no log output")
		}
		time.Sleep(10 * time.Millisecond)
	}
	first := inode(t, log)

	// 40 KB through a 4 KB file: many rotations, three backups kept.
	time.Sleep(3 * time.Second)
	matches, _ := filepath.Glob(log + ".*")
	if strings.Join(matches, ",") != strings.Join([]string{log + ".1", log + ".2", log + ".3"}, ",") {
		t.Fatalf("backups %v, want %s.1 to .3", matches, log)
	}
	for _, m := range append(matches, log) {
		if fi, _ := os.Stat(m); fi.Size() > 4096+8192 {
			t.Errorf("%s is %d bytes, want about maxbytes", m, fi.Size())
		}
	}

	// A file kept by rename, not copied: the inodes are all different from
	// the current file, and the files that were rotated out keep theirs.
	cur := inode(t, log)
	if cur == first {
		t.Fatal("the log file was never replaced: rotation copied and truncated")
	}
	seen := map[uint64]bool{cur: true}
	for _, m := range matches {
		if seen[inode(t, m)] {
			t.Errorf("%s shares an inode with another log file", m)
		}
		seen[inode(t, m)] = true
	}
}

// maxbytes = 0 means no limit: the file grows and nothing rotates.
func TestLog_RotationUnlimited(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "out.log")
	client, _ := startDaemon(t, `
[programs.writer]
command = "`+writerProgram(t, dir, 20, "0")+`"
startsecs = 0
stdout_logfile = "`+log+`"
stdout_logfile_maxbytes = "0"
`)
	waitForState(t, client, "writer", "RUNNING", 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if fi, err := os.Stat(log); err == nil && fi.Size() >= 20*1024 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("log never reached 20 KB")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if matches, _ := filepath.Glob(log + ".*"); len(matches) != 0 {
		t.Fatalf("backups %v with maxbytes = 0", matches)
	}
}

// When the rename fails, the daemon keeps writing to the current file.
func TestLog_RotationRenameFailureKeepsWriting(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.Mkdir(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(logs, "out.log")
	// The log file exists before the directory is locked; the rename of a
	// full file then fails, while appending to it still works.
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	client, _ := startDaemon(t, `
[programs.writer]
command = "`+writerProgram(t, dir, 30, "0.05")+`"
autostart = false
startsecs = 0
stdout_logfile = "`+log+`"
stdout_logfile_maxbytes = "2KB"
stdout_logfile_backups = 2
`)
	if err := os.Chmod(logs, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(logs, 0o755) })
	if err := client.Start("writer"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	fi, err := os.Stat(log)
	if err != nil || fi.Size() <= 4096 {
		t.Fatalf("log file %v, %v: want it to keep growing past maxbytes", fi, err)
	}
	if matches, _ := filepath.Glob(log + ".*"); len(matches) != 0 {
		t.Fatalf("backups %v in a read-only directory", matches)
	}
	if info, _ := getProcessInfo(client, "writer"); info.State != "RUNNING" {
		t.Fatalf("writer %s", info.State)
	}
}

// An unparseable size is a config error, not "no limit" (#187).
func TestLog_InvalidMaxbytesRefused(t *testing.T) {
	for _, size := range []string{"10K", "1.5MB", "abc", "-1"} {
		expectRefused(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\nstdout_logfile_maxbytes = \""+size+"\"\n",
			`programs.web: stdout_logfile_maxbytes must be a size like 50MB (B, KB, MB or GB), got "`+size+`"`)
	}
}

// strip_ansi removes every CSI sequence from the log file, the tail buffer
// and tail -f, also when a sequence is split across reads; without it the
// sequences are kept.
func TestLog_ANSIStripEverywhere(t *testing.T) {
	dir := t.TempDir()
	// Colours, cursor movement, erase and private modes; the last sequence
	// is split across two writes with a pause between them.
	script := writeScript(t, dir, "colour.sh", `sleep 1
printf '\033[31mERROR\033[0m: failed\n'
printf '\033[2J\033[1;1H\033[Kcleared\033[?25l\n'
printf 'split \033[3'
sleep 0.3
printf '2mgreen\033[0m\n'
exec sleep 300`)
	for _, strip := range []bool{true, false} {
		name := "plain"
		setting := ""
		if strip {
			name = "stripped"
			setting = "strip_ansi = true\n"
		}
		t.Run(name, func(t *testing.T) {
			log := filepath.Join(dir, name+".log")
			client, _ := startDaemon(t, `
[programs.colour]
command = "`+script+`"
startsecs = 0
stdout_logfile = "`+log+`"
`+setting)
			waitForState(t, client, "colour", "RUNNING", 5*time.Second)

			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			var follow syncBuffer
			go func() { _ = client.TailFollow(ctx, "colour", "stdout", &follow) }()

			waitForOutput(t, client, "colour", "green")
			time.Sleep(300 * time.Millisecond)
			var tail bytes.Buffer
			if err := client.Tail("colour", "stdout", 4096, &tail); err != nil {
				t.Fatal(err)
			}
			file, _ := os.ReadFile(log)
			want := "ERROR: failed\ncleared\nsplit green\n"
			for what, got := range map[string]string{"log file": string(file), "tail": tail.String(), "tail -f": follow.String()} {
				if strip {
					// tail -f shows the output exactly, also the line
					// written in two chunks (#190).
					if strings.Contains(got, "\x1b") || !strings.Contains(got, want) {
						t.Errorf("%s: %q, want %q without escapes", what, got, want)
					}
				} else if !strings.Contains(got, "\x1b[31mERROR\x1b[0m") {
					t.Errorf("%s: %q, want the escapes kept", what, got)
				}
			}
		})
	}
}

// kahi ctl tail -f prints the output as written: a line written in two
// chunks stays one line, and no blank lines are added (#190).
func TestLog_TailFollowCLIExact(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "chunks.sh", `sleep 1
printf 'one\n'
printf 'split '
sleep 0.3
printf 'line\n'
printf 'three\n'
exec sleep 300`)
	client, socket := startDaemon(t, `
[programs.chunks]
command = "`+script+`"
startsecs = 0
`)
	waitForState(t, client, "chunks", "RUNNING", 5*time.Second)
	cmd := exec.Command(kahiBinary, "ctl", "-s", socket, "tail", "-f", "chunks")
	out := &syncBuffer{}
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "three\n") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got, want := out.String(), "one\nsplit line\nthree\n"; got != want {
		t.Fatalf("ctl tail -f printed %q, want %q", got, want)
	}
}
