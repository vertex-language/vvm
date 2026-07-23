// opr.go
package x86_64

import enc "github.com/vertex-language/vvm/isa/x86_64/encoder"

// Reg reuses isa/x86_64's register identity via the encoder alias, so this
// package never redefines the sixteen GPRs.
type Reg = enc.Reg

const (
	RRAX  = enc.RRAX
	RRCX  = enc.RRCX
	RRDX  = enc.RRDX
	RRBX  = enc.RRBX
	RRSP  = enc.RRSP
	RRBP  = enc.RRBP
	RRSI  = enc.RRSI
	RRDI  = enc.RRDI
	RR8   = enc.RR8
	RR9   = enc.RR9
	RR10  = enc.RR10
	RR11  = enc.RR11
	RR12  = enc.RR12
	RR13  = enc.RR13
	RR14  = enc.RR14
	RR15  = enc.RR15
	RNone = enc.RNone

	// XMM register aliases (sharing the 0-15 encoding space in ModRM)
	RXMM0  = enc.RRAX
	RXMM1  = enc.RRCX
	RXMM2  = enc.RRDX
	RXMM3  = enc.RRBX
	RXMM4  = enc.RRSP
	RXMM5  = enc.RRBP
	RXMM6  = enc.RRSI
	RXMM7  = enc.RRDI
	RXMM8  = enc.RR8
	RXMM9  = enc.RR9
	RXMM10 = enc.RR10
	RXMM11 = enc.RR11
	RXMM12 = enc.RR12
	RXMM13 = enc.RR13
	RXMM14 = enc.RR14
	RXMM15 = enc.RR15
)

// Condition codes, re-exported from the encoder (which itself re-exports
// isa/x86_64's tttn encoding).
const (
	CondO  = enc.CondO
	CondNO = enc.CondNO
	CondB  = enc.CondB
	CondAE = enc.CondAE
	CondE  = enc.CondE
	CondNE = enc.CondNE
	CondBE = enc.CondBE
	CondA  = enc.CondA
	CondL  = enc.CondL
	CondGE = enc.CondGE
	CondLE = enc.CondLE
	CondG  = enc.CondG
	CondP  = enc.CondP
	CondNP = enc.CondNP
)

// OprKind is this backend's pre-encoding operand tag.
type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm
	OMem
	OSlot
)

type Opr struct {
	Kind   OprKind
	Reg    Reg
	Imm    int64
	Sym    string
	Base   Reg
	Index  Reg
	Scale  byte
	Disp   int32
	MSym   string
	RIPSym string
	Slot   int
}

func R(r Reg) Opr          { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr      { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

func Mem(b Reg, d int32) Opr { return Opr{Kind: OMem, Base: b, Index: RNone, Disp: d} }
func MemIndexed(base, index Reg, scale byte, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: index, Scale: scale, Disp: disp}
}
func MemRIP(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: RNone, Index: RNone, RIPSym: sym, Disp: disp}
}
func MemAbs(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: RNone, Index: RNone, MSym: sym, Disp: disp}
}
func Slot(n int) Opr { return Opr{Kind: OSlot, Slot: n} }

func (o Opr) isSlot() bool { return o.Kind == OSlot }

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