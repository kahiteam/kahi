package main

import (
	"strings"
	"testing"
)

func TestCheckGraph(t *testing.T) {
	ok := []feature{{ID: "a"}, {ID: "b", Deps: []string{"a"}}, {ID: "c", Deps: []string{"a", "b"}}}
	if err := checkGraph(ok); err != nil {
		t.Fatalf("valid graph: %v", err)
	}
	cases := map[string]struct {
		features []feature
		want     string
	}{
		"duplicate": {[]feature{{ID: "a"}, {ID: "a"}}, "duplicate id a"},
		"unknown":   {[]feature{{ID: "a", Deps: []string{"gone"}}}, "a depends on unknown id gone"},
		"self":      {[]feature{{ID: "a", Deps: []string{"a"}}}, "dependency cycle a -> a"},
		"cycle": {[]feature{
			{ID: "x"},
			{ID: "a", Deps: []string{"x", "b"}},
			{ID: "b", Deps: []string{"c"}},
			{ID: "c", Deps: []string{"a"}},
		}, "dependency cycle a -> b -> c -> a"},
	}
	for name, tc := range cases {
		err := checkGraph(tc.features)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}
