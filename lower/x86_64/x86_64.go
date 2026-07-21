// Package x86_64 lowers verified vir modules to x86-64 (AMD64) machine code.
//
// vir.Module -> x86_64.Program, and nothing more. This package knows x86-64
// instruction selection, the System V AMD64 ABI (frame layout, call-argument
// layout, struct/array layout), inline-assembly lowering, and per-target-OS
// syscall conventions. The instruction-set facts it builds on — register
// identity, condition-code numbering, REX/ModRM/SIB layout, the
// opcode<->mnemonic tables — live in isa/x86_64 and are used directly at
// the point of use (isax86_64.RAX, isax86_64.CondE, ...), never re-declared
// under a local name. The generic Inst-stream-to-bytes assembler lives in
// isa/x86_64/encoder. This package knows nothing about ELF/Mach-O/PE.
package x86_64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/x86_64/encoder"
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
	Fixups []encoder.Fixup
}

type Global struct {
	Name   string
	Data   []byte // nil for zero (BSS-style) storage
	Size   uint32
	Align  uint32
	Export bool
	TLS    bool
	Fixups []encoder.Fixup
}

// Lower converts a verified module into an x86_64 Program. The module must
// have passed vir.Verify; Lower assumes the §9 obligations.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target != nil && m.Target.Arch != "x86_64" {
		return nil, fmt.Errorf("lower/x86_64: module targets arch %q, not x86_64", m.Target.Arch)
	}
	lw := &lowerer{
		m: m, lay: NewLayout(m),
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

// lowerer holds module-wide bookkeeping shared by global and function
// lowering: the entity-kind table (routes an ident in operand position to
// const/global/fn/extern/struct/fnsig handling) and the layout engine every
// size/offset computation goes through.
type lowerer struct {
	m      *vir.Module
	lay    *Layout
	kinds  map[string]string
	consts map[string]*vir.Constant
}

func (lw *lowerer) lookupCallable(name string) (ret vir.Type, params []vir.Param, variadic, ok bool) {
	for _, g := range lw.m.Externs {
		for _, e := range g.Functions {
			if e.Name == name {
				return e.Ret, e.Params, e.Variadic, true
			}
		}
	}
	for _, f := range lw.m.Functions {
		if f.Name == name {
			return f.Ret, f.Params, false, true // fn-def can't express variadic
		}
	}
	return nil, nil, false, false
}