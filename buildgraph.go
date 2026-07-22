// buildgraph.go
package vvm

import (
	"fmt"

	vmetabinary "github.com/vertex-language/vvm/format/vmeta/binary"
	"github.com/vertex-language/vvm/graph"
	vir "github.com/vertex-language/vvm/ir/vir"
)

// BuildGraph is Build for a multi-module program: src is keyed by each
// module's own qualified import path (vir.ModuleShape.QualifiedID() —
// namespace+"/"+name, or a bare name with no namespace) — exactly the
// string every *other* module's `import "..."` line uses to reference
// it. root names which of those modules supplies the process entry
// point (or, for a shared-library Target, is simply the module being
// built — see entrythunk.go's OutputSharedLibrary gate).
//
// This is Stage 0 through Stage 7 for a real import graph — the flow
// the top-level README describes, which until graph/buildgraph.go only
// existed as documentation: BuildModule's existing single-module path
// never touched imports at all (bare vir.Verify, never
// VerifyWithImports).
func BuildGraph(src map[string][]byte, root string, t Target) ([]byte, error) {
	modules := make(map[string]*vir.Module, len(src))
	for key, b := range src {
		m, err := decodeModule(b)
		if err != nil {
			return nil, fmt.Errorf("vvm: decode %q: %w", key, err)
		}
		modules[key] = m
	}
	return BuildModuleGraph(modules, root, t)
}

// BuildModuleGraph is BuildGraph for a caller already holding decoded
// *vir.Module values.
func BuildModuleGraph(modules map[string]*vir.Module, root string, t Target) ([]byte, error) {
	if _, ok := modules[root]; !ok {
		return nil, fmt.Errorf("vvm: root module %q not present in the supplied module set", root)
	}

	// --- Stage 0 (Extraction): each module's shape only needs its own
	// declaration section, per ir.md §7.3 — not a full Verify. The
	// vmetabinary.Result is built in memory (never actually serialized)
	// so this reuses graph's real input shape without inventing a
	// parallel one just for the in-process case.
	results := make(map[string]*vmetabinary.Result, len(modules))
	for key, m := range modules {
		shape := vir.ExtractShape(m)
		if qid := shape.QualifiedID(); qid != key {
			return nil, fmt.Errorf(
				"vvm: module supplied under key %q declares qualified identity %q (module %q, namespace %q) — the map key must match the module's own namespace+name",
				key, qid, m.Name, m.Namespace)
		}
		imports := make([]string, len(m.Imports))
		for i, imp := range m.Imports {
			imports[i] = imp.Path
		}
		results[key] = &vmetabinary.Result{Shape: shape, Target: m.Target, Imports: imports}
	}

	// --- Stage 1: readiness order + cross-module import validation +
	// cycle detection, from Stage 0 output only (graph package).
	order, err := graph.ResolveImportGraph(results)
	if err != nil {
		return nil, fmt.Errorf("vvm: %w", err)
	}

	// --- Stage A (VerifyWithImports) + Stage B (rewrite), per module,
	// in dependency-first order — so a module's own Stage A never runs
	// before the .vmeta shape it needs is itself trustworthy.
	for _, key := range order {
		m := modules[key]
		shapes, err := graph.ShapesForImports(results, key)
		if err != nil {
			return nil, fmt.Errorf("vvm: %w", err)
		}
		if err := vir.VerifyWithImports(m, shapes); err != nil {
			return nil, fmt.Errorf("vvm: verify %q: %w", key, err)
		}
		if err := rewriteImports(m, shapes); err != nil {
			return nil, fmt.Errorf("vvm: %q: %w", key, err)
		}
		// Re-verify post-rewrite: rewriteImports only ever produces
		// ordinary extern/link constructs vir.Verify already knows how
		// to check unconditionally (no shapes needed — no qualified
		// references remain by construction). Same mutate-then-reverify
		// discipline entrythunk.go already uses.
		if err := vir.Verify(m); err != nil {
			return nil, fmt.Errorf("vvm: verify %q (post import rewrite): %w", key, err)
		}
	}

	// Entry-point resolution only ever applies to root, and only after
	// root's own Stage A/B — resolveEntryPoint's §9.4a assumptions
	// (m.EntryFunction()) need m already verified, the same ordering
	// build.go's single-module path already follows.
	entryPoint, err := resolveEntryPoint(modules[root], t)
	if err != nil {
		return nil, err
	}
	if err := vir.Verify(modules[root]); err != nil {
		return nil, fmt.Errorf("vvm: verify %q (post entry-thunk synthesis): %w", root, err)
	}

	f, err := t.objFormat()
	if err != nil {
		return nil, err
	}

	// --- Stages 2-6, per module, independently.
	objs := make(map[string][]byte, len(order))
	for _, key := range order {
		obj, err := toObjectBytes(modules[key], t, f)
		if err != nil {
			return nil, fmt.Errorf("vvm: %q: %w", key, err)
		}
		objs[key] = obj
	}

	// --- Stage 7: link every module's object together. newLinker now
	// takes every module in the graph (not just root) so §7.4
	// link-dependency resolution (m.Links) runs for each one.
	allModules := make([]*vir.Module, 0, len(order))
	for _, key := range order {
		allModules = append(allModules, modules[key])
	}
	l, err := newLinker(allModules, t, entryPoint)
	if err != nil {
		return nil, err
	}
	for _, key := range order {
		if err := l.AddObject(key+".o", objs[key]); err != nil {
			return nil, fmt.Errorf("vvm: add object %q: %w", key, err)
		}
	}

	out, err := l.Link()
	if err != nil {
		return nil, fmt.Errorf("vvm: link: %w", err)
	}
	return out, nil
}