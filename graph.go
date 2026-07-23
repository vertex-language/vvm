// graph.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/importer"
	"github.com/vertex-language/vvm/ir/verify"
	"github.com/vertex-language/vvm/ir/vir"
)

// BuildGraph is BuildModuleGraph's serialized-source counterpart, the
// same relationship Build has to BuildModule: it decodes every src (each
// may independently be .vbyte or .vir) and hands the resulting modules to
// BuildModuleGraph.
func BuildGraph(srcs [][]byte, rootModuleName string, t Target) ([]byte, error) {
	modules := make([]*vir.Module, 0, len(srcs))
	for i, src := range srcs {
		m, err := decodeModule(src)
		if err != nil {
			return nil, fmt.Errorf("vvm: decode module %d: %w", i, err)
		}
		modules = append(modules, m)
	}
	return BuildModuleGraph(modules, rootModuleName, t)
}

// BuildModuleGraph runs the multi-module pipeline for modules that
// reference each other via `import` — importer's own documented Flow,
// run exactly as its README specifies:
//
//	importer.NewSet(modules)   — index by qualified identity
//	set.ResolveImports()       — every `import "X"` -> real module
//	verify.Verify(m) per module — ir/verify, import-agnostic, unchanged
//	set.CheckReferences()      — every qualified ref checked against the
//	                              real target's real declarations
//	set.Rewrite()              — erase cross-module refs: const -> inline
//	                              literal; fn/global -> real mangled
//	                              symbol; struct/fnsig -> unchanged
//	lower/<arch>, unchanged from here on
//
// importer's Flow ends with "unchanged from here on" — no additional
// rewrite pass exists or is needed; Rewrite already leaves every module
// in exactly the shape toObjectBytes knows how to consume, the same as
// the single-module path.
//
// rootModuleName selects which module's `entry`-attributed fn (if any)
// resolveEntryPoint should look at; every module in the set still gets
// lowered and linked in, exactly as BuildModule does for one module.
func BuildModuleGraph(modules []*vir.Module, rootModuleName string, t Target) ([]byte, error) {
	if t.Flat {
		return nil, fmt.Errorf(
			"vvm: %s: flat targets don't support multi-module graphs — flat forbids "+
				"relocations by construction, and cross-module calls are exactly what "+
				"importer.Rewrite turns into relocatable extern-style references", t)
	}

	set, err := importer.NewSet(modules)
	if err != nil {
		return nil, fmt.Errorf("vvm: %w", err)
	}
	if err := set.ResolveImports(); err != nil {
		return nil, fmt.Errorf("vvm: %w", err)
	}
	for _, m := range modules {
		if err := verify.Verify(m); err != nil {
			return nil, fmt.Errorf("vvm: verify %q: %w", m.Name, err)
		}
	}
	if err := set.CheckReferences(); err != nil {
		return nil, fmt.Errorf("vvm: %w", err)
	}
	if err := set.Rewrite(); err != nil {
		return nil, fmt.Errorf("vvm: %w", err)
	}

	root := findModule(modules, rootModuleName)
	if root == nil {
		return nil, fmt.Errorf("vvm: root module %q not found in the given set", rootModuleName)
	}

	entrySym, stub, err := resolveEntryPoint(root, t)
	if err != nil {
		return nil, err
	}

	f, err := t.objFormat()
	if err != nil {
		return nil, err
	}

	l, err := newLinker(modules, t, entrySym)
	if err != nil {
		return nil, err
	}
	for _, m := range modules {
		obj, err := toObjectBytes(m, t, f)
		if err != nil {
			return nil, fmt.Errorf("vvm: module %q: %w", m.Name, err)
		}
		if err := l.AddObject(m.Name+".o", obj); err != nil {
			return nil, fmt.Errorf("vvm: add object %q: %w", m.Name, err)
		}
	}
	if stub != nil {
		if err := l.AddObject("vvm_crt.o", stub.Object); err != nil {
			return nil, fmt.Errorf("vvm: add crt object: %w", err)
		}
	}

	out, err := l.Link()
	if err != nil {
		return nil, fmt.Errorf("vvm: link: %w", err)
	}
	return out, nil
}

func findModule(modules []*vir.Module, name string) *vir.Module {
	for _, m := range modules {
		if m.Name == name {
			return m
		}
	}
	return nil
}