package process

import (
	"sort"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

// ApplyReload applies a reloaded config. It stops the processes of removed
// and changed programs, waits for them to stop, and removes them together
// with their capture writers and implicit groups. It then loads cfg, which
// creates the changed programs afresh from the new config, and starts the
// processes of added and changed programs that autostart.
//
// A program whose processes do not stop within stopwaitsecs plus
// stopWaitMargin keeps its old processes and is returned in stuck, so the
// caller can keep its old config and a later reload retries it.
func (m *Manager) ApplyReload(cfg *config.Config, added, changed, removed []string) (stuck []string) {
	retiring := append(append([]string{}, removed...), changed...)

	// Retire before stopping: an autorestart or backoff retry firing between
	// the stop and the removal would otherwise spawn an untracked child.
	for _, prog := range retiring {
		for _, p := range m.programProcesses(prog) {
			p.Retire()
		}
	}

	// Signal every retiring process first, then wait, so the waits overlap
	// instead of adding up.
	for _, prog := range retiring {
		for _, p := range m.programProcesses(prog) {
			if stoppable(p.State()) {
				if err := p.Stop(); err != nil {
					m.logger.Error("reload stop failed", "program", prog, "process", p.Name(), "error", err)
				}
			}
		}
	}

	stuckSet := make(map[string]bool)
	for _, prog := range retiring {
		for _, p := range m.programProcesses(prog) {
			wait := time.Duration(p.Config().Stopwaitsecs+stopWaitMargin) * time.Second
			if !m.waitForStopped(p, wait) {
				m.logger.Error("process did not stop for reload; keeping its old config",
					"program", prog, "process", p.Name(), "state", p.State().String())
				stuckSet[prog] = true
			}
		}
	}

	isRemoved := make(map[string]bool, len(removed))
	for _, prog := range removed {
		isRemoved[prog] = true
	}
	for _, prog := range retiring {
		if !stuckSet[prog] {
			m.removeProgram(prog, isRemoved[prog])
		}
	}
	m.removeStaleGroups(cfg, stuckSet)

	m.LoadConfig(cfg)

	for _, prog := range append(append([]string{}, added...), changed...) {
		if !stuckSet[prog] {
			m.startProgram(prog)
		}
	}

	for prog := range stuckSet {
		stuck = append(stuck, prog)
	}
	sort.Strings(stuck)
	return stuck
}

// programProcesses returns the processes of a program, sorted by name.
func (m *Manager) programProcesses(prog string) []*Process {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var procs []*Process
	for _, p := range m.processes {
		if p.Group() == prog {
			procs = append(procs, p)
		}
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].Name() < procs[j].Name() })
	return procs
}

// removeProgram deletes a stopped program's processes, closes and deletes
// their capture writers, and deletes its implicit group.
func (m *Manager) removeProgram(prog string, publish bool) {
	m.mu.Lock()
	for name, p := range m.processes {
		if p.Group() != prog {
			continue
		}
		delete(m.processes, name)
		for _, key := range []string{name + ":stdout", name + ":stderr"} {
			if cw := m.captures[key]; cw != nil {
				if err := cw.Close(); err != nil {
					m.logger.Warn("close capture writer", "process", name, "error", err)
				}
			}
			delete(m.captures, key)
		}
	}
	delete(m.groups, prog)
	m.mu.Unlock()

	if publish && m.bus != nil {
		m.bus.Publish(events.Event{
			Type: events.ProcessGroupRemoved,
			Data: map[string]string{"group": prog},
		})
	}
}

// removeStaleGroups deletes groups that cfg no longer defines, either as a
// program or as an explicit group. LoadConfig rebuilds the explicit groups
// that remain; it never deletes any.
func (m *Manager) removeStaleGroups(cfg *config.Config, keep map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name := range m.groups {
		if _, ok := cfg.Programs[name]; ok {
			continue
		}
		if _, ok := cfg.Groups[name]; ok {
			continue
		}
		if !keep[name] {
			delete(m.groups, name)
		}
	}
}

// startProgram starts the processes of a program that autostart, unless the
// daemon is shutting down.
func (m *Manager) startProgram(prog string) {
	for _, p := range m.programProcesses(prog) {
		if m.ShuttingDown() {
			return
		}
		if a := p.Config().Autostart; a != nil && !*a {
			continue
		}
		if err := p.Start(); err != nil {
			m.logger.Error("reload start failed", "program", prog, "process", p.Name(), "error", err)
		}
	}
}
