package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// completionServer is a daemon API with processes and the avail list.
func completionServer(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/processes":
			_, _ = w.Write([]byte(`[{"name":"worker_1","group":"worker"},{"name":"web","group":"web"},{"name":"worker_0","group":"worker"}]`))
		case "/api/v1/config/avail":
			_, _ = w.Write([]byte(`[{"name":"a","group":"g"},{"name":"web","group":"web","in_use":true},{"name":"x","group":"x"}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	ctlAddr, ctlSocket = strings.TrimPrefix(srv.URL, "http://"), ""
	t.Cleanup(func() { ctlAddr = "" })
}

func run(t *testing.T, cmd *cobra.Command, args []string, prefix string) []string {
	t.Helper()
	names, dir := cmd.ValidArgsFunction(cmd, args, prefix)
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("%s: directive %d", cmd.Name(), dir)
	}
	return names
}

func TestCompletionSets(t *testing.T) {
	completionServer(t)
	for _, c := range []struct {
		cmd    *cobra.Command
		args   []string
		prefix string
		want   string
	}{
		{ctlStartCmd, nil, "", "web worker_0 worker_1 web:* worker:* all"},
		{ctlStopCmd, []string{"web"}, "w", "worker_0 worker_1 web:* worker:*"},
		{ctlTailCmd, nil, "", "web worker_0 worker_1"},
		{ctlTailCmd, []string{"web"}, "", ""},
		{ctlSignalCmd, nil, "SIGU", "SIGUSR1 SIGUSR2"},
		{ctlSignalCmd, []string{"SIGTERM"}, "", "web worker_0 worker_1 web:* worker:* all"},
		{ctlSignalCmd, []string{"SIGTERM", "web"}, "", ""},
		{ctlAddCmd, nil, "", "g x"},
		{ctlRemoveCmd, nil, "", "web worker"},
		{ctlUpdateCmd, nil, "", "g web worker x"},
		{ctlRereadCmd, []string{"g"}, "", "web worker x"},
	} {
		if got := strings.Join(run(t, c.cmd, c.args, c.prefix), " "); got != c.want {
			t.Errorf("%s %v %q: %q, want %q", c.cmd.Name(), c.args, c.prefix, got, c.want)
		}
	}
}

// No daemon, or a daemon that never answers: no names within the budget.
func TestCompletionWithoutDaemon(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	old := completionBudget
	completionBudget = 200 * time.Millisecond
	t.Cleanup(func() { completionBudget = old })
	ctlAddr = ln.Addr().String()
	t.Cleanup(func() { ctlAddr = "" })
	start := time.Now()
	if names := run(t, ctlStartCmd, nil, ""); len(names) != 0 {
		t.Errorf("stalled daemon: %q", names)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v", d)
	}
	for _, cmd := range []*cobra.Command{ctlAddCmd, ctlUpdateCmd, ctlRemoveCmd} {
		if names := run(t, cmd, nil, ""); len(names) != 0 {
			t.Errorf("%s: %q", cmd.Name(), names)
		}
	}
}
