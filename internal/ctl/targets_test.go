package ctl

import (
	"slices"
	"testing"
)

func TestMatchTargets(t *testing.T) {
	procs := []ProcessInfo{
		{Name: "api", Group: "svc", Program: "api"},
		{Name: "web_0", Group: "web", Program: "web"},
		{Name: "web_1", Group: "web", Program: "web"},
	}
	names := func(ps []ProcessInfo) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}
	for _, tc := range []struct {
		targets []string
		want    []string
	}{
		{[]string{"all"}, []string{"api", "web_0", "web_1"}},
		{[]string{"web:*"}, []string{"web_0", "web_1"}},
		{[]string{"svc:*"}, []string{"api"}},
		{[]string{"api:*"}, []string{"api"}}, // a table member resolves by program
		{[]string{"web_1", "api"}, []string{"api", "web_1"}},
		{[]string{"nosuch"}, nil},
	} {
		got := MatchTargets(procs, tc.targets)
		if got == nil || !slices.Equal(names(got), tc.want) {
			t.Errorf("%v: %v, want %v", tc.targets, names(got), tc.want)
		}
	}
}
