// Package aarch64 lowers verified vir modules to 64-bit ARM (A64) machine
// code. See README.md for scope, layout, and why this is one package
// rather than five.
package aarch64

import "github.com/vertex-language/vvm/isa/aarch64/encoder"

// OprKind discriminates an Opr's payload: encoder.OprKind's three
// resolved kinds (register, immediate, concrete memory), plus exactly one
// more, OSlot, for a vir value's not-yet-placed stack home.
type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm // immediate; Sym != "" is unused here (see Inst.Sym for movsym)
	OMem // [Base + Disp]
	OSlot
)

// Opr mirrors encoder.Opr's shape but adds OSlot. isel.go is the only
// thing that produces OSlot operands; encode.go's resolveOpr is the only
// thing that consumes them, rewriting every OSlot to a concrete OMem
// before conversion to encoder.Opr.
type Opr struct {
	Kind OprKind
	Reg  encoder.Reg
	Imm  int64
	Sym  string
	Base encoder.Reg
	Disp int32
	Slot string
}

func R(r encoder.Reg) Opr            { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr                { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr            { return Opr{Kind: OImm, Sym: s} }
func Mem(b encoder.Reg, d int32) Opr { return Opr{Kind: OMem, Base: b, Disp: d} }
func Slot(n string) Opr              { return Opr{Kind: OSlot, Slot: n} }

// Inst is this package's pre-encoding vocabulary: the same operand shape
// as encoder.Inst, plus three function-exit markers — epi_ret,
// epi_jmp_sym, epi_jmp_r — that isel.go emits at every return/tailcall
// site instead of a bare ret/b_sym/br_r. encode.go's toEncoderInsts is
// the only thing that expands one of these into the real epilogue
// (mov sp,fp; ldp fp,lr,[sp],#16) followed by the plain exit instruction
// isa/aarch64/encoder actually knows about.
type Inst struct {
	Op         string
	D, S, T, X Opr
	Cc         byte
	Sz         int
	Lbl        string
	Sym        string
	Imm        int64
}