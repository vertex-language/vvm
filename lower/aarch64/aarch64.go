// Package aarch64 lowers verified vir modules to 64-bit ARM (A64) machine
// code for arch "aarch64" (little-endian data) or "aarch64_be"
// (big-endian data), selected by the Arch argument to Lower. Big()
// governs only Data serialization — Code is always little-endian (A64
// instruction fetch is architecturally LE in both archs). See README.md
// for scope and layout.
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// Arch selects which A64 architecture Lower serializes for.
type Arch string

const (
	ArchAArch64   Arch = "aarch64"
	ArchAArch64BE Arch = "aarch64_be"
)

func (a Arch) Big() bool   { return a == ArchAArch64BE }
func (a Arch) valid() bool { return a == ArchAArch64 || a == ArchAArch64BE }

// Program is a self-contained description of the lowered module.
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

// Fixup/FixupKind are re-exported from isa/aarch64/encoder, which owns
// the encoding (mirrors lower/x86_64's re-export convention).
type Fixup = encoder.Fixup
type FixupKind = encoder.FixupKind

const (
	FixupCall26 = encoder.FixupCall26
	FixupJump26 = encoder.FixupJump26
	FixupMovzG3 = encoder.FixupMovzG3
	FixupMovkG2 = encoder.FixupMovkG2
	FixupMovkG1 = encoder.FixupMovkG1
	FixupMovkG0 = encoder.FixupMovkG0
	FixupAbs64  = encoder.FixupAbs64
)

// Lower converts a verified module into an aarch64 Program for the given
// arch ("aarch64" or "aarch64_be"). The module must have passed
// vir.Verify.
func Lower(m *vir.Module, arch Arch) (*Program, error) {
	if !arch.valid() {
		return nil, fmt.Errorf("lower/aarch64: unknown arch %q (want %q or %q)", arch, ArchAArch64, ArchAArch64BE)
	}
	if m.Target != nil && string(arch) != m.Target.Arch {
		return nil, fmt.Errorf("lower/aarch64: module targets arch %q, build requested %q (§10.6: the two must agree)", m.Target.Arch, arch)
	}
	lw := &lowerer{
		m: m, lay: NewLayout(m), arch: arch,
		kinds:  map[string]string{},
		consts: map[string]*vir.Constant{},
		tls:    map[string]bool{},
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
		if g.TLS {
			lw.tls[g.Name] = true
		}
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
	tls    map[string]bool
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

// ---------------------------------------------------------------------------
// Function lowering
// ---------------------------------------------------------------------------

func (lw *lowerer) lowerFunc(f *vir.Function) (Func, error) {
	fl := &fnLower{lowerer: lw, f: f}
	var err error
	if fl.types, err = fl.typeFunc(); err != nil {
		return Func{}, err
	}
	for _, p := range f.Params {
		if p.ByVal != "" || p.SRet != "" {
			return Func{}, fmt.Errorf("byval/sret not yet lowered on aarch64 (AAPCS64 aggregate passing + x8 TODO)")
		}
	}
	for i, p := range f.Params {
		if i < 8 {
			fl.normReg(encoder.Reg(i), p.Type)
			fl.st(p.Name, encoder.Reg(i))
		} else {
			fl.emit(Inst{Op: "ldr", D: R(encoder.X0), S: Mem(encoder.FP, int32(16+8*(i-8))), Sz: 8})
			fl.normReg(encoder.X0, p.Type)
			fl.st(p.Name, encoder.X0)
		}
	}
	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(Inst{Op: "label", Lbl: b.Label})
		}
		for i := range b.Lines {
			line := &b.Lines[i]
			switch {
			case line.Instruction != nil:
				if err := fl.selInst(line.Instruction); err != nil {
					return Func{}, fmt.Errorf("block %s: %s: %w", labelName(b), line.Instruction.Op, err)
				}
			case line.Asm != nil:
				if err := fl.selAsm(line.Asm); err != nil {
					return Func{}, fmt.Errorf("block %s: asm: %w", labelName(b), err)
				}
			}
		}
		if err := fl.selTerm(b.Term); err != nil {
			return Func{}, fmt.Errorf("block %s: terminator: %w", labelName(b), err)
		}
	}
	fr := BuildFrame(f, fl.b)
	code, fixups, err := assemble(fl.b, fr)
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

type fnLower struct {
	*lowerer
	f     *vir.Function
	types map[string]vir.Type
	b     []Inst
	nlbl  int
}

func (fl *fnLower) emit(i Inst)                            { fl.b = append(fl.b, i) }
func (fl *fnLower) alu(op string, sz int, d, s Opr)         { fl.emit(Inst{Op: op, Sz: sz, D: d, S: s}) }
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}

// selAsm lowers one BodyLine.Asm block via LowerBlock (asm.go), appending
// its Inst stream in place — asm.go and isel.go share the same
// slot/scratch conventions, so no bridging is required.
func (fl *fnLower) selAsm(block *vir.AsmBlock) error {
	if fl.m.AsmDialect == nil {
		return fmt.Errorf("asm block present but module declares no AsmDialect (verifier should have caught this)")
	}
	regTable := vir.RegisterTableForArchitecture(string(fl.arch))
	if regTable == nil {
		return fmt.Errorf("no register table wired for arch %q", fl.arch)
	}
	insts, err := LowerBlock(*fl.m.AsmDialect, block, regTable)
	if err != nil {
		return err
	}
	fl.b = append(fl.b, insts...)
	return nil
}