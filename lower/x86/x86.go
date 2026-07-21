// Package x86 lowers verified vir modules to 32-bit x86 (IA-32) machine
// code.
//
// vir.Module -> x86.Program, and nothing more. This is one flat package:
// instruction selection, ABI/frame layout, slot resolution, syscall
// conventions, and inline-assembly lowering are all facets of a single
// lowering pipeline that runs in a fixed order and shares one Opr/Inst
// vocabulary — splitting them into mcode/abi/regalloc/syscallabi/inlineasm
// subpackages bought no real independence, and the only visible effect was
// several copies of the same isa/x86 register constants re-exported under
// new names. Register identity, condition codes, and ModRM/SIB facts are
// used directly from isa/x86 (isax86.REAX, isax86.CondE, ...) — nothing
// here re-declares them. (isa/x86's own README explains the split that
// *is* load-bearing: ISA fact vs. lowering decision. That one stays.)
//
// This package knows x86 instruction encoding and the i386 System V cdecl
// ABI; it knows nothing about ELF/COFF/Mach-O.
//
// ABI: cdecl. Arguments on the stack in 4-byte slots, first argument at the
// lowest address, caller cleans up, result in EAX. EAX/ECX/EDX
// caller-saved; EBX/ESI/EDI/EBP callee-saved; EBP is the frame pointer.
//
// Code model: non-PIC. Globals/function addresses are 32-bit absolute
// (FixupAbs32); calls and cross-function jumps are 32-bit PC-relative
// (FixupPCRel32).
//
// Coverage: the integer/pointer subset of Vertex IR, inline assembly
// (Intel and AT&T dialects, curated mnemonic set), and syscalls
// (per-target-OS trap convention). Floats, vectors, i64/i128 named values,
// saturating arithmetic, and bitrev are still rejected with explicit
// errors (TODO, tier work).
package x86

import (
	"fmt"

	"github.com/vertex-language/vvm/isa/x86/encoder"
	"github.com/vertex-language/vvm/ir/vir"
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

// Fixup/FixupKind are isa/x86/encoder's relocation vocabulary. This one
// alias is deliberate and single-hop — downstream object writers get to
// import only package x86 — unlike the register-constant copies this
// redo removes, it doesn't duplicate a fact, it just re-exports one type.
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