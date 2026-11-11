package process

import (
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"
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

	// groupPriority maps a program to the priority of the [groups] table it
	// belongs to; ordering puts it first (#196). Rebuilt whenever a group is
	// activated, deactivated or reprioritised.
	groupPriority map[string]int

	// The daemon's own stdout and stderr, which receive the output of
	// streams that have no log file, as JSON lines (FUNC-018). passMu keeps
	// lines from different processes whole.
	passMu  sync.Mutex
	passOut io.Writer
	passErr io.Writer
}

// Group holds a named set of processes: a [groups] table, which owns its
// member programs, or a program that no table lists (ADR-034).
type Group struct {
	Name      string
	Processes []string // process names, sorted
	Priority  int
	Programs  []string // member programs, sorted
	Table     bool     // a [groups] table rather than a program's own group
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

// SetOutput sets the writers that receive, as JSON lines, the output of
// process streams without a log file: stdout streams go to stdout, stderr
// streams to stderr. It must be called before LoadConfig. With no writers,
// such output is kept only in the capture buffer.
func (m *Manager) SetOutput(stdout, stderr io.Writer) {
	m.passOut, m.passErr = stdout, stderr
}

// passthroughHandler returns the line handler that forwards a process's
// output streams without a log file to the daemon's stdout and stderr, or
// nil when no stream of cfg is forwarded. With redirect_stderr, stderr
// lines are part of the stdout stream.
func (m *Manager) passthroughHandler(cfg config.ProgramConfig) func(name, stream string, line []byte) {
	toStdout := cfg.StdoutLogfile == "" && m.passOut != nil
	toStderr := cfg.StderrLogfile == "" && !cfg.RedirectStderr && m.passErr != nil
	if !toStdout && !toStderr {
		return nil
	}
	redirect, strip := cfg.RedirectStderr, cfg.StripAnsi
	return func(name, stream string, line []byte) {
		if redirect {
			stream = "stdout"
		}
		var w io.Writer
		switch {
		case stream == "stdout" && toStdout:
			w = m.passOut
		case stream == "stderr" && toStderr:
			w = m.passErr
		default:
			return
		}
		if strip {
			line = logging.StripANSI(line)
		}
		out := logging.FormatJSONLine(name, stream, string(line))
		m.passMu.Lock()
		_, _ = w.Write(out)
		m.passMu.Unlock()
	}
}

// ShutdownCh returns the shutdown channel.
func (m *Manager) ShutdownCh() chan struct{} { return m.shutdownCh }

// LoadConfig creates processes and groups from a parsed config.
func (m *Manager) LoadConfig(cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	specs := effectiveGroups(cfg)
	for _, name := range sortedNames(specs) {
		if _, active := m.groups[name]; active {
			continue
		}
		m.activateLocked(name, specs[name], cfg)
	}
	m.rebuildGroupPriorityLocked()
}

// activateLocked creates the processes of a group's member programs and the
// group itself, and returns the process names. Callers hold m.mu and make
// sure no member program has processes yet.
func (m *Manager) activateLocked(name string, spec groupSpec, cfg *config.Config) []string {
	var procs []string
	for _, prog := range spec.Programs {
		procs = append(procs, m.createProgramLocked(prog, cfg.Programs[prog], name)...)
	}
	sort.Strings(procs)
	m.groups[name] = &Group{
		Name:      name,
		Processes: procs,
		Priority:  spec.Priority,
		Programs:  spec.Programs,
		Table:     spec.Table,
	}
	return procs
}

// deactivateLocked deletes a group, its processes and their capture writers,
// and returns the process names. Callers hold m.mu; the processes are
// stopped and retired.
func (m *Manager) deactivateLocked(name string) []string {
	g := m.groups[name]
	if g == nil {
		return nil
	}
	for _, procName := range g.Processes {
		m.deleteProcessLocked(procName)
	}
	delete(m.groups, name)
	return g.Processes
}

// deleteProcessLocked removes one process and closes its capture writers.
func (m *Manager) deleteProcessLocked(name string) {
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

// rebuildGroupPriorityLocked recomputes groupPriority from the active
// [groups] tables.
func (m *Manager) rebuildGroupPriorityLocked() {
	m.groupPriority = make(map[string]int)
	for _, g := range m.groups {
		if !g.Table {
			continue
		}
		for _, prog := range g.Programs {
			m.groupPriority[prog] = g.Priority
		}
	}
}

// publishGroup sends a group event listing the group's processes, sorted
// and comma-separated (FUNC-053).
func (m *Manager) publishGroup(t events.EventType, name string, procs []string) {
	if m.bus == nil {
		return
	}
	sorted := append([]string(nil), procs...)
	sort.Strings(sorted)
	m.bus.Publish(events.Event{
		Type: t,
		Data: map[string]string{"group": name, "processes": strings.Join(sorted, ",")},
	})
}

// createProgramLocked creates the processes of one program, in group, with
// their capture writers, and returns their names. Callers hold m.mu.
func (m *Manager) createProgramLocked(progName string, progCfg config.ProgramConfig, group string) []string {
	var procNames []string
	for _, inst := range ExpandNumprocs(progName, progCfg) {
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
		// A log file that cannot be opened fails each start until it can
		// (FUNC-018, #171); the writer still keeps the ring buffer.
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
		name := inst.Name
		opts := []ProcessOption{
			WithShutdownCh(m.shutdownCh),
			WithProgram(progName),
			WithPreSpawn(func() error {
				for _, cw := range []*logging.CaptureWriter{stdoutCW, stderrCW} {
					if cw == nil {
						continue
					}
					if err := cw.EnsureOpen(); err != nil {
						return fmt.Errorf("cannot open log file for %s: %w", name, err)
					}
				}
				return nil
			}),
		}
		if stdoutCW != nil {
			cw := stdoutCW
			opts = append(opts, WithStdoutHandler(func(name string, data []byte) {
				data = cw.Capture(data)
				if bus != nil && len(data) > 0 {
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
					data = stdoutCW.Capture(data)
				} else {
					data = cw.Capture(data)
				}
				if bus != nil && len(data) > 0 {
					bus.Publish(events.Event{
						Type: events.ProcessLogStderr,
						Data: map[string]string{"name": name, "data": string(data)},
					})
				}
			}))
		}

		if h := m.passthroughHandler(inst.Config); h != nil {
			opts = append(opts, WithLineHandler(h))
		}

		p := NewProcess(
			inst.Name, group, inst.Config,
			m.spawner, m.bus, m.logger,
			opts...,
		)
		m.processes[inst.Name] = p
		procNames = append(procNames, inst.Name)
	}
	return procNames
}

// ProcessInstance holds expanded process info from numprocs.
type ProcessInstance struct {
	Name   string
	Group  string
	Config config.ProgramConfig
}

// ExpandNumprocs expands a program config with numprocs > 1 into individual
// instances, named by config.InstanceNames.
func ExpandNumprocs(progName string, cfg config.ProgramConfig) []ProcessInstance {
	names := config.InstanceNames(progName, cfg)
	if cfg.Numprocs <= 1 {
		return []ProcessInstance{{
			Name:   names[0],
			Group:  progName,
			Config: cfg,
		}}
	}

	instances := make([]ProcessInstance, 0, len(names))
	for k, name := range names {
		instances = append(instances, ProcessInstance{
			Name:   name,
			Group:  progName,
			Config: instanceConfig(cfg, cfg.NumprocsStart+k),
		})
	}
	return instances
}

// instanceConfig returns one instance's config: config loading leaves
// %(process_num)d in place for numprocs > 1, so each instance substitutes its
// own number into the fields that may use it. Environment is copied so the
// instances do not share one map.
func instanceConfig(cfg config.ProgramConfig, num int) config.ProgramConfig {
	n := fmt.Sprintf("%d", num)
	sub := func(s string) string { return expandVar(s, "%(process_num)d", n) }
	cfg.Command = sub(cfg.Command)
	cfg.Directory = sub(cfg.Directory)
	cfg.StdoutLogfile = sub(cfg.StdoutLogfile)
	cfg.StderrLogfile = sub(cfg.StderrLogfile)
	cfg.User = sub(cfg.User)
	if cfg.Environment != nil {
		env := make(map[string]string, len(cfg.Environment))
		for k, v := range cfg.Environment {
			env[k] = sub(v)
		}
		cfg.Environment = env
	}
	return cfg
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
		if stoppable(p.State()) {
			if err := p.Stop(); err != nil {
				m.logger.Error("stop failed", "process", p.Name(), "error", err)
			}
		}
	}
}

// sortedProcesses returns processes in priority order (sortByPriority).
// ascending=true for start order, false for stop order. Callers hold m.mu.
func (m *Manager) sortedProcesses(ascending bool) []*Process {
	procs := make([]*Process, 0, len(m.processes))
	for _, p := range m.processes {
		procs = append(procs, p)
	}
	m.sortByPriority(procs, ascending)
	return procs
}

// sortByPriority orders processes for every ordered action: by the priority
// of their [groups] table if they are in one, else their own priority; then
// by their own priority; then by name. Descending reverses all three.
// Callers hold m.mu.
func (m *Manager) sortByPriority(procs []*Process, ascending bool) {
	key := func(p *Process) (int, int, string) {
		own := p.Config().Priority
		if gp, ok := m.groupPriority[p.Program()]; ok {
			return gp, own, p.Name()
		}
		return own, own, p.Name()
	}
	sort.Slice(procs, func(i, j int) bool {
		g1, o1, n1 := key(procs[i])
		g2, o2, n2 := key(procs[j])
		var less bool
		switch {
		case g1 != g2:
			less = g1 < g2
		case o1 != o2:
			less = o1 < o2
		default:
			less = n1 < n2
		}
		if !ascending {
			return !less && (g1 != g2 || o1 != o2 || n1 != n2)
		}
		return less
	})
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

// ActivateGroup adds a group that cfg defines but that is not active, starts
// its autostart processes and publishes PROCESS_GROUP_ADDED (FUNC-011,
// ADR-034). cfg is the config the daemon last loaded.
func (m *Manager) ActivateGroup(name string, cfg *config.Config) error {
	spec, ok := effectiveGroups(cfg)[name]
	if !ok {
		return unknownGroup(name, cfg)
	}
	m.mu.Lock()
	if _, active := m.groups[name]; active {
		m.mu.Unlock()
		return fmt.Errorf("group already active: %s", name)
	}
	for _, prog := range spec.Programs {
		if len(m.programProcessesLocked(prog)) > 0 {
			m.mu.Unlock()
			return fmt.Errorf("group %s: program %s is still active", name, prog)
		}
	}
	procs := m.activateLocked(name, spec, cfg)
	m.rebuildGroupPriorityLocked()
	m.mu.Unlock()

	m.publishGroup(events.ProcessGroupAdded, name, procs)
	for _, prog := range spec.Programs {
		m.startProgram(prog, true)
	}
	return nil
}

// DeactivateGroup removes an active group whose processes are all STOPPED,
// EXITED or FATAL and publishes PROCESS_GROUP_REMOVED (FUNC-011, ADR-034).
// With stop, it first stops the group's processes and waits up to each
// one's stopwaitsecs plus stopWaitMargin (kahi ctl remove --stop). Its
// config stays; ActivateGroup or a reload brings it back.
func (m *Manager) DeactivateGroup(name string, cfg *config.Config, stop bool) error {
	m.mu.Lock()
	g, ok := m.groups[name]
	if !ok {
		m.mu.Unlock()
		return unknownGroup(name, cfg)
	}
	procs := m.groupProcessesSorted(g, false)
	m.mu.Unlock()

	// Retire first, so a queued autorestart or backoff retry cannot start a
	// process between the check and the removal; undo it if one is running.
	for _, p := range procs {
		p.Retire()
	}
	if stop {
		for _, p := range procs {
			if stoppable(p.State()) {
				if err := p.Stop(); err != nil {
					m.logger.Error("group remove: stop failed", "group", name, "process", p.Name(), "error", err)
				}
			}
		}
		for _, p := range procs {
			wait := time.Duration(p.Config().Stopwaitsecs+stopWaitMargin) * time.Second
			if !m.waitForStopped(p, wait) {
				for _, q := range procs {
					q.unretire()
				}
				return fmt.Errorf("group %s: process %s did not stop", name, p.Name())
			}
		}
	}
	for _, p := range procs {
		if !removable(p.State()) {
			for _, q := range procs {
				q.unretire()
			}
			return fmt.Errorf("group %s has running processes, stop first", name)
		}
	}

	m.mu.Lock()
	removed := m.deactivateLocked(name)
	m.rebuildGroupPriorityLocked()
	m.mu.Unlock()
	m.publishGroup(events.ProcessGroupRemoved, name, removed)
	return nil
}

// removable reports whether a group with a process in this state can be
// removed: only STOPPED, EXITED and FATAL count as stopped (FUNC-011).
func removable(s State) bool {
	return s == Stopped || s == Exited || s == Fatal
}

// unknownGroup is the error for a name that is not a group of cfg: a member
// program of a [groups] table is addressed through its table (ADR-034).
func unknownGroup(name string, cfg *config.Config) error {
	if table := tableOf(cfg, name); table != "" {
		return fmt.Errorf("%s is in group %s; use %s", name, table, table)
	}
	return fmt.Errorf("no such group: %s", name)
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

// ReopenLogs reopens every capture log file, for logrotate setups that rename
// the file and then send SIGUSR2. A failure is logged and the remaining
// writers are still reopened.
func (m *Manager) ReopenLogs() {
	for key, cw := range m.captureWriters() {
		if err := cw.Reopen(); err != nil {
			m.logger.Error("log reopen failed", "capture", key, "error", err)
		}
	}
}

// CloseLogs closes every capture log file. The daemon calls it last during
// shutdown.
func (m *Manager) CloseLogs() {
	for key, cw := range m.captureWriters() {
		if err := cw.Close(); err != nil {
			m.logger.Warn("capture writer close failed during shutdown", "capture", key, "error", err)
		}
	}
}

// captureWriters returns a snapshot of the non-nil capture writers, so they
// can be reopened or closed without holding m.mu during file I/O.
func (m *Manager) captureWriters() map[string]*logging.CaptureWriter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*logging.CaptureWriter, len(m.captures))
	for key, cw := range m.captures {
		if cw != nil {
			out[key] = cw
		}
	}
	return out
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

// waitForStopped polls until the process reaches a terminal state. It reports
// false if the timeout expired first.
func (m *Manager) waitForStopped(p *Process, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		state := p.State()
		if state == Stopped || state == Exited || state == Fatal {
			return true
		}
		select {
		case <-deadline:
			return false
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
	procs, err := m.targets(name, true)
	if err != nil {
		return err
	}

	for _, p := range procs {
		if err := p.Start(); err != nil {
			m.logger.Error("group start failed", "group", name, "process", p.Name(), "error", err)
		}
	}
	return nil
}

// StopGroup stops all processes in a group.
func (m *Manager) StopGroup(name string) error {
	procs, err := m.targets(name, false)
	if err != nil {
		return err
	}

	for _, p := range procs {
		if stoppable(p.State()) {
			if err := p.Stop(); err != nil {
				m.logger.Error("group stop failed", "group", name, "process", p.Name(), "error", err)
			}
		}
	}
	return nil
}

// stoppable reports whether a bulk stop must stop a process in this state.
// Backoff counts: its pending retry would otherwise spawn it again.
func stoppable(s State) bool {
	return s == Running || s == Starting || s == Backoff
}

// RestartGroup restarts all processes in a group.
func (m *Manager) RestartGroup(name string) error {
	if err := m.StopGroup(name); err != nil {
		return err
	}

	// Wait for all group processes to reach a terminal state.
	procs, err := m.targets(name, false)
	if err != nil {
		return err
	}
	for _, p := range procs {
		m.waitForStopped(p, 30*time.Second)
	}

	return m.StartGroup(name)
}

// targets resolves name:* to processes in priority order: the group of that
// name, else the processes of the program of that name, so web:* keeps
// working for a member of a [groups] table (ADR-034).
func (m *Manager) targets(name string, ascending bool) ([]*Process, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var procs []*Process
	if g, ok := m.groups[name]; ok {
		procs = m.groupProcessesSorted(g, ascending)
	} else {
		procs = m.programProcessesLocked(name)
		m.sortByPriority(procs, ascending)
	}
	if len(procs) == 0 {
		return nil, fmt.Errorf("no such group: %s", name)
	}
	return procs, nil
}

func (m *Manager) groupProcessesSorted(g *Group, ascending bool) []*Process {
	procs := make([]*Process, 0, len(g.Processes))
	for _, pName := range g.Processes {
		if p, ok := m.processes[pName]; ok {
			procs = append(procs, p)
		}
	}
	m.sortByPriority(procs, ascending)
	return procs
}

func (m *Manager) processInfo(p *Process) api.ProcessInfo {
	state := p.State()
	desc := p.Config().Description
	// A process that could not be spawned shows why, as supervisord does.
	if spawnErr := p.SpawnError(); spawnErr != "" && (state == Fatal || state == Backoff) {
		desc = spawnErr
	}
	return api.ProcessInfo{
		Name:        p.Name(),
		Group:       p.Group(),
		State:       state.String(),
		StateCode:   int(state),
		PID:         p.Pid(),
		Uptime:      p.Uptime(),
		Description: desc,
		ExitStatus:  p.ExitCode(),
		ExitSignal:  p.ExitSignal(),
	}
}

// ConfigDiff computes the groups added, changed and removed between two
// configs. A group is a [groups] table or a program that no table lists; it
// is changed when its member list, its priority or any member program's
// config differs (ADR-034).
func ConfigDiff(old, new *config.Config) (added, changed, removed []string) {
	og, ng := effectiveGroups(old), effectiveGroups(new)
	for name, n := range ng {
		o, ok := og[name]
		switch {
		case !ok:
			added = append(added, name)
		case groupChanged(o, n, old, new):
			changed = append(changed, name)
		}
	}
	for name := range og {
		if _, ok := ng[name]; !ok {
			removed = append(removed, name)
		}
	}

	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	return
}

func groupChanged(o, n groupSpec, old, new *config.Config) bool {
	if !reflect.DeepEqual(o, n) {
		return true
	}
	for _, prog := range n.Programs {
		if programChanged(old.Programs[prog], new.Programs[prog]) {
			return true
		}
	}
	return false
}

// programChanged compares every field: environment, logging and autostart
// changes must restart the program too. ProgramConfig holds a map, a slice
// and a *bool that are fresh on every load, so compare by value.
func programChanged(a, b config.ProgramConfig) bool {
	return !reflect.DeepEqual(a, b)
}
