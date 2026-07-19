// Package mcode is the x86-64-shaped pseudo-instruction stream: Inst/Opr,
// physical registers, and the relocation model. Isel and inlineasm both
// produce Inst streams in this vocabulary; Encode is the single place that
// turns them into bytes.
package mcode

type Reg byte

const (
	RAX Reg = 0
	RCX Reg = 1
	RDX Reg = 2
	RBX Reg = 3
	RSP Reg = 4
	RBP Reg = 5
	RSI Reg = 6
	RDI Reg = 7
	R8  Reg = 8
	R9  Reg = 9
	R10 Reg = 10
	R11 Reg = 11
	R12 Reg = 12
	R13 Reg = 13
	R14 Reg = 14
	R15 Reg = 15

	RNone Reg = 0xFF
)

type OprKind byte

const (
	KNone OprKind = iota
	KReg
	KImm  // immediate
	KSym  // a symbol address, materialized RIP-relatively (+imm addend)
	KMem  // [base+disp]
	KRIP  // [rip + sym + disp] — RIP-relative memory operand
	KSlot // a vir value's home slot; resolved by regalloc
)

type Opr struct {
	K    OprKind
	Reg  Reg
	Imm  int64
	Sym  string
	Base Reg
	Disp int32
	Slot string
}

func R(r Reg) Opr             { return Opr{K: KReg, Reg: r} }
func Imm(v int64) Opr         { return Opr{K: KImm, Imm: v} }
func SymAddr(s string) Opr    { return Opr{K: KSym, Sym: s} }
func Mem(b Reg, d int32) Opr  { return Opr{K: KMem, Base: b, Disp: d} }
func RipMem(s string) Opr     { return Opr{K: KRIP, Sym: s} }
func SlotOpr(n string) Opr    { return Opr{K: KSlot, Slot: n} }

// Condition codes (Intel tttn encoding: 0F 8x jcc, 0F 9x setcc, 0F 4x cmovcc).
const (
	CondO  = 0
	CondNO = 1
	CondB  = 2 // unsigned <  (carry)
	CondAE = 3
	CondE  = 4
	CondNE = 5
	CondBE = 6
	CondA  = 7
	CondS  = 8
	CondNS = 9
	CondL  = 12 // signed 
	CondGE = 13
	CondLE = 14
	CondG  = 15
)

// Inst ops (Op field). Sz ∈ {1, 2, 4, 8}; 8 selects REX.W forms.
//
//	label                 lbl
//	mov                   d,s (reg<-imm/imm64, reg<-sym [lea rip], reg<-reg,
//	                           reg<-mem, mem<-reg[sz], mem<-imm32)
//	movzx / movsx         d reg, s reg/mem, sz 1|2|4 (4 = movsxd for movsx,
//	                           plain 32-bit mov for movzx)
//	lea                   d reg, s mem/rip
//	add or and sub xor cmp  d,s in reg/mem/imm combinations; sz 4|8
//	test                  d reg, s reg; sz 4|8
//	imul                  d reg, s reg/mem (0F AF); sz 4|8
//	imul3                 d reg, s reg/mem, imm (69 /r id); sz 4|8
//	not neg mul1 imul1 div idiv   s reg (F7 group); sz 4|8
//	inc dec               s reg/mem (FF group); sz 4|8
//	cdq / cqo
//	shl shr sar rol ror   d reg, s = imm (Cx) or CL (Dx); sz 1|2|4|8
//	setcc                 cc, d reg (low byte; RAX..RBX only)
//	cmovcc                cc, d reg, s reg/mem; sz 4|8
//	jmp / jcc             lbl (intra-function, rel32, patched)
//	call_sym / call_r     sym / s reg
//	push / pop            s reg / d reg (64-bit)
//	ret ud2 nop mfence cld std rep_movsb rep_stosb
//	syscall               SYSCALL trap (0F 05); no operands
//	bsr bsf               d reg, s reg/mem; sz 4|8
//	bswap                 d reg; sz 4|8
//	xchg / lock_xadd / lock_cmpxchg   d mem, s reg; sz 4|8
//	popcnt                d reg, s reg/mem; sz 4|8 (tier-gated, TODO §10.4)
//	epi_ret / epi_jmp_sym / epi_jmp_r
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

// Fixup is a hole in Code/Data that a downstream consumer must resolve.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}

type FixupKind int

const (
	FixupPCRel32Call FixupKind = iota
	FixupPCRel32
	FixupAbs64
)

func (k FixupKind) String() string {
	switch k {
	case FixupPCRel32Call:
		return "pcrel32call"
	case FixupPCRel32:
		return "pcrel32"
	case FixupAbs64:
		return "abs64"
	}
	return "fixup?"
}