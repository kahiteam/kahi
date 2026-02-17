package process

import (
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

func TestBuildHomogeneousGroupsSingle(t *testing.T) {
	cfg := defaultProgramConfig()
	p := NewProcess("web", "web", cfg, &MockSpawner{}, testBus(), testLogger())

	groups := BuildHomogeneousGroups(map[string][]*Process{
		"web": {p},
	})

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups["web"]
	if g == nil {
		t.Fatal("missing group 'web'")
	}
	if len(g.Processes) != 1 || g.Processes[0] != "web" {
		t.Fatalf("processes = %v, want [web]", g.Processes)
	}
	if g.Priority != 999 {
		t.Fatalf("priority = %d, want 999", g.Priority)
	}
}

func TestBuildHomogeneousGroupsMultipleInstances(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Priority = 100
	p0 := NewProcess("worker_0", "worker", cfg, &MockSpawner{}, testBus(), testLogger())
	p1 := NewProcess("worker_1", "worker", cfg, &MockSpawner{}, testBus(), testLogger())
	p2 := NewProcess("worker_2", "worker", cfg, &MockSpawner{}, testBus(), testLogger())

	groups := BuildHomogeneousGroups(map[string][]*Process{
		"worker": {p0, p1, p2},
	})

	g := groups["worker"]
	if g == nil {
		t.Fatal("missing group 'worker'")
	}
	if len(g.Processes) != 3 {
		t.Fatalf("expected 3 processes, got %d", len(g.Processes))
	}
	if g.Priority != 100 {
		t.Fatalf("priority = %d, want 100", g.Priority)
	}
	// Verify sorted.
	for i := 1; i < len(g.Processes); i++ {
		if g.Processes[i] < g.Processes[i-1] {
			t.Fatal("processes not sorted")
		}
	}
}

func TestBuildHomogeneousGroupsEmpty(t *testing.T) {
	groups := BuildHomogeneousGroups(map[string][]*Process{})
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups, got %d", len(groups))
	}
}

func TestValidateGroupNameCollisionsNone(t *testing.T) {
	groups := map[string]*Group{
		"web":    {Name: "web"},
		"worker": {Name: "worker"},
	}
	if err := ValidateGroupNameCollisions(groups); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGroupNameCollisionsEmpty(t *testing.T) {
	if err := ValidateGroupNameCollisions(map[string]*Group{}); err != nil {
		t.Fatal(err)
	}
}

func TestMergeHeterogeneousGroupsOnlyImplicit(t *testing.T) {
	implicit := map[string]*Group{
		"web":    {Name: "web", Processes: []string{"web"}},
		"worker": {Name: "worker", Processes: []string{"worker"}},
	}
	result := MergeHeterogeneousGroups(implicit, map[string]*Group{})

	if len(result) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result))
	}
}

func TestMergeHeterogeneousGroupsOnlyExplicit(t *testing.T) {
	explicit := map[string]*Group{
		"all": {Name: "all", Processes: []string{"web", "worker"}},
	}
	result := MergeHeterogeneousGroups(map[string]*Group{}, explicit)

	if len(result) != 1 {
		t.Fatalf("expected 1 group, got %d", len(result))
	}
	if result["all"] == nil {
		t.Fatal("missing group 'all'")
	}
}

func TestMergeHeterogeneousGroupsExplicitSuppresses(t *testing.T) {
	implicit := map[string]*Group{
		"web":    {Name: "web", Processes: []string{"web"}},
		"worker": {Name: "worker", Processes: []string{"worker"}},
	}
	explicit := map[string]*Group{
		"web": {Name: "web", Processes: []string{"web", "worker"}},
	}
	result := MergeHeterogeneousGroups(implicit, explicit)

	if len(result) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result))
	}
	// Explicit "web" should replace implicit "web".
	if len(result["web"].Processes) != 2 {
		t.Fatalf("expected 2 processes in 'web', got %d", len(result["web"].Processes))
	}
}

func TestExpandNumprocsSingle(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Numprocs = 1
	instances := ExpandNumprocs("web", cfg)

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if instances[0].Name != "web" {
		t.Fatalf("name = %q, want web", instances[0].Name)
	}
	if instances[0].Group != "web" {
		t.Fatalf("group = %q, want web", instances[0].Group)
	}
}

func TestExpandNumprocsMultiple(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Numprocs = 3
	cfg.ProcessName = "worker-%(process_num)d"
	instances := ExpandNumprocs("worker", cfg)

	if len(instances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(instances))
	}
	if instances[0].Name != "worker-0" {
		t.Fatalf("instances[0].Name = %q, want worker-0", instances[0].Name)
	}
	if instances[2].Name != "worker-2" {
		t.Fatalf("instances[2].Name = %q, want worker-2", instances[2].Name)
	}
	for _, inst := range instances {
		if inst.Group != "worker" {
			t.Fatalf("group = %q, want worker", inst.Group)
		}
	}
}

func TestExpandNumprocsNoTemplate(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Numprocs = 2
	cfg.ProcessName = ""
	instances := ExpandNumprocs("worker", cfg)

	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	if instances[0].Name != "worker_0" {
		t.Fatalf("instances[0].Name = %q, want worker_0", instances[0].Name)
	}
}

func TestExpandNumprocsWithStart(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Numprocs = 2
	cfg.NumprocsStart = 5
	cfg.ProcessName = "job-%(process_num)d"
	instances := ExpandNumprocs("job", cfg)

	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	if instances[0].Name != "job-5" {
		t.Fatalf("instances[0].Name = %q, want job-5", instances[0].Name)
	}
	if instances[1].Name != "job-6" {
		t.Fatalf("instances[1].Name = %q, want job-6", instances[1].Name)
	}
}

func TestConfigDiffAdded(t *testing.T) {
	old := &config.Config{Programs: map[string]config.ProgramConfig{}}
	new := &config.Config{Programs: map[string]config.ProgramConfig{
		"web": defaultProgramConfig(),
		"api": defaultProgramConfig(),
	}}
	added, changed, removed := ConfigDiff(old, new)

	if len(added) != 2 {
		t.Fatalf("added = %v, want 2", added)
	}
	if len(changed) != 0 {
		t.Fatalf("changed = %v, want 0", changed)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want 0", removed)
	}
}

func TestConfigDiffRemoved(t *testing.T) {
	old := &config.Config{Programs: map[string]config.ProgramConfig{
		"web": defaultProgramConfig(),
	}}
	new := &config.Config{Programs: map[string]config.ProgramConfig{}}
	added, _, removed := ConfigDiff(old, new)

	if len(removed) != 1 || removed[0] != "web" {
		t.Fatalf("removed = %v, want [web]", removed)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want 0", added)
	}
}

func TestConfigDiffChanged(t *testing.T) {
	cfgOld := defaultProgramConfig()
	cfgNew := defaultProgramConfig()
	cfgNew.Command = "/bin/newcmd"

	old := &config.Config{Programs: map[string]config.ProgramConfig{"web": cfgOld}}
	new := &config.Config{Programs: map[string]config.ProgramConfig{"web": cfgNew}}
	added, changed, removed := ConfigDiff(old, new)

	if len(changed) != 1 || changed[0] != "web" {
		t.Fatalf("changed = %v, want [web]", changed)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("unexpected added=%v removed=%v", added, removed)
	}
}

func TestConfigDiffNoChange(t *testing.T) {
	cfg := defaultProgramConfig()
	old := &config.Config{Programs: map[string]config.ProgramConfig{"web": cfg}}
	new := &config.Config{Programs: map[string]config.ProgramConfig{"web": cfg}}
	added, chg, removed := ConfigDiff(old, new)

	if len(added)+len(chg)+len(removed) != 0 {
		t.Fatalf("expected no diff, got added=%v changed=%v removed=%v", added, chg, removed)
	}
}

func TestConfigDiffSorted(t *testing.T) {
	old := &config.Config{Programs: map[string]config.ProgramConfig{}}
	new := &config.Config{Programs: map[string]config.ProgramConfig{
		"zebra":  defaultProgramConfig(),
		"alpha":  defaultProgramConfig(),
		"middle": defaultProgramConfig(),
	}}
	added, _, _ := ConfigDiff(old, new)

	if len(added) != 3 {
		t.Fatalf("expected 3 added, got %d", len(added))
	}
	if added[0] != "alpha" || added[1] != "middle" || added[2] != "zebra" {
		t.Fatalf("added not sorted: %v", added)
	}
}
