// Package securitymodel contains offline, bounded models only. No node imports it.
package securitymodel

import (
	"slices"
	"testing"
)

type edge[S comparable] struct {
	next   S
	action string
}
type searchResult struct {
	states, transitions int
	complete            bool
	violation           string
	trace               []string
}

// search performs breadth-first exploration; limits must never masquerade as success.
func search[S comparable](initial S, next func(S) []edge[S], check func(S) string, limit int) searchResult {
	type node struct {
		state  S
		parent int
		action string
	}
	nodes := []node{{state: initial, parent: -1}}
	seen := map[S]int{initial: 0}
	r := searchResult{states: 1}
	witness := func(i int, reason string) searchResult {
		r.violation = reason
		for nodes[i].parent >= 0 {
			r.trace = append(r.trace, nodes[i].action)
			i = nodes[i].parent
		}
		slices.Reverse(r.trace)
		return r
	}
	if reason := check(initial); reason != "" {
		return witness(0, reason)
	}
	for i := 0; i < len(nodes); i++ {
		for _, e := range next(nodes[i].state) {
			r.transitions++
			if _, ok := seen[e.next]; ok {
				continue
			}
			if len(nodes) >= limit {
				return r
			}
			j := len(nodes)
			nodes = append(nodes, node{e.next, i, e.action})
			seen[e.next] = j
			r.states++
			if reason := check(e.next); reason != "" {
				return witness(j, reason)
			}
		}
	}
	r.complete = true
	return r
}

func requireExhausted(t *testing.T, r searchResult) {
	t.Helper()
	t.Logf("states=%d transitions=%d complete=%v", r.states, r.transitions, r.complete)
	if r.violation != "" || !r.complete {
		t.Fatalf("incomplete or unsafe: %+v", r)
	}
}

func requireWitness(t *testing.T, r searchResult, want string) {
	t.Helper()
	if r.violation != want || len(r.trace) == 0 {
		t.Fatalf("want %q witness: %+v", want, r)
	}
	t.Logf("mutant detected: %s; shortest trace: %v", r.violation, r.trace)
}

func TestSearchFindsShortestCounterexample(t *testing.T) {
	next := func(n int) []edge[int] {
		if n == 0 {
			return []edge[int]{{1, "long"}, {2, "short"}}
		}
		if n == 1 {
			return []edge[int]{{3, "detour"}}
		}
		if n == 2 || n == 3 {
			return []edge[int]{{4, "bad"}}
		}
		return nil
	}
	r := search(0, next, func(n int) string {
		if n == 4 {
			return "four"
		}
		return ""
	}, 20)
	if r.violation != "four" || !slices.Equal(r.trace, []string{"short", "bad"}) {
		t.Fatalf("missing shortest witness: %+v", r)
	}
}

func TestSearchExhaustionAndLimit(t *testing.T) {
	next := func(n int) []edge[int] {
		if n < 3 {
			return []edge[int]{{n, "duplicate"}, {n + 1, "increment"}}
		}
		return nil
	}
	check := func(int) string { return "" }
	r := search(0, next, check, 4)
	if !r.complete || r.states != 4 || r.transitions != 6 {
		t.Fatalf("exhaustion: %+v", r)
	}
	r = search(0, next, check, 2)
	if r.complete || r.violation != "" || r.states != 2 {
		t.Fatalf("limit: %+v", r)
	}
}
