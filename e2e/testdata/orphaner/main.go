// Command orphaner is a static helper for the PID 1 e2e test (#145). It is
// copied into a FROM scratch image, which has no shell.
//
//	orphaner spawn N  start N children that outlive it, then exit at once
//	orphaner child    sleep one second, then exit
//	orphaner zombies  print how many processes in /proc are zombies
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "spawn":
		n, _ := strconv.Atoi(os.Args[2])
		for i := 0; i < n; i++ {
			// Not waited for: when this process exits, the children are
			// reparented to PID 1.
			if _, err := os.StartProcess(os.Args[0], []string{os.Args[0], "child"}, &os.ProcAttr{}); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	case "child":
		time.Sleep(time.Second)
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
	default:
		os.Exit(2)
	}
}
