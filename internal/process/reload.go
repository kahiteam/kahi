package process

import (
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

// groupSpec is an effective group of a config: a [groups] table, which owns
// its member programs, or a program that no table lists (ADR-034).
type groupSpec struct {
	Programs []string // sorted
	Priority int
	Table    bool
}

// effectiveGroups returns the groups cfg defines, by name.
func effectiveGroups(cfg *config.Config) map[string]groupSpec {
	out := make(map[string]groupSpec)
	inTable := make(map[string]bool)
	for name, g := range cfg.Groups {
		progs := append([]string(nil), g.Programs...)
		sort.Strings(progs)
		out[name] = groupSpec{Programs: progs, Priority: g.Priority, Table: true}
		for _, prog := range g.Programs {
			inTable[prog] = true
		}
	}
	for name, p := range cfg.Programs {
		if !inTable[name] {
			out[name] = groupSpec{Programs: []string{name}, Priority: p.Priority}
		}
	}
	return out
}

// tableOf returns the [groups] table of cfg that lists prog, or "".
func tableOf(cfg *config.Config, prog string) string {
	for name, g := range cfg.Groups {
		if slices.Contains(g.Programs, prog) {
			return name
		}
	}
	return ""
}

func sortedNames(specs map[string]groupSpec) []string {
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ReloadOutcome is what ApplyReload did. Added, Changed and Removed name
// groups. Stuck names the programs whose processes did not stop: they keep
// their old processes, and StuckGroups the groups that therefore stay as
// they were, so the caller can keep their old config and a later reload
// retries them.
type ReloadOutcome struct {
	Added, Changed, Removed []string
	Stuck, StuckGroups      []string
}

// PlanReload reports the groups a reload from oldCfg to newCfg adds, changes
// and removes, against the groups that are active now: a group the config
// defines but that was removed at runtime counts as added, and one that is
// inactive and no longer defined is not reported (ADR-034).
func (m *Manager) PlanReload(oldCfg, newCfg *config.Config) (added, changed, removed []string) {
	_, c, r := ConfigDiff(oldCfg, newCfg)
	m.mu.RLock()
	active := make(map[string]bool, len(m.groups))
	for name := range m.groups {
		active[name] = true
	}
	m.mu.RUnlock()

	for _, name := range sortedNames(effectiveGroups(newCfg)) {
		if !active[name] {
			added = append(added, name)
		}
	}
	for _, name := range c {
		if active[name] {
			changed = append(changed, name)
		}
	}
	for _, name := range r {
		if active[name] {
			removed = append(removed, name)
		}
	}
	return added, changed, removed
}

// reloadUnit is what a reload stops: a whole group (removed, or recreated
// because its member list changed) or one program of an unchanged group
// whose spawn settings changed.
type reloadUnit struct {
	group, program string
	procs          []*Process
}

// ApplyReload applies a reloaded config while preserving the run state of
// what already exists (ADR-034, #119):
//
//   - Removed groups are stopped, then deleted with their processes and
//     capture writers (PROCESS_GROUP_REMOVED).
//   - A group whose member list changed is removed and added again; a
//     [groups] table dropped while its programs stay becomes their own
//     groups, and a table added over active programs replaces theirs.
//   - In a group whose members are unchanged, a changed program whose change
//     only touches supervision settings (see needsRestart) is updated in
//     place; any other changed program is stopped and created afresh, and
//     started again only if one of its processes was running, starting or
//     backing off.
//   - Added groups, including groups the config defines that were removed at
//     runtime, are created (PROCESS_GROUP_ADDED) and their autostart
//     processes started.
//
// A group or program whose processes do not stop within stopwaitsecs plus
// stopWaitMargin keeps its old processes and is reported as stuck.
func (m *Manager) ApplyReload(oldCfg, newCfg *config.Config) ReloadOutcome {
	added, changed, removed := m.PlanReload(oldCfg, newCfg)
	og, ng := effectiveGroups(oldCfg), effectiveGroups(newCfg)

	var units []reloadUnit
	for _, name := range removed {
		units = append(units, m.groupUnit(name))
	}
	var recreate []string
	wasActive := make(map[string]bool)
	for _, name := range changed {
		o, n := og[name], ng[name]
		if !slices.Equal(o.Programs, n.Programs) || o.Table != n.Table {
			recreate = append(recreate, name)
			units = append(units, m.groupUnit(name))
			continue
		}
		for _, prog := range n.Programs {
			oldProg, newProg := oldCfg.Programs[prog], newCfg.Programs[prog]
			if !programChanged(oldProg, newProg) {
				continue
			}
			if !needsRestart(oldProg, newProg) {
				m.updateInPlace(prog, newProg)
				continue
			}
			procs := m.programProcesses(prog)
			for _, p := range procs {
				if stoppable(p.State()) {
					wasActive[prog] = true
				}
			}
			units = append(units, reloadUnit{group: name, program: prog, procs: procs})
		}
		if o.Priority != n.Priority {
			m.setGroupPriority(name, n.Priority)
		}
	}

	// Retire before stopping: an autorestart or backoff retry firing between
	// the stop and the removal would otherwise spawn an untracked child.
	for _, u := range units {
		for _, p := range u.procs {
			p.Retire()
		}
	}
	// Signal every process first, then wait, so the waits overlap instead of
	// adding up.
	for _, u := range units {
		for _, p := range u.procs {
			if stoppable(p.State()) {
				if err := p.Stop(); err != nil {
					m.logger.Error("reload stop failed", "process", p.Name(), "error", err)
				}
			}
		}
	}
	stuckUnit := make([]bool, len(units))
	for i, u := range units {
		for _, p := range u.procs {
			wait := time.Duration(p.Config().Stopwaitsecs+stopWaitMargin) * time.Second
			if !m.waitForStopped(p, wait) {
				m.logger.Error("process did not stop for reload; keeping its old config",
					"group", u.group, "process", p.Name(), "state", p.State().String())
				stuckUnit[i] = true
			}
		}
	}

	var out ReloadOutcome
	stuckGroup := make(map[string]bool)
	for i, u := range units {
		if stuckUnit[i] {
			if u.program != "" {
				out.Stuck = append(out.Stuck, u.program)
			} else {
				stuckGroup[u.group] = true
				out.Stuck = append(out.Stuck, og[u.group].Programs...)
			}
			continue
		}
		if u.program != "" {
			m.recreateProgram(u.group, u.program, newCfg)
			if wasActive[u.program] {
				m.startProgram(u.program, false)
			}
			continue
		}
		m.mu.Lock()
		procs := m.deactivateLocked(u.group)
		m.rebuildGroupPriorityLocked()
		m.mu.Unlock()
		m.publishGroup(events.ProcessGroupRemoved, u.group, procs)
	}

	// Recreated groups come back first, then the added ones. A group whose
	// member programs still have processes (they belong to a stuck group)
	// is left for the next reload.
	for _, name := range append(append([]string{}, recreate...), added...) {
		if stuckGroup[name] {
			continue
		}
		spec := ng[name]
		m.mu.Lock()
		blocked := false
		for _, prog := range spec.Programs {
			if len(m.programProcessesLocked(prog)) > 0 {
				blocked = true
			}
		}
		if blocked {
			m.mu.Unlock()
			stuckGroup[name] = true
			continue
		}
		procs := m.activateLocked(name, spec, newCfg)
		m.rebuildGroupPriorityLocked()
		m.mu.Unlock()
		m.publishGroup(events.ProcessGroupAdded, name, procs)
		for _, prog := range spec.Programs {
			m.startProgram(prog, true)
		}
	}

	out.Added, out.Changed, out.Removed = added, changed, removed
	sort.Strings(out.Stuck)
	for name := range stuckGroup {
		out.StuckGroups = append(out.StuckGroups, name)
	}
	sort.Strings(out.StuckGroups)
	return out
}

// groupUnit is the reload unit for a whole active group.
func (m *Manager) groupUnit(name string) reloadUnit {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u := reloadUnit{group: name}
	if g := m.groups[name]; g != nil {
		u.procs = m.groupProcessesSorted(g, false)
	}
	return u
}

// recreateProgram replaces the stopped processes of prog, in group, with new
// ones from cfg; the group keeps its other members. No group event: the
// group itself is unchanged.
func (m *Manager) recreateProgram(group, prog string, cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.programProcessesLocked(prog) {
		m.deleteProcessLocked(p.Name())
	}
	m.createProgramLocked(prog, cfg.Programs[prog], group)
	if g := m.groups[group]; g != nil {
		var procs []string
		for name, p := range m.processes {
			if p.Group() == group {
				procs = append(procs, name)
			}
		}
		sort.Strings(procs)
		g.Processes = procs
	}
}

// setGroupPriority changes a group's priority in place.
func (m *Manager) setGroupPriority(name string, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g := m.groups[name]; g != nil {
		g.Priority = priority
	}
	m.rebuildGroupPriorityLocked()
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
	// A program's own group takes its priority; a [groups] table member's
	// priority is the table's.
	m.mu.Lock()
	if g, ok := m.groups[prog]; ok && !g.Table {
		g.Priority = progCfg.Priority
	}
	m.mu.Unlock()
}

// programProcesses returns the processes of a program, sorted by name.
func (m *Manager) programProcesses(prog string) []*Process {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.programProcessesLocked(prog)
}

func (m *Manager) programProcessesLocked(prog string) []*Process {
	var procs []*Process
	for _, p := range m.processes {
		if p.Program() == prog {
			procs = append(procs, p)
		}
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].Name() < procs[j].Name() })
	return procs
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
