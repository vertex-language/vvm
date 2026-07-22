// opr.go
package x86

import isax86 "github.com/vertex-language/vvm/isa/x86"

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm  // immediate; Sym != "" makes it a symbol address (+ Imm addend)
	OMem  // [Base(+Index*Scale)+Disp], or absolute when Base/Index == isax86.RNone
	OSlot // a vir value's home slot; resolved during final assembly (encode.go)
)

type Opr struct {
	Kind  OprKind
	Reg   isax86.Reg
	Imm   int64
	Sym   string
	Base  isax86.Reg
	Index isax86.Reg
	Scale byte
	Disp  int32
	MSym  string
	Slot  string
}

func R(r isax86.Reg) Opr   { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr      { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }
func Slot(name string) Opr { return Opr{Kind: OSlot, Slot: name} }

func Mem(b isax86.Reg, d int32) Opr {
	return Opr{Kind: OMem, Base: b, Index: isax86.RNone, Disp: d}
}

func MemIndexed(base, index isax86.Reg, scale byte, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: index, Scale: scale, Disp: disp}
}

func MemAbs(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: isax86.RNone, Index: isax86.RNone, MSym: sym, Disp: disp}
}

type Inst struct {
	Op  string
	D   Opr
	S   Opr
	CC  byte
	Sz  int
	Lbl string
	Sym string
	Imm int64
}