// Command orphaner is a static helper for the root-in-container e2e tests
// (#145, ADR-037). It is copied into a FROM scratch image, which has no
// shell. It reads /proc instead of making Linux-only calls, so it also
// builds on macOS.
//
//	orphaner spawn N [kill]  start N children that outlive it, then exit at once;
//	                         with kill the children end by SIGKILL
//	orphaner child [kill]    sleep one second, then exit (or SIGKILL itself)
//	orphaner zombies  print how many processes in /proc are zombies
//	orphaner env      print the environment, one sorted entry per line
//	orphaner id       print "uid=U euid=E gid=G egid=H groups=[...]"
//	orphaner limits   print "NOFILE soft hard" and "NPROC soft hard"
//	orphaner umask [path]  print "umask 0NNN"; with a path, create it with
//	                  mode 0666 and print "mode 0NNN", its resulting mode
//	orphaner cat path print a file
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "spawn":
		n, _ := strconv.Atoi(os.Args[2])
		args := []string{os.Args[0], "child"}
		if len(os.Args) > 3 {
			args = append(args, os.Args[3])
		}
		for i := 0; i < n; i++ {
			// Not waited for: when this process exits, the children are
			// reparented to PID 1.
			if _, err := os.StartProcess(os.Args[0], args, &os.ProcAttr{}); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	case "child":
		time.Sleep(time.Second)
		if len(os.Args) > 2 && os.Args[2] == "kill" {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		}
	case "zombies":
		stats, _ := filepath.Glob("/proc/[0-9]*/stat")
		n := 0
		for _, path := range stats {
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			// The state follows the command name, which is in parentheses.
			if i := strings.LastIndexByte(string(b), ')'); i >= 0 && strings.HasPrefix(string(b[i:]), ") Z") {
				n++
			}
		}
		fmt.Println(n)
	case "env":
		env := os.Environ()
		sort.Strings(env)
		for _, kv := range env {
			fmt.Println(kv)
		}
	case "id":
		groups, _ := os.Getgroups()
		sort.Ints(groups)
		fmt.Printf("uid=%d euid=%d gid=%d egid=%d groups=%v\n", os.Getuid(), os.Geteuid(), os.Getgid(), os.Getegid(), groups)
	case "limits":
		// /proc/self/limits: "Max open files  1024  4096  files".
		b, err := os.ReadFile("/proc/self/limits")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, line := range strings.Split(string(b), "\n") {
			for prefix, name := range map[string]string{"Max open files": "NOFILE", "Max processes": "NPROC"} {
				if strings.HasPrefix(line, prefix) {
					f := strings.Fields(strings.TrimPrefix(line, prefix))
					if len(f) >= 2 {
						fmt.Printf("%s %s %s\n", name, f[0], f[1])
					}
				}
			}
		}
	case "umask":
		// Read, not set: /proc/self/status has "Umask:\t0022".
		b, err := os.ReadFile("/proc/self/status")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "Umask:"); ok {
				fmt.Printf("umask %s\n", strings.TrimSpace(v))
			}
		}
		if len(os.Args) > 2 {
			f, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o666)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			_ = f.Close()
			fi, err := os.Stat(os.Args[2])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("mode %04o\n", fi.Mode().Perm())
		}
	case "cat":
		b, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(b)
	default:
		os.Exit(2)
	}
}
