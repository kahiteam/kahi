package config

import (
	"fmt"
	"sort"
	"strings"
)

// InstanceNames returns the process names a program expands to. With
// numprocs > 1 there is one name per instance: process_name with each
// instance's %(process_num)d, or <program>_<n> when process_name is unset.
// process.ExpandNumprocs names its instances with it, so validation checks
// the names the daemon actually uses.
func InstanceNames(program string, p ProgramConfig) []string {
	if p.Numprocs <= 1 {
		if p.ProcessName != "" && p.ProcessName != program {
			return []string{p.ProcessName}
		}
		return []string{program}
	}
	names := make([]string, 0, p.Numprocs)
	for i := p.NumprocsStart; i < p.NumprocsStart+p.Numprocs; i++ {
		names = append(names, instanceName(program, p, i))
	}
	return names
}

func instanceName(program string, p ProgramConfig, num int) string {
	if p.ProcessName == "" {
		return fmt.Sprintf("%s_%d", program, num)
	}
	return strings.NewReplacer(
		"%(program_name)s", program,
		"%(process_num)d", fmt.Sprintf("%d", num),
		"%(group_name)s", program,
		"%(numprocs)d", fmt.Sprintf("%d", p.Numprocs),
	).Replace(p.ProcessName)
}

// validateProcessNames rejects configs whose processes would share a name:
// the daemon tracks processes by name, so all but one would never run
// (#166). It runs after includes, on fully expanded program configs.
func validateProcessNames(cfg *Config) []error {
	// Programs and event listener pools share one namespace (ADR-042).
	type entry struct {
		label string // "programs.x" or "eventlisteners.x"
		name  string
		p     ProgramConfig
	}
	var entries []entry
	for name, p := range cfg.Programs {
		entries = append(entries, entry{"programs." + name, name, p})
	}
	for name, l := range cfg.EventListeners {
		entries = append(entries, entry{"eventlisteners." + name, name, l.ProgramConfig})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].label < entries[j].label })

	var errs []error
	owner := make(map[string]string)
	for _, e := range entries {
		p := e.p
		if p.Numprocs > 1 && p.ProcessName != "" && !strings.Contains(p.ProcessName, "%(process_num)d") {
			errs = append(errs, fmt.Errorf("%s: process_name %q must contain %%(process_num)d when numprocs > 1", e.label, p.ProcessName))
			continue
		}
		for _, name := range InstanceNames(e.name, p) {
			if other, ok := owner[name]; ok {
				errs = append(errs, fmt.Errorf("duplicate process name: %s (%s and %s)", name, other, e.label))
				continue
			}
			owner[name] = e.label
		}
	}
	return errs
}
