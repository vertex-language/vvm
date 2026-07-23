// opr.go
package aarch64

import (
	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// This package's pre-encoding operand and instruction representation. It is
// isa/aarch64/encoder's shape plus one variant: OSlot, a named value's frame
// slot, which only becomes an [x29, #off] memory operand once BuildFrame has
// run. encode.go resolves it and converts the rest explicitly.
type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OExt
	OImm
	OMem
	OSlot // a named value's frame slot — no encoder equivalent
)

type Opr struct {
	Kind OprKind

	Reg encoder.Reg
	SP  bool
	Imm int64
	Sym string

	Shift    byte
	ShiftAmt byte

	Ext    byte
	ExtAmt byte

	Mode   encoder.MemMode
	Base   encoder.Reg
	Index  encoder.Reg
	Disp   int64
	Scaled bool

	Slot string // OSlot: the value whose slot this is
}

func R(r encoder.Reg) Opr  { return Opr{Kind: OReg, Reg: r} }
func Rsp() Opr             { return Opr{Kind: OReg, Reg: encoder.SPr, SP: true} }
func Imm(v int64) Opr      { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }
func Slot(name string) Opr { return Opr{Kind: OSlot, Slot: name} }

func RShift(r encoder.Reg, shift, amt byte) Opr {
	return Opr{Kind: OReg, Reg: r, Shift: shift, ShiftAmt: amt}
}

func RExt(r encoder.Reg, ext, amt byte) Opr {
	return Opr{Kind: OExt, Reg: r, Ext: ext, ExtAmt: amt}
}

func Mem(base encoder.Reg, disp int64) Opr {
	return Opr{Kind: OMem, Base: base, Index: encoder.RNone, Disp: disp, Mode: encoder.MemOffset}
}

func MemPre(base encoder.Reg, disp int64) Opr {
	return Opr{Kind: OMem, Base: base, Index: encoder.RNone, Disp: disp, Mode: encoder.MemPre}
}

func MemPost(base encoder.Reg, disp int64) Opr {
	return Opr{Kind: OMem, Base: base, Index: encoder.RNone, Disp: disp, Mode: encoder.MemPost}
}

func MemSym(base encoder.Reg, sym string) Opr {
	return Opr{Kind: OMem, Base: base, Index: encoder.RNone, Sym: sym, Mode: encoder.MemOffset}
}

// Inst is one pre-encoding pseudo instruction. Op spellings are the
// encoder's, plus three epilogue pseudo-ops this package expands in
// encode.go:
//
//	epi_ret      the epilogue followed by `ret`
//	epi_b_sym    the epilogue followed by `b Sym`   (direct tailcall)
//	epi_br_reg   the epilogue followed by `br N`    (indirect tailcall)
//
// The pseudo-ops exist because a terminator is selected long before the frame
// is final, and because a tailcall needs the epilogue *and* no return
// address pushed.
type Inst struct {
	Op   string
	W    encoder.Width
	CC   encoder.Cond
	D    Opr
	N    Opr
	M    Opr
	A    Opr
	Lbl  string
	Sym  string
	Imm  int64
	Imm2 int64
}