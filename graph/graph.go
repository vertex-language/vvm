// graph.go
package graph

import (
	"fmt"
	"sort"
	"strings"

	vmetabinary "github.com/vertex-language/vvm/format/vmeta/binary"
)

// Package graph computes a dependency-first readiness order over a set of
// modules using only their .vmeta shape data (format/vmeta, ir/vir §7.3
// Stage 1 — "resolveImportGraph"). It never touches .vir/.vbyte source
// and never imports ir/vir's verifier or construction API — only the
// vir.ModuleShape type, which format/vmeta/binary.Result already carries
// as a field, so no additional real dependency is introduced beyond what
// format/vmeta itself already needs.
//
// This is what lets vvm's own multi-module orchestration decide, from
// cheap Stage 0 output alone, which modules are ready for Stage A
// (vir.VerifyWithImports) before any module's expensive full compile has
// run — the core trick the top-level README describes but that no
// package actually implemented until now.

// ResolveImportGraph computes a dependency-first readiness order over
// modules, keyed by each module's own qualified import path — exactly
// the string another module's `import "..."` line would reference it by
// (vir.ModuleShape.QualifiedID(): namespace + "/" + name, or a bare name
// when no namespace is declared).
//
// Every ResolveImportGraph caller is expected to key its map by that same
// convention; ResolveImportGraph cross-checks it rather than trusting it
// silently — a Result whose own Shape.QualifiedID() doesn't match the map
// key it was stored under is rejected, since that mismatch would mean
// some earlier step already confused two modules' identities.
//
// order lists every key of modules exactly once, dependency-first: a
// module's key never appears before any module it directly imports.
// Ties (modules with no dependency relationship at a given step) are
// broken lexically by key, so the result is deterministic across runs —
// this matters for build reproducibility and for tests, not just
// aesthetics.
//
// An import naming a module absent from modules, a self-import, a
// duplicate import within one module's own list, or an import cycle are
// all reported as errors rather than silently worked around — matching
// the "fail loudly, never guess" culture the rest of the pipeline
// documents (vir.Verify, objectwriter's adapters, vvm's own dispatch).
//
// NOTE ON INFERENCE: the underlying spec text (ir.md §7.3) never states
// outright that the import graph must be acyclic, but Stage A's whole
// model — a module becomes ready once its *direct imports'* .vmeta is
// ready — has no coherent meaning if two modules depend on each other,
// so cycle rejection is treated here as a hard requirement rather than
// an optional strictness knob.
func ResolveImportGraph(modules map[string]*vmetabinary.Result) ([]string, error) {
	if len(modules) == 0 {
		return nil, nil
	}

	edges := make(map[string][]string, len(modules))
	for key, res := range modules {
		if res == nil || res.Shape == nil {
			return nil, fmt.Errorf("graph: module %q has no shape data", key)
		}
		if qid := res.Shape.QualifiedID(); qid != key {
			return nil, fmt.Errorf(
				"graph: module keyed %q but its own shape reports qualified identity %q — caller identity mismatch",
				key, qid)
		}

		seen := make(map[string]bool, len(res.Imports))
		for _, imp := range res.Imports {
			if imp == "" {
				return nil, fmt.Errorf("graph: module %q: empty import string in .vmeta IMPD section", key)
			}
			if imp == key {
				return nil, fmt.Errorf("graph: module %q imports itself (§7.3)", key)
			}
			if seen[imp] {
				// vir.Verify already rejects a single module's own
				// duplicate `import` line (verify.go's seenImport map)
				// at Stage A time; this is the same check applied here
				// to .vmeta-derived data, which a stale or hand-edited
				// .vmeta could still violate even when the module's own
				// real source never could.
				return nil, fmt.Errorf("graph: module %q: duplicate import %q", key, imp)
			}
			seen[imp] = true
			if _, ok := modules[imp]; !ok {
				return nil, fmt.Errorf("graph: module %q imports %q, but no .vmeta was supplied for it", key, imp)
			}
			edges[key] = append(edges[key], imp)
		}
	}

	order, ok := kahn(modules, edges)
	if !ok {
		cyc := findCycle(edges)
		return nil, fmt.Errorf(
			"graph: import cycle detected: %s (Stage A assumes an acyclic import graph — no participant in a cycle could ever become ready)",
			strings.Join(cyc, " -> "))
	}
	return order, nil
}

// kahn runs Kahn's algorithm over the reversed edge direction: edges[key]
// lists what key depends on, so each entry in edges[key] must be emitted
// before key itself. ok is false if a cycle prevented every module from
// being emitted.
func kahn(modules map[string]*vmetabinary.Result, edges map[string][]string) (order []string, ok bool) {
	remaining := make(map[string]int, len(modules))
	dependents := make(map[string][]string, len(modules)) // imp -> modules waiting on it
	for key := range modules {
		remaining[key] = len(edges[key])
	}
	for key, imps := range edges {
		for _, imp := range imps {
			dependents[imp] = append(dependents[imp], key)
		}
	}

	var ready []string
	for key, n := range remaining {
		if n == 0 {
			ready = append(ready, key)
		}
	}
	sort.Strings(ready)

	order = make([]string, 0, len(modules))
	for len(ready) > 0 {
		next := ready[0]
		ready = ready[1:]
		order = append(order, next)

		var newlyReady []string
		deps := append([]string(nil), dependents[next]...)
		sort.Strings(deps)
		for _, dep := range deps {
			remaining[dep]--
			if remaining[dep] == 0 {
				newlyReady = append(newlyReady, dep)
			}
		}
		ready = mergeSorted(ready, newlyReady)
	}

	return order, len(order) == len(modules)
}

// mergeSorted merges two already sorted, duplicate-free string slices.
func mergeSorted(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make([]string, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] <= b[j] {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// findCycle locates one concrete cycle in edges for error reporting, via
// DFS with an explicit recursion stack. Only called after kahn has
// already determined a cycle exists, so it always finds one.
func findCycle(edges map[string][]string) []string {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var stack []string

	var visit func(n string) []string
	visit = func(n string) []string {
		state[n] = inStack
		stack = append(stack, n)
		for _, m := range edges[n] {
			switch state[m] {
			case unvisited:
				if cyc := visit(m); cyc != nil {
					return cyc
				}
			case inStack:
				for i, s := range stack {
					if s == m {
						cyc := append([]string{}, stack[i:]...)
						return append(cyc, m)
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return nil
	}

	keys := make([]string, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic starting point across repeated calls

	for _, k := range keys {
		if state[k] == unvisited {
			if cyc := visit(k); cyc != nil {
				return cyc
			}
		}
	}
	return []string{"<cycle detection inconsistency — please report>"}
}