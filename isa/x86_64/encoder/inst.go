// encoder/inst.go
package encoder

import isax64 "github.com/vertex-language/vvm/isa/x86_64"

type Reg = isax64.Reg

const (
	RRAX = isax64.RRAX
	RRCX = isax64.RRCX
	RRDX = isax64.RRDX
	RRBX = isax64.RRBX
	RRSP = isax64.RRSP
	RRBP = isax64.RRBP
	RRSI = isax64.RRSI
	RRDI = isax64.RRDI
	RR8  = isax64.RR8
	RR9  = isax64.RR9
	RR10 = isax64.RR10
	RR11 = isax64.RR11
	RR12 = isax64.RR12
	RR13 = isax64.RR13
	RR14 = isax64.RR14
	RR15 = isax64.RR15
	RNone = isax64.RNone

	RXMM0  = isax64.RRAX
	RXMM1  = isax64.RRCX
	RXMM2  = isax64.RRDX
	RXMM3  = isax64.RRBX
	RXMM4  = isax64.RRSP
	RXMM5  = isax64.RRBP
	RXMM6  = isax64.RRSI
	RXMM7  = isax64.RRDI
	RXMM8  = isax64.RR8
	RXMM9  = isax64.RR9
	RXMM10 = isax64.RR10
	RXMM11 = isax64.RR11
	RXMM12 = isax64.RR12
	RXMM13 = isax64.RR13
	RXMM14 = isax64.RR14
	RXMM15 = isax64.RR15
)

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm 
	OMem 
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

const (
	CondO  = isax64.CondO
	CondNO = isax64.CondNO
	CondB  = isax64.CondB
	CondAE = isax64.CondAE
	CondE  = isax64.CondE
	CondNE = isax64.CondNE
	CondBE = isax64.CondBE
	CondA  = isax64.CondA
	CondS  = isax64.CondS
	CondNS = isax64.CondNS
	CondP  = isax64.CondP
	CondNP = isax64.CondNP
	CondL  = isax64.CondL
	CondGE = isax64.CondGE
	CondLE = isax64.CondLE
	CondG  = isax64.CondG
)

func NegateCond(cc byte) byte { return isax64.NegateCond(cc) }

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

type FixupKind int

const (
	FixupPCRel32 FixupKind = iota
	FixupAbs32
	FixupAbs64
)

func (k FixupKind) String() string {
	switch k {
	case FixupPCRel32:
		return "pcrel32"
	case FixupAbs32:
		return "abs32"
	case FixupAbs64:
		return "abs64"
	}
	return "fixup?"
}

type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}