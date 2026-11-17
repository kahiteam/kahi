package main

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/ctl"
	"github.com/spf13/cobra"
)

// Dynamic completion of process and group names from the running daemon
// (FUNC-037, ADR-052). The client is the command's own, built from the
// flags already typed (-s, -c, --addr and credentials), with one budget for
// the whole completion; a daemon that is not there or too slow gives no
// names and no message. File names are never offered.

// completionBudget is the total time completion may wait for the daemon.
var completionBudget = time.Second

type completer func(c *ctl.Client, args []string) []string

// complete wraps a completer: it runs within the budget, drops names
// already typed or not matching the prefix, and never offers files.
func complete(fn completer) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		c := newCtlClient()
		c.SetQueryTimeout(completionBudget)
		done := make(chan []string, 1)
		go func() { done <- fn(c, args) }()
		var names []string
		select {
		case names = <-done:
		case <-time.After(completionBudget):
		}
		var out []string
		for _, n := range names {
			if strings.HasPrefix(n, toComplete) && !slices.Contains(args, n) {
				out = append(out, n)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// atArg limits a completer to argument position i (0-based).
func atArg(i int, fn completer) completer {
	return func(c *ctl.Client, args []string) []string {
		if len(args) != i {
			return nil
		}
		return fn(c, args)
	}
}

func processNames(c *ctl.Client, _ []string) []string {
	procs, err := c.Processes()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(procs))
	for _, p := range procs {
		names = append(names, p.Name)
	}
	return names
}

// runningGroups returns the groups in the running state, sorted.
func runningGroups(c *ctl.Client) ([]string, bool) {
	procs, err := c.Processes()
	if err != nil {
		return nil, false
	}
	set := map[string]bool{}
	for _, p := range procs {
		set[p.Group] = true
	}
	return sortedSet(set), true
}

// targets are the names start/stop/restart/status accept: processes,
// {group}:* for every running group, and all.
func targets(c *ctl.Client, _ []string) []string {
	procs, err := c.Processes()
	if err != nil {
		return nil
	}
	var names []string
	groups := map[string]bool{}
	for _, p := range procs {
		names = append(names, p.Name)
		groups[p.Group] = true
	}
	for _, g := range sortedSet(groups) {
		names = append(names, g+":*")
	}
	return append(names, "all")
}

func groupsToRemove(c *ctl.Client, _ []string) []string {
	g, _ := runningGroups(c)
	return g
}

// groupsToAdd are the config file's groups not in the running state.
func groupsToAdd(c *ctl.Client, _ []string) []string {
	entries, err := c.Avail()
	if err != nil {
		return nil
	}
	running, ok := runningGroups(c)
	if !ok {
		return nil
	}
	set := map[string]bool{}
	for _, e := range entries {
		if !slices.Contains(running, e.Group) {
			set[e.Group] = true
		}
	}
	return sortedSet(set)
}

// groupsToUpdate are the running groups and the config file's groups.
func groupsToUpdate(c *ctl.Client, _ []string) []string {
	running, ok := runningGroups(c)
	if !ok {
		return nil
	}
	set := map[string]bool{}
	for _, g := range running {
		set[g] = true
	}
	if entries, err := c.Avail(); err == nil {
		for _, e := range entries {
			set[e.Group] = true
		}
	}
	return sortedSet(set)
}

func signalThenTargets(c *ctl.Client, args []string) []string {
	switch len(args) {
	case 0:
		return config.SignalNames()
	case 1:
		return targets(c, args)
	}
	return nil
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func init() {
	for _, c := range []*cobra.Command{ctlStartCmd, ctlStopCmd, ctlRestartCmd, ctlStatusCmd} {
		c.ValidArgsFunction = complete(targets)
	}
	for _, c := range []*cobra.Command{ctlTailCmd, ctlPIDCmd, ctlSendCmd, ctlAttachCmd} {
		c.ValidArgsFunction = complete(atArg(0, processNames))
	}
	ctlSignalCmd.ValidArgsFunction = complete(signalThenTargets)
	ctlAddCmd.ValidArgsFunction = complete(atArg(0, groupsToAdd))
	ctlRemoveCmd.ValidArgsFunction = complete(atArg(0, groupsToRemove))
	ctlUpdateCmd.ValidArgsFunction = complete(groupsToUpdate)
	ctlRereadCmd.ValidArgsFunction = complete(groupsToUpdate)
	ctlAvailCmd.ValidArgsFunction = cobra.NoFileCompletions
}
