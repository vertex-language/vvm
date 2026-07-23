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
)

// Condition codes, re-exported from the encoder (which itself re-exports
// isa/x86_64's tttn encoding), the same way Reg is aliased above. isel.go's
// cmpCC/selCompare/selShift/etc. reference these bare (CondE, CondNE, ...)
// without importing either isa/x86_64 or its encoder directly.
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
)

// OprKind is this backend's pre-encoding operand tag. It mirrors the
// encoder's, plus OSlot — a not-yet-placed stack slot that encode.go
// resolves to [rbp+off] before handing bytes to the encoder.
type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm
	OMem
	OSlot // resolved to Mem(RBP, Frame.Offset(Slot)) in encode.go
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
	Slot   int // OSlot: index into the frame's local-slot array
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

// Inst is one pre-encoding pseudo instruction. Op spellings are exactly the
// encoder's (encode.go's switch is authoritative) plus three epilogue
// pseudo-ops the encoder never sees:
//
//	epi_ret       expand epilogue, then ret
//	epi_jmp_sym   expand epilogue, then jmp <Sym>   (tailcall to symbol)
//	epi_jmp_r     expand epilogue, then jmp <S reg> (indirect tailcall)
//
// Sz is an operand width in bytes (1/2/4/8); 0 means unset and is treated
// as 8, the pointer/register width of a 64-bit backend.
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