package process

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
	"github.com/kahiteam/kahi/internal/logging"
)

// Manager manages all processes and groups. It implements api.ProcessManager
// and api.GroupManager.
type Manager struct {
	mu         sync.RWMutex
	processes  map[string]*Process
	groups     map[string]*Group
	captures   map[string]*logging.CaptureWriter
	bus        *events.Bus
	logger     *slog.Logger
	spawner    ProcessSpawner
	shutdownCh chan struct{}
}

// Group holds a named set of processes.
type Group struct {
	Name      string
	Processes []string // process names
	Priority  int
}

// NewManager creates a process manager.
func NewManager(spawner ProcessSpawner, bus *events.Bus, logger *slog.Logger) *Manager {
	return &Manager{
		processes:  make(map[string]*Process),
		groups:     make(map[string]*Group),
		captures:   make(map[string]*logging.CaptureWriter),
		bus:        bus,
		logger:     logger,
		spawner:    spawner,
		shutdownCh: make(chan struct{}),
	}
}

// ShutdownCh returns the shutdown channel.
func (m *Manager) ShutdownCh() chan struct{} { return m.shutdownCh }

// LoadConfig creates processes and groups from a parsed config.
func (m *Manager) LoadConfig(cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for progName, progCfg := range cfg.Programs {
		instances := ExpandNumprocs(progName, progCfg)
		var procNames []string

		for _, inst := range instances {
			if _, exists := m.processes[inst.Name]; exists {
				continue
			}

			// Create capture writers for stdout (and stderr if not redirected).
			stdoutCW, err := logging.NewCaptureWriter(logging.CaptureConfig{
				ProcessName: inst.Name,
				Stream:      "stdout",
				Logfile:     inst.Config.StdoutLogfile,
				StripAnsi:   inst.Config.StripAnsi,
				MaxBytes:    inst.Config.StdoutLogfileMaxbytes,
				Backups:     inst.Config.StdoutLogfileBackups,
				Logger:      m.logger,
			})
			if err != nil {
				m.logger.Error("create stdout capture", "process", inst.Name, "error", err)
			}
			m.captures[inst.Name+":stdout"] = stdoutCW

			var stderrCW *logging.CaptureWriter
			if !inst.Config.RedirectStderr {
				stderrCW, err = logging.NewCaptureWriter(logging.CaptureConfig{
					ProcessName: inst.Name,
					Stream:      "stderr",
					Logfile:     inst.Config.StderrLogfile,
					StripAnsi:   inst.Config.StripAnsi,
					MaxBytes:    inst.Config.StderrLogfileMaxbytes,
					Backups:     inst.Config.StderrLogfileBackups,
					Logger:      m.logger,
				})
				if err != nil {
					m.logger.Error("create stderr capture", "process", inst.Name, "error", err)
				}
				m.captures[inst.Name+":stderr"] = stderrCW
			}

			bus := m.bus
			opts := []ProcessOption{WithShutdownCh(m.shutdownCh)}
			if stdoutCW != nil {
				cw := stdoutCW
				opts = append(opts, WithStdoutHandler(func(name string, data []byte) {
					_, _ = cw.Write(data)
					if bus != nil {
						bus.Publish(events.Event{
							Type: events.ProcessLogStdout,
							Data: map[string]string{"name": name, "data": string(data)},
						})
					}
				}))
			}
			if stderrCW != nil {
				cw := stderrCW
				redirect := inst.Config.RedirectStderr
				opts = append(opts, WithStderrHandler(func(name string, data []byte) {
					if redirect && stdoutCW != nil {
						_, _ = stdoutCW.Write(data)
					} else {
						_, _ = cw.Write(data)
					}
					if bus != nil {
						bus.Publish(events.Event{
							Type: events.ProcessLogStderr,
							Data: map[string]string{"name": name, "data": string(data)},
						})
					}
				}))
			}

			p := NewProcess(
				inst.Name, inst.Group, inst.Config,
				m.spawner, m.bus, m.logger,
				opts...,
			)
			m.processes[inst.Name] = p
			procNames = append(procNames, inst.Name)
		}

		// Create implicit homogeneous group.
		if _, exists := m.groups[progName]; !exists {
			m.groups[progName] = &Group{
				Name:      progName,
				Processes: procNames,
				Priority:  progCfg.Priority,
			}
		}
	}

	// Create explicit heterogeneous groups.
	for groupName, groupCfg := range cfg.Groups {
		var allProcs []string
		for _, progName := range groupCfg.Programs {
			if g, ok := m.groups[progName]; ok {
				allProcs = append(allProcs, g.Processes...)
			}
		}
		m.groups[groupName] = &Group{
			Name:      groupName,
			Processes: allProcs,
			Priority:  groupCfg.Priority,
		}
	}
}

// ProcessInstance holds expanded process info from numprocs.
type ProcessInstance struct {
	Name   string
	Group  string
	Config config.ProgramConfig
}

// ExpandNumprocs expands a program config with numprocs > 1 into individual instances.
func ExpandNumprocs(progName string, cfg config.ProgramConfig) []ProcessInstance {
	if cfg.Numprocs <= 1 {
		name := progName
		// Use expanded ProcessName if set and different from the program key.
		if cfg.ProcessName != "" && cfg.ProcessName != progName {
			name = cfg.ProcessName
		}
		return []ProcessInstance{{
			Name:   name,
			Group:  progName,
			Config: cfg,
		}}
	}

	instances := make([]ProcessInstance, 0, cfg.Numprocs)
	for i := cfg.NumprocsStart; i < cfg.NumprocsStart+cfg.Numprocs; i++ {
		name := expandProcessName(cfg.ProcessName, progName, i, cfg.Numprocs)
		if name == "" {
			name = fmt.Sprintf("%s_%d", progName, i)
		}
		instanceCfg := cfg
		instances = append(instances, ProcessInstance{
			Name:   name,
			Group:  progName,
			Config: instanceCfg,
		})
	}
	return instances
}

func expandProcessName(template, progName string, num, numprocs int) string {
	if template == "" {
		return ""
	}
	result := template
	result = expandVar(result, "%(program_name)s", progName)
	result = expandVar(result, "%(process_num)d", fmt.Sprintf("%d", num))
	result = expandVar(result, "%(group_name)s", progName)
	result = expandVar(result, "%(numprocs)d", fmt.Sprintf("%d", numprocs))
	return result
}

func expandVar(s, pattern, value string) string {
	for {
		idx := indexOf(s, pattern)
		if idx < 0 {
			return s
		}
		s = s[:idx] + value + s[idx+len(pattern):]
	}
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// AutostartAll starts all processes with autostart=true in priority order.
func (m *Manager) AutostartAll() {
	m.mu.RLock()
	procs := m.sortedProcesses(true)
	m.mu.RUnlock()

	for _, p := range procs {
		if p.Config().Autostart != nil && !*p.Config().Autostart {
			continue
		}
		if err := p.Start(); err != nil {
			m.logger.Error("autostart failed", "process", p.Name(), "error", err)
		}
	}
}

// StopAll stops all processes in reverse priority order.
func (m *Manager) StopAll() {
	close(m.shutdownCh)

	m.mu.RLock()
	procs := m.sortedProcesses(false)
	m.mu.RUnlock()

	for _, p := range procs {
		if p.State() == Running || p.State() == Starting {
			if err := p.Stop(); err != nil {
				m.logger.Error("stop failed", "process", p.Name(), "error", err)
			}
		}
	}
}

// sortedProcesses returns processes sorted by priority.
// ascending=true for start order, false for stop order.
func (m *Manager) sortedProcesses(ascending bool) []*Process {
	procs := make([]*Process, 0, len(m.processes))
	for _, p := range m.processes {
		procs = append(procs, p)
	}

	sort.Slice(procs, func(i, j int) bool {
		pi := procs[i].Config().Priority
		pj := procs[j].Config().Priority
		if pi != pj {
			if ascending {
				return pi < pj
			}
			return pi > pj
		}
		if ascending {
			return procs[i].Name() < procs[j].Name()
		}
		return procs[i].Name() > procs[j].Name()
	})

	return procs
}

// GetProcess returns a process by name.
func (m *Manager) GetProcess(name string) (*Process, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.processes[name]
	if !ok {
		return nil, fmt.Errorf("no such process: %s", name)
	}
	return p, nil
}

// ProcessByPid finds a process by its PID.
func (m *Manager) ProcessByPid(pid int) *Process {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.processes {
		if p.Pid() == pid {
			return p
		}
	}
	return nil
}

// Processes returns all managed processes.
func (m *Manager) Processes() []*Process {
	m.mu.RLock()
	defer m.mu.RUnlock()

	procs := make([]*Process, 0, len(m.processes))
	for _, p := range m.processes {
		procs = append(procs, p)
	}
	return procs
}

// Groups returns all managed groups.
func (m *Manager) Groups() map[string]*Group {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*Group, len(m.groups))
	for k, v := range m.groups {
		result[k] = v
	}
	return result
}

// AddGroup adds a group at runtime.
func (m *Manager) AddGroup(name string, procs []string, priority int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.groups[name]; exists {
		return fmt.Errorf("group %s already exists", name)
	}

	m.groups[name] = &Group{
		Name:      name,
		Processes: procs,
		Priority:  priority,
	}

	if m.bus != nil {
		m.bus.Publish(events.Event{
			Type: events.ProcessGroupAdded,
			Data: map[string]string{"group": name},
		})
	}
	return nil
}

// RemoveGroup removes a group at runtime.
func (m *Manager) RemoveGroup(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, exists := m.groups[name]
	if !exists {
		return fmt.Errorf("no such group: %s", name)
	}

	// Check all processes are stopped.
	for _, pName := range g.Processes {
		if p, ok := m.processes[pName]; ok {
			if p.State() == Running || p.State() == Starting {
				return fmt.Errorf("cannot remove group %s: process %s is still running", name, pName)
			}
		}
	}

	// Remove processes belonging to this group.
	for _, pName := range g.Processes {
		delete(m.processes, pName)
	}
	delete(m.groups, name)

	if m.bus != nil {
		m.bus.Publish(events.Event{
			Type: events.ProcessGroupRemoved,
			Data: map[string]string{"group": name},
		})
	}
	return nil
}

// AddProcess adds a process at runtime.
func (m *Manager) AddProcess(name string, p *Process) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processes[name] = p
}

// RemoveProcess removes a process at runtime.
func (m *Manager) RemoveProcess(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processes, name)
}

// --- api.ProcessManager implementation ---

// List returns info for all processes.
func (m *Manager) List() []api.ProcessInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]api.ProcessInfo, 0, len(m.processes))
	for _, p := range m.processes {
		infos = append(infos, m.processInfo(p))
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos
}

// Get returns info for a single process.
func (m *Manager) Get(name string) (api.ProcessInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.processes[name]
	if !ok {
		return api.ProcessInfo{}, fmt.Errorf("no such process: %s", name)
	}
	return m.processInfo(p), nil
}

// Start starts a process by name.
func (m *Manager) Start(name string) error {
	p, err := m.GetProcess(name)
	if err != nil {
		return err
	}
	return p.Start()
}

// Stop stops a process by name.
func (m *Manager) Stop(name string) error {
	p, err := m.GetProcess(name)
	if err != nil {
		return err
	}
	return p.Stop()
}

// Restart restarts a process by name.
func (m *Manager) Restart(name string) error {
	p, err := m.GetProcess(name)
	if err != nil {
		return err
	}

	state := p.State()
	if state == Running || state == Starting {
		if err := p.Stop(); err != nil {
			return err
		}
		m.waitForStopped(p, 30*time.Second)
	}
	return p.Start()
}

// waitForStopped polls until the process reaches a terminal state.
func (m *Manager) waitForStopped(p *Process, timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		state := p.State()
		if state == Stopped || state == Exited || state == Fatal {
			return
		}
		select {
		case <-deadline:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Signal sends a signal to a process.
func (m *Manager) Signal(name string, sig string) error {
	p, err := m.GetProcess(name)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

// WriteStdin writes to a process stdin.
func (m *Manager) WriteStdin(name string, data []byte) error {
	p, err := m.GetProcess(name)
	if err != nil {
		return err
	}
	return p.WriteStdin(data)
}

// ReadLog reads from process log ring buffer.
func (m *Manager) ReadLog(name string, stream string, offset int64, length int) ([]byte, error) {
	_, err := m.GetProcess(name)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	cw, ok := m.captures[name+":"+stream]
	m.mu.RUnlock()
	if !ok || cw == nil {
		return []byte{}, nil
	}
	return cw.ReadTail(length), nil
}

// --- api.GroupManager implementation ---

// ListGroups returns all group names.
func (m *Manager) ListGroups() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.groups))
	for name := range m.groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// StartGroup starts all processes in a group.
func (m *Manager) StartGroup(name string) error {
	m.mu.RLock()
	g, ok := m.groups[name]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("no such group: %s", name)
	}
	procs := m.groupProcessesSorted(g, true)
	m.mu.RUnlock()

	for _, p := range procs {
		if err := p.Start(); err != nil {
			m.logger.Error("group start failed", "group", name, "process", p.Name(), "error", err)
		}
	}
	return nil
}

// StopGroup stops all processes in a group.
func (m *Manager) StopGroup(name string) error {
	m.mu.RLock()
	g, ok := m.groups[name]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("no such group: %s", name)
	}
	procs := m.groupProcessesSorted(g, false)
	m.mu.RUnlock()

	for _, p := range procs {
		if p.State() == Running || p.State() == Starting {
			if err := p.Stop(); err != nil {
				m.logger.Error("group stop failed", "group", name, "process", p.Name(), "error", err)
			}
		}
	}
	return nil
}

// RestartGroup restarts all processes in a group.
func (m *Manager) RestartGroup(name string) error {
	if err := m.StopGroup(name); err != nil {
		return err
	}

	// Wait for all group processes to reach a terminal state.
	m.mu.RLock()
	g, ok := m.groups[name]
	if ok {
		for _, pName := range g.Processes {
			if p, exists := m.processes[pName]; exists {
				m.mu.RUnlock()
				m.waitForStopped(p, 30*time.Second)
				m.mu.RLock()
			}
		}
	}
	m.mu.RUnlock()

	return m.StartGroup(name)
}

func (m *Manager) groupProcessesSorted(g *Group, ascending bool) []*Process {
	procs := make([]*Process, 0, len(g.Processes))
	for _, pName := range g.Processes {
		if p, ok := m.processes[pName]; ok {
			procs = append(procs, p)
		}
	}

	sort.Slice(procs, func(i, j int) bool {
		pi := procs[i].Config().Priority
		pj := procs[j].Config().Priority
		if pi != pj {
			if ascending {
				return pi < pj
			}
			return pi > pj
		}
		if ascending {
			return procs[i].Name() < procs[j].Name()
		}
		return procs[i].Name() > procs[j].Name()
	})

	return procs
}

func (m *Manager) processInfo(p *Process) api.ProcessInfo {
	return api.ProcessInfo{
		Name:        p.Name(),
		Group:       p.Group(),
		State:       p.State().String(),
		StateCode:   int(p.State()),
		PID:         p.Pid(),
		Uptime:      p.Uptime(),
		Description: p.Config().Description,
		ExitStatus:  p.ExitCode(),
	}
}

// ConfigDiff computes added, changed, and removed programs between configs.
func ConfigDiff(old, new *config.Config) (added, changed, removed []string) {
	for name := range new.Programs {
		if _, exists := old.Programs[name]; !exists {
			added = append(added, name)
		}
	}

	for name := range old.Programs {
		if _, exists := new.Programs[name]; !exists {
			removed = append(removed, name)
		}
	}

	for name, newCfg := range new.Programs {
		if oldCfg, exists := old.Programs[name]; exists {
			if programChanged(oldCfg, newCfg) {
				changed = append(changed, name)
			}
		}
	}

	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	return
}

func programChanged(a, b config.ProgramConfig) bool {
	return a.Command != b.Command ||
		a.Numprocs != b.Numprocs ||
		a.Priority != b.Priority ||
		a.Startsecs != b.Startsecs ||
		a.Startretries != b.Startretries ||
		a.Stopsignal != b.Stopsignal ||
		a.Stopwaitsecs != b.Stopwaitsecs ||
		a.Autorestart != b.Autorestart ||
		a.Directory != b.Directory ||
		a.User != b.User ||
		a.Umask != b.Umask
}
