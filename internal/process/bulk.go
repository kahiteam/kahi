package process

import (
	"fmt"
	"time"

	"github.com/kahiteam/kahi/internal/api"
)

// errShuttingDown is the bulk-action error once process shutdown has begun.
const errShuttingDown = "daemon is shutting down"

// stopWaitMargin is added to a process's stopwaitsecs when a bulk stop waits
// for it, to cover the SIGKILL escalation after stopwaitsecs.
const stopWaitMargin = 5

// ShuttingDown reports whether StopAll has begun process shutdown.
func (m *Manager) ShuttingDown() bool {
	select {
	case <-m.shutdownCh:
		return true
	default:
		return false
	}
}

// StartAllOrdered starts every configured process in ascending priority order,
// including processes with autostart = false (FUNC-083, ADR-030).
func (m *Manager) StartAllOrdered() []api.ActionResult {
	return m.startAllOrdered(nil)
}

// StopAllOrdered stops every configured process in descending priority order,
// waiting for each to stop before the next. Unlike StopAll it does not begin
// shutdown.
func (m *Manager) StopAllOrdered() []api.ActionResult {
	return m.stopPass(m.orderedNames(false))
}

// RestartAllOrdered stops every process, then starts every process except
// those whose stop failed. Both passes use one snapshot, and the result has
// one entry per process in start order.
func (m *Manager) RestartAllOrdered() []api.ActionResult {
	order := m.orderedNames(true)
	reversed := make([]string, len(order))
	for i, name := range order {
		reversed[len(order)-1-i] = name
	}

	stopFailed := map[string]api.ActionResult{}
	for _, r := range m.stopPass(reversed) {
		if r.Status == api.ActionError {
			stopFailed[r.Name] = r
		}
	}
	skip := make(map[string]bool, len(stopFailed))
	for name := range stopFailed {
		skip[name] = true
	}
	started := map[string]api.ActionResult{}
	for _, r := range m.startPass(order, skip) {
		started[r.Name] = r
	}

	results := make([]api.ActionResult, 0, len(order))
	for _, name := range order {
		if r, ok := stopFailed[name]; ok {
			results = append(results, r)
		} else {
			results = append(results, started[name])
		}
	}
	return results
}

// startAllOrdered is the start pass over a fresh snapshot, leaving out the
// names in skip entirely.
func (m *Manager) startAllOrdered(skip map[string]bool) []api.ActionResult {
	return m.startPass(m.orderedNames(true), skip)
}

// orderedNames snapshots the process names in priority order. Names, not
// pointers: each action re-resolves its process, so a process removed by a
// concurrent reload is never acted on detached.
func (m *Manager) orderedNames(ascending bool) []string {
	m.mu.RLock()
	procs := m.sortedProcesses(ascending)
	m.mu.RUnlock()
	names := make([]string, len(procs))
	for i, p := range procs {
		names[i] = p.Name()
	}
	return names
}

func (m *Manager) startPass(order []string, skip map[string]bool) []api.ActionResult {
	results := make([]api.ActionResult, 0, len(order))
	for _, name := range order {
		if skip[name] {
			continue
		}
		results = append(results, m.startOne(name))
	}
	return results
}

func (m *Manager) stopPass(order []string) []api.ActionResult {
	results := make([]api.ActionResult, 0, len(order))
	for _, name := range order {
		results = append(results, m.stopOne(name))
	}
	return results
}

// startOne starts one process unless it is already up. No Manager lock is held
// across Start: shutdown is checked before and after, and a process whose start
// raced the beginning of shutdown is stopped again.
func (m *Manager) startOne(name string) api.ActionResult {
	if m.ShuttingDown() {
		return actionError(name, errShuttingDown)
	}
	p, err := m.GetProcess(name)
	if err != nil {
		return actionError(name, err.Error())
	}
	switch p.State() {
	case Running, Starting, Backoff:
		return api.ActionResult{Name: name, Status: api.ActionSkipped}
	case Stopping:
		return actionError(name, "process is stopping")
	}

	startErr := p.Start()
	if m.ShuttingDown() {
		_ = p.Stop()
		return actionError(name, errShuttingDown)
	}
	if startErr != nil {
		return actionError(name, startErr.Error())
	}
	return api.ActionResult{Name: name, Status: api.ActionOK}
}

// stopOne stops one process and waits up to its stopwaitsecs plus
// stopWaitMargin for it to stop. A process already STOPPING is waited for, not
// signalled again.
func (m *Manager) stopOne(name string) api.ActionResult {
	p, err := m.GetProcess(name)
	if err != nil {
		return actionError(name, err.Error())
	}
	switch p.State() {
	case Stopped, Exited, Fatal:
		return api.ActionResult{Name: name, Status: api.ActionSkipped}
	case Backoff:
		if err := p.Stop(); err != nil {
			return actionError(name, err.Error())
		}
		return api.ActionResult{Name: name, Status: api.ActionOK}
	case Running, Starting:
		if err := p.Stop(); err != nil {
			return actionError(name, err.Error())
		}
	}

	waitSecs := p.Config().Stopwaitsecs + stopWaitMargin
	if !m.waitForStopped(p, time.Duration(waitSecs)*time.Second) {
		return actionError(name, fmt.Sprintf("stop timed out after %ds", waitSecs))
	}
	return api.ActionResult{Name: name, Status: api.ActionOK}
}

func actionError(name, msg string) api.ActionResult {
	return api.ActionResult{Name: name, Status: api.ActionError, Error: msg}
}
