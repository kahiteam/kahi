package process

import (
	"reflect"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

// Event listener pools run like programs (FUNC-055, ADR-042): each member is
// a Process in the group named after the pool, whose stdin and stdout belong
// to the pool's dispatcher instead of the capture.

// listenerStdinRefusal is why stdin writes to a pool member are refused.
const listenerStdinRefusal = "stdin carries the event listener protocol"

// unitConfig returns the program config of name in cfg: a [programs] entry
// or an event listener pool.
func unitConfig(cfg *config.Config, name string) config.ProgramConfig {
	if p, ok := cfg.Programs[name]; ok {
		return p
	}
	return cfg.EventListeners[name].ProgramConfig
}

// unitChanged reports whether a program or pool differs between configs;
// for a pool its own keys count too.
func unitChanged(oldCfg, newCfg *config.Config, name string) bool {
	if _, ok := newCfg.EventListeners[name]; ok {
		return !reflect.DeepEqual(oldCfg.EventListeners[name], newCfg.EventListeners[name])
	}
	return programChanged(oldCfg.Programs[name], newCfg.Programs[name])
}

// poolKeysChanged reports whether a pool's own keys (events, buffer_size,
// result_timeout) differ.
func poolKeysChanged(oldCfg, newCfg *config.Config, name string) bool {
	o, n := oldCfg.EventListeners[name], newCfg.EventListeners[name]
	return !reflect.DeepEqual(o.Events, n.Events) || o.BufferSize != n.BufferSize || o.ResultTimeout != n.ResultTimeout
}

// poolTypes returns the event types a pool subscribes to: its events
// without unknown names (those were warned about at load) and duplicates.
func poolTypes(l config.EventListenerConfig) []events.EventType {
	known := make(map[events.EventType]bool, len(events.AllEventTypes))
	for _, t := range events.AllEventTypes {
		known[t] = true
	}
	seen := map[events.EventType]bool{}
	var out []events.EventType
	for _, e := range l.Events {
		t := events.EventType(e)
		if known[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func (m *Manager) poolSettings(name string, l config.EventListenerConfig) events.PoolConfig {
	return events.PoolConfig{
		Name:          name,
		Types:         poolTypes(l),
		BufferSize:    l.BufferSize,
		ResultTimeout: time.Duration(l.ResultTimeout) * time.Second,
		Logger:        m.logger,
		OnTimeout:     m.listenerTimedOut,
	}
}

// createUnitLocked creates the processes of a program or an event listener
// pool, in group, and returns their names. A pool keeps its dispatcher and
// buffered events when its members are recreated (a reload). Callers hold
// m.mu.
func (m *Manager) createUnitLocked(name string, cfg *config.Config, group string) []string {
	l, ok := cfg.EventListeners[name]
	if !ok || m.bus == nil {
		return m.createProgramLocked(name, unitConfig(cfg, name), group, nil)
	}
	settings := m.poolSettings(name, l)
	pool := m.pools[name]
	if pool == nil {
		pool = events.NewPool(m.bus, settings)
		m.pools[name] = pool
	} else {
		pool.Reconfigure(settings)
	}
	return m.createProgramLocked(name, l.ProgramConfig, group, pool)
}

// stopPoolLocked ends a removed pool's dispatcher; its buffered events are
// discarded (FUNC-055). Callers hold m.mu.
func (m *Manager) stopPoolLocked(name string) {
	if pool := m.pools[name]; pool != nil {
		pool.Stop()
		delete(m.pools, name)
	}
}

// releasePoolMembers gives the events that procs hold back to their pools
// without counting a failure, before a reload stops them.
func (m *Manager) releasePoolMembers(procs []*Process) {
	m.mu.RLock()
	pools := make(map[string]*events.Pool, len(procs))
	for _, p := range procs {
		if pool := m.pools[p.Group()]; pool != nil {
			pools[p.Name()] = pool
		}
	}
	m.mu.RUnlock()
	for name, pool := range pools {
		pool.Release(name)
	}
}

// listenerTimedOut stops a member that did not answer within result_timeout
// with its stopsignal, and SIGKILLs it after stopwaitsecs if the same child
// still runs; the exit is handled like any exit, so autorestart applies
// (FUNC-055).
func (m *Manager) listenerTimedOut(member string) {
	m.mu.RLock()
	p := m.processes[member]
	m.mu.RUnlock()
	if p == nil {
		return
	}
	pid := p.Pid()
	if pid == 0 {
		return
	}
	cfg := p.Config()
	sig := cfg.Stopsignal
	if sig == "" {
		sig = "TERM"
	}
	if err := p.Signal(sig); err != nil {
		m.logger.Error("stop timed-out listener", "process", member, "error", err)
		return
	}
	time.AfterFunc(time.Duration(cfg.Stopwaitsecs)*time.Second, func() {
		if p.Pid() == pid {
			p.Kill()
		}
	})
}
