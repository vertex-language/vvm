// Package arm lowers verified vir modules to 32-bit ARM (A32) machine code,
// in either byte order: arch "arm" or "armeb" (arch.go). Instruction-set
// facts live in isa/arm; the generic assembler lives in isa/arm/encoder.
// This package owns only vir.Module -> arm.Program: instruction selection,
// the AAPCS frame/call convention, and slot resolution (encode.go).
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/arm/encoder"
)

type Program struct {
	Arch    Arch
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
	Data   []byte
	Size   uint32
	Align  uint32
	Export bool
	TLS    bool
	Fixups []Fixup
}

// Fixup/FixupKind are encoder's — encoder.Encode is the only thing that
// produces them, so this is a type alias to the single source, not a
// hand-copied second table.
type Fixup = encoder.Fixup
type FixupKind = encoder.FixupKind

const (
	FixupCall24  = encoder.FixupCall24
	FixupJump24  = encoder.FixupJump24
	FixupMovwAbs = encoder.FixupMovwAbs
	FixupMovtAbs = encoder.FixupMovtAbs
	FixupAbs32   = encoder.FixupAbs32
)

func Lower(m *vir.Module, arch Arch) (*Program, error) {
	if !arch.valid() {
		return nil, fmt.Errorf("lower/arm: unknown arch %q (want %q or %q)", arch, ArchARM, ArchARMEB)
	}
	if m.Target != nil && string(arch) != m.Target.Arch {
		return nil, fmt.Errorf("lower/arm: module targets arch %q, build requested %q", m.Target.Arch, arch)
	}
	lw := &lowerer{
		m: m, lay: NewLayout(m), arch: arch,
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

	p := &Program{Arch: arch}
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
	arch   Arch
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

func (lw *lowerer) lowerFunc(f *vir.Function) (Func, error) {
	fl := &fnLower{lowerer: lw, f: f}
	var err error
	if fl.types, err = fl.typeFunc(); err != nil {
		return Func{}, err
	}
	for _, p := range f.Params {
		if p.ByVal != "" || p.SRet != "" {
			return Func{}, fmt.Errorf("byval/sret not yet lowered on arm (AAPCS aggregate passing TODO)")
		}
	}
	for i, p := range f.Params {
		if i < 4 {
			fl.emit(Inst{Op: "str", D: Slot(p.Name), S: R(encoder.Reg(i))})
		}
	}
	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(Inst{Op: "label", Lbl: b.Label})
		}
		for _, line := range b.Lines {
			if line.Asm != nil {
				return Func{}, fmt.Errorf("block %s: inline asm not lowered on arm", labelName(b))
			}
			in := line.Instruction
			if err := fl.selInst(in); err != nil {
				return Func{}, fmt.Errorf("block %s: %s: %w", labelName(b), in.Op, err)
			}
		}
		if err := fl.selTerm(b.Term); err != nil {
			return Func{}, fmt.Errorf("block %s: terminator: %w", labelName(b), err)
		}
	}
	fr := BuildFrame(f, fl.b)
	code, fixups, err := assemble(fl.b, fr, fl.arch.Big())
	if err != nil {
		return Func{}, err
	}
	return Func{Name: f.Name, Code: code, Align: 4, Export: f.Export, Fixups: fixups}, nil
}

func labelName(b *vir.Block) string {
	if b.Label == "" {
		return "<entry>"
	}
	return b.Label
}