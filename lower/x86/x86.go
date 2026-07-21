// Package x86 lowers verified vir modules to 32-bit x86 (IA-32) machine
// code.
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
		kinds:  map[string]string{},
		consts: map[string]*vir.Constant{},
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
		}
	}
	for _, f := range m.Functions {
		lw.kinds[f.Name] = "fn"
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

type lowerer struct {
	m      *vir.Module
	lay    *Layout
	kinds  map[string]string
	consts map[string]*vir.Constant
}

func (lw *lowerer) lookupCallable(name string) (ret vir.Type, params []vir.Param, ok bool) {
	for _, g := range lw.m.Externs {
		for _, e := range g.Functions {
			if e.Name == name {
				return e.Ret, e.Params, true
			}
		}
	}
	for _, f := range lw.m.Functions {
		if f.Name == name {
			return f.Ret, f.Params, true
		}
	}
	return nil, nil, false
}