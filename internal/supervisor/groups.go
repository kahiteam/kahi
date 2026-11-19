package supervisor

import (
	"errors"
	"fmt"
	"maps"
	"sort"

	"github.com/kahiteam/kahi/internal/config"
)

// Partial update and avail (FUNC-085, ADR-051).

// ReloadGroups applies the config file's changes to the named groups only;
// no names, or "all", is a full reload. The whole file is loaded and
// validated first, and an unknown name refuses the update before anything
// changes. Groups that are not named keep their old config, so a later
// reread still reports them; [supervisor] settings and webhooks change only
// on a full reload.
func (s *Supervisor) ReloadGroups(groups []string) (res config.ReloadResult, err error) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	if isAll(groups) {
		return s.applyReload()
	}
	// One attempt per update, refused names included (FUNC-062).
	if s.reloads != nil {
		defer func() { s.reloads.ReloadAttempt(err != nil) }()
	}
	s.mu.Lock()
	if s.shutting {
		s.mu.Unlock()
		return config.ReloadResult{}, errors.New("daemon is shutting down")
	}
	oldCfg := s.config
	s.mu.Unlock()

	newCfg, warnings, err := s.loadConfig()
	if err != nil {
		return config.ReloadResult{}, fmt.Errorf("config error: %w", err)
	}
	if err := s.manager.CheckGroups(groups, newCfg); err != nil {
		return config.ReloadResult{}, err
	}
	groups = s.withTakenOver(groups, newCfg)
	merged := mergeGroups(oldCfg, newCfg, groups)
	only := make(map[string]bool, len(groups))
	for _, g := range groups {
		only[g] = true
	}
	out := s.manager.ApplyReloadGroups(oldCfg, merged, only)
	s.logger.Info("config updated", "groups", groups, "added", out.Added, "changed", out.Changed, "removed", out.Removed)
	for _, prog := range out.Stuck {
		if old, ok := oldCfg.Programs[prog]; ok {
			merged.Programs[prog] = old
		} else if old, ok := oldCfg.EventListeners[prog]; ok {
			merged.EventListeners[prog] = old
		}
	}
	for _, name := range out.StuckGroups {
		if old, ok := oldCfg.Groups[name]; ok {
			merged.Groups[name] = old
		}
	}
	s.mu.Lock()
	s.config = merged
	s.mu.Unlock()
	return config.ReloadResult{Added: out.Added, Changed: out.Changed, Removed: out.Removed, Warnings: warnings}, nil
}

// PreviewReloadGroups is ReloadGroups as a dry run (kahi ctl reread).
func (s *Supervisor) PreviewReloadGroups(groups []string) (config.ReloadResult, error) {
	if isAll(groups) {
		return s.PreviewReload()
	}
	newCfg, warnings, err := s.loadConfig()
	if err != nil {
		return config.ReloadResult{}, fmt.Errorf("config error: %w", err)
	}
	if err := s.manager.CheckGroups(groups, newCfg); err != nil {
		return config.ReloadResult{}, err
	}
	groups = s.withTakenOver(groups, newCfg)
	s.mu.Lock()
	oldCfg := s.config
	s.mu.Unlock()
	only := map[string]bool{}
	for _, g := range groups {
		only[g] = true
	}
	added, changed, removed := s.manager.PlanReloadGroups(oldCfg, mergeGroups(oldCfg, newCfg, groups), only)
	return config.ReloadResult{Added: added, Changed: changed, Removed: removed, Warnings: warnings}, nil
}

// Avail lists the programs and pools of the config file the daemon would
// load on reload, sorted by name (FUNC-085).
func (s *Supervisor) Avail() ([]config.AvailEntry, error) {
	cfg, _, err := s.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}
	var out []config.AvailEntry
	add := func(name string, p config.ProgramConfig) {
		auto := p.Autostart == nil || *p.Autostart
		out = append(out, config.AvailEntry{Name: name, Group: config.GroupOf(cfg, name), InUse: s.manager.InUse(name), Autostart: auto, Priority: p.Priority})
	}
	for name, p := range cfg.Programs {
		add(name, p)
	}
	for name, l := range cfg.EventListeners {
		add(name, l.ProgramConfig)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// withTakenOver adds to groups the standalone groups of programs that a
// named [groups] table of newCfg now owns: the table replaces them, as in a
// full reload, so they are removed with the update. Without them the table
// could not be activated while their processes run.
func (s *Supervisor) withTakenOver(groups []string, newCfg *config.Config) []string {
	out := append([]string{}, groups...)
	seen := make(map[string]bool, len(groups))
	for _, g := range groups {
		seen[g] = true
	}
	active := make(map[string]bool)
	for _, g := range s.manager.ListGroups() {
		active[g] = true
	}
	for _, g := range groups {
		t, ok := newCfg.Groups[g]
		if !ok {
			continue
		}
		for _, p := range t.Programs {
			if active[p] && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func isAll(groups []string) bool {
	return len(groups) == 0 || (len(groups) == 1 && groups[0] == "all")
}

// mergeGroups returns oldCfg with the named groups taken from newCfg: their
// table, member programs and pools replaced, or deleted when newCfg no
// longer has them.
func mergeGroups(oldCfg, newCfg *config.Config, groups []string) *config.Config {
	m := *oldCfg
	m.Programs = maps.Clone(oldCfg.Programs)
	m.EventListeners = maps.Clone(oldCfg.EventListeners)
	m.Groups = maps.Clone(oldCfg.Groups)
	if m.Programs == nil {
		m.Programs = map[string]config.ProgramConfig{}
	}
	if m.EventListeners == nil {
		m.EventListeners = map[string]config.EventListenerConfig{}
	}
	if m.Groups == nil {
		m.Groups = map[string]config.GroupConfig{}
	}
	members := func(cfg *config.Config, g string) []string {
		if t, ok := cfg.Groups[g]; ok {
			return t.Programs
		}
		return []string{g}
	}
	for _, g := range groups {
		for _, p := range members(oldCfg, g) {
			delete(m.Programs, p)
			delete(m.EventListeners, p)
		}
		delete(m.Groups, g)
		if t, ok := newCfg.Groups[g]; ok {
			m.Groups[g] = t
		}
		for _, p := range members(newCfg, g) {
			if pc, ok := newCfg.Programs[p]; ok {
				m.Programs[p] = pc
			}
			if lc, ok := newCfg.EventListeners[p]; ok {
				m.EventListeners[p] = lc
			}
		}
	}
	return &m
}
