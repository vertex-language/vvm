// x86.go
// Package x86 lowers verified vir modules to 32-bit x86 (IA-32) machine
// code. It expects the module has already passed vir.Verify and, if it
// came from multiple source files, importer.Rewrite — cross-module
// references should already be erased into plain calls/symbols/inline
// literals by the time Lower sees it. Inline assembly is not part of this
// package: it was removed from ir/vir's data model (see ir/vir's asm.md)
// and has no representation here to lower.
package x86

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/x86/encoder"
)

// Program is a self-contained description of the lowered module:
// instruction bytes, symbols, and unresolved fixups — nothing else.
type Program struct {
	Funcs   []Func
	Globals []Global
}

type Func struct {
	Name   string
	Code   []byte
	Align  uint32
	Export bool
	Fixups []Fixup
}

type Global struct {
	Name   string
	Data   []byte // nil for zero (BSS-style) storage
	Size   uint32 // may exceed len(Data) for zero fill
	Align  uint32
	Export bool
	TLS    bool
	Fixups []Fixup
}

type Fixup = encoder.Fixup
type FixupKind = encoder.FixupKind

const (
	FixupPCRel32 = encoder.FixupPCRel32
	FixupAbs32   = encoder.FixupAbs32
)

// Lower converts a verified module into an x86 Program. The module must
// have passed vir.Verify; Lower assumes the §9 obligations already hold.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target != nil && m.Target.Arch != "x86" {
		return nil, fmt.Errorf("lower/x86: module targets arch %q, not x86", m.Target.Arch)
	}
	lw := &lowerer{
		m: m, lay: newLayout(m),
		kinds:     map[string]string{},
		consts:    map[string]*vir.Constant{},
		callables: map[string]callable{},
	}
	for _, s := range m.Structs {
		lw.kinds[s.Name] = "struct"
	}
	for _, s := range m.FunctionSignatures {
		lw.kinds[s.Name] = "fnsig"
	}
	for _, c := range m.Constants {
		lw.kinds[c.Name] = "const"
		lw.consts[c.Name] = c
	}
	for _, g := range m.Globals {
		lw.kinds[g.Name] = "global"
	}
	for _, g := range m.Externs {
		for _, f := range g.Functions {
			lw.kinds[f.Name] = "extern"
			lw.callables[f.Name] = callable{Ret: f.Ret, Params: f.Params, Variadic: f.Variadic}
		}
	}
	// Module-local definitions are indexed after externs so a locally
	// defined function wins over an extern declaration of the same name,
	// preserving the old lookup order (which scanned externs first but
	// returned the first match in each list).
	for _, f := range m.Functions {
		lw.kinds[f.Name] = "fn"
		lw.callables[f.Name] = callable{Ret: f.Ret, Params: f.Params, Variadic: f.Variadic}
	}

	p := &Program{}
	for _, g := range m.Globals {
		pg, err := lw.lowerGlobal(g)
		if err != nil {
			return nil, fmt.Errorf("global %s: %w", g.Name, err)
		}
		p.Globals = append(p.Globals, pg)
	}
	for _, f := range m.Functions {
		pf, err := lw.lowerFunc(f)
		if err != nil {
			return nil, fmt.Errorf("fn %s: %w", f.Name, err)
		}
		p.Funcs = append(p.Funcs, pf)
	}
	return p, nil
}

// callable is everything instruction selection needs to know about a call
// target, whether it's defined here or imported.
//
// Variadic is part of it because a call site cannot otherwise tell an
// argument that ran off the end of a fixed parameter list — a verifier
// error — from one that legitimately landed in a variadic tail.
type callable struct {
	Ret      vir.Type
	Params   []vir.Param
	Variadic bool
}

type lowerer struct {
	m         *vir.Module
	lay       *Layout
	kinds     map[string]string
	consts    map[string]*vir.Constant
	callables map[string]callable
}

// lookupCallable resolves a call target. Previously this walked every
// extern group and every function on each call; it's a map lookup now,
// built once in Lower.
func (lw *lowerer) lookupCallable(name string) (ret vir.Type, params []vir.Param, ok bool) {
	c, ok := lw.callables[name]
	if !ok {
		return nil, nil, false
	}
	return c.Ret, c.Params, true
}

// lookupCallee is lookupCallable plus the variadic flag.
func (lw *lowerer) lookupCallee(name string) (callable, bool) {
	c, ok := lw.callables[name]
	return c, ok
}