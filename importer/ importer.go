// importer.go
// Package importer implements cross-module resolution for a set of
// decoded *vir.Module values (see importer.md). It is the only package
// that knows about `import "X"`, qualified-ident operands, and
// StructType.Import — ir/vir and ir/verify are both entirely
// import-agnostic (verify.md, README "Why this package doesn't check
// anything").
//
// Pipeline position (importer.md "Flow"):
//
//	format/vbyte/{binary,text}.Decode  -> unverified *vir.Module, one per file
//	importer.NewSet(modules)          -> index by qualified identity
//	set.ResolveImports()              -> every `import "X"` -> real module
//	verify.Verify(m) per module       -> ir/verify, unchanged, import-agnostic
//	set.CheckReferences()             -> every qualified ref checked against
//	                                      the real target's real declarations
//	set.Rewrite()                     -> erase cross-module refs
//	lower/<arch>                      -> unchanged from here on
//
// verify.Verify must succeed on a module before CheckReferences looks at
// it — checking a cross-module reference is only meaningful once the
// referencing module is known to be internally well-formed. That
// ordering is the caller's responsibility (it's a pipeline step, not
// something CheckReferences enforces by calling verify.Verify itself —
// this package doesn't import ir/verify at all). ResolveImports has no
// such dependency: it's a pure name lookup and may run before or after
// Verify.
package importer

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Set is every decoded module a program needs, all present in memory at
// once, indexed by qualified identity so import paths resolve directly.
type Set struct {
	modules []*vir.Module
	byID    map[string]*vir.Module

	// resolved[m][path] is populated by ResolveImports; nil until then.
	resolved map[*vir.Module]map[string]*vir.Module
}

// identity returns m's lookup key exactly as an import path must spell
// it: "namespace/name" if m declared a namespace, bare "name" otherwise
// (importer.md "Bare-name vs. namespaced identity").
func identity(m *vir.Module) string {
	if m.Namespace != "" {
		return m.Namespace + "/" + m.Name
	}
	return m.Name
}

// NewSet indexes modules by qualified identity. Two modules can only
// collide on a bare name if both are unnamespaced and share a module
// name — that's the only case rejected here (a namespaced module is only
// ever reachable via its full namespace/name string, so it can't collide
// with anything bare).
func NewSet(modules []*vir.Module) (*Set, error) {
	byID := make(map[string]*vir.Module, len(modules))
	for _, m := range modules {
		if m.Name == "" {
			return nil, fmt.Errorf("module has no name")
		}
		id := identity(m)
		if prev, dup := byID[id]; dup {
			return nil, fmt.Errorf("duplicate module identity %q (modules %q and %q)", id, prev.Name, m.Name)
		}
		byID[id] = m
	}
	return &Set{
		modules:  modules,
		byID:     byID,
		resolved: make(map[*vir.Module]map[string]*vir.Module, len(modules)),
	}, nil
}