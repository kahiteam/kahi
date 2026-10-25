package process

import (
	"reflect"
	"sort"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

// ApplyReload applies a reloaded config while preserving the run state of
// what already exists:
//
//   - Removed programs are stopped and removed with their capture writers
//     and groups.
//   - Changed programs whose change only touches supervision settings (see
//     needsRestart) are updated in place; their children keep running.
//   - Other changed programs are stopped, removed and created afresh from the
//     new config; they are started again only if one of their processes was
//     running, starting or backing off before. autostart is not consulted:
//     it governs daemon startup, and editing it must not stop anything.
//   - Added programs are started when autostart allows it.
//
// A program whose processes do not stop within stopwaitsecs plus
// stopWaitMargin keeps its old processes and is returned in stuck, so the
// caller can keep its old config and a later reload retries it.
func (m *Manager) ApplyReload(oldCfg, newCfg *config.Config, added, changed, removed []string) (stuck []string) {
	var restart []string
	for _, prog := range changed {
		if needsRestart(oldCfg.Programs[prog], newCfg.Programs[prog]) {
			restart = append(restart, prog)
		} else {
			m.updateInPlace(prog, newCfg.Programs[prog])
		}
	}

	wasActive := make(map[string]bool)
	for _, prog := range restart {
		for _, p := range m.programProcesses(prog) {
			if stoppable(p.State()) {
				wasActive[prog] = true
			}
		}
	}

	retiring := append(append([]string{}, removed...), restart...)

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
	m.removeStaleGroups(newCfg, stuckSet)

	m.LoadConfig(newCfg)

	for _, prog := range restart {
		if !stuckSet[prog] && wasActive[prog] {
			m.startProgram(prog, false)
		}
	}
	for _, prog := range added {
		m.startProgram(prog, true)
	}

	for prog := range stuckSet {
		stuck = append(stuck, prog)
	}
	sort.Strings(stuck)
	return stuck
}

// needsRestart reports whether a program change affects how its children
// are spawned. Settings the supervisor only consults while managing a
// process are applied in place; any other difference, including fields added
// later, restarts the program.
func needsRestart(oldProg, newProg config.ProgramConfig) bool {
	return !reflect.DeepEqual(spawnSettings(oldProg), spawnSettings(newProg))
}

// spawnSettings returns cfg with the supervision-only settings cleared.
func spawnSettings(cfg config.ProgramConfig) config.ProgramConfig {
	cfg.Priority = 0
	cfg.Autostart = nil
	cfg.Autorestart = ""
	cfg.Startsecs = 0
	cfg.Startretries = 0
	cfg.Exitcodes = nil
	cfg.Stopsignal = ""
	cfg.Stopwaitsecs = 0
	cfg.Stopasgroup = false
	cfg.Killasgroup = false
	cfg.Description = ""
	return cfg
}

// updateInPlace gives the running processes of prog their new config without
// restarting them. The instance set is unchanged: numprocs and process_name
// are spawn settings.
func (m *Manager) updateInPlace(prog string, progCfg config.ProgramConfig) {
	byName := make(map[string]config.ProgramConfig)
	for _, inst := range ExpandNumprocs(prog, progCfg) {
		byName[inst.Name] = inst.Config
	}
	for _, p := range m.programProcesses(prog) {
		if cfg, ok := byName[p.Name()]; ok {
			p.UpdateConfig(cfg)
		}
	}
	m.mu.Lock()
	if g, ok := m.groups[prog]; ok {
		g.Priority = progCfg.Priority
	}
	m.mu.Unlock()
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

// startProgram starts the processes of a program unless the daemon is
// shutting down. With honourAutostart, processes whose autostart is false
// are left stopped.
func (m *Manager) startProgram(prog string, honourAutostart bool) {
	for _, p := range m.programProcesses(prog) {
		if m.ShuttingDown() {
			return
		}
		if a := p.Config().Autostart; honourAutostart && a != nil && !*a {
			continue
		}
		if err := p.Start(); err != nil {
			m.logger.Error("reload start failed", "program", prog, "process", p.Name(), "error", err)
		}
	}
}
