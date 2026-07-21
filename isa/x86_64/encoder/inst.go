// Package encoder is a generic x86-64 assembler: it turns an instruction
// stream built from isa/x86_64's facts into machine bytes. It has no vir
// knowledge, no OS knowledge, and no register-allocation policy — Opr has
// no "unresolved slot" variant at all, so every operand this package sees
// already names a concrete register, immediate, symbol, or memory
// location. It also does no prologue/epilogue splicing: a function's
// frame setup and teardown are ordinary instructions in the stream,
// staged by whatever lowering package builds one (see lower/x86_64), not
// bytes this package injects on the caller's behalf.
package encoder

import x86_64 "github.com/vertex-language/vvm/isa/x86_64"

type Reg = x86_64.Reg

type OprKind byte

const (
	KNone OprKind = iota
	KReg
	KImm // immediate
	KSym // a symbol address, materialized RIP-relatively (+imm addend)
	KMem // [base+disp], or absolute [disp32] when Base == RNone
	KRIP // [rip + sym + disp] — RIP-relative memory operand
)

type Opr struct {
	K    OprKind
	Reg  Reg
	Imm  int64
	Sym  string
	Base Reg
	Disp int32
}

func R(r Reg) Opr            { return Opr{K: KReg, Reg: r} }
func Imm(v int64) Opr        { return Opr{K: KImm, Imm: v} }
func SymAddr(s string) Opr   { return Opr{K: KSym, Sym: s} }
func Mem(b Reg, d int32) Opr { return Opr{K: KMem, Base: b, Disp: d} }
func RipMem(s string) Opr    { return Opr{K: KRIP, Sym: s} }

// Inst ops (Op field). Sz ∈ {1, 2, 4, 8}; 8 selects REX.W forms. This is
// the shared vocabulary lower/x86_64's isel and inlineasm both target.
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
//	setcc                 cc, d reg (low byte; RAX..RBX only in this bring-up)
//	cmovcc                cc, d reg, s reg/mem; sz 4|8
//	jmp / jcc             lbl (intra-stream, rel32, patched against labels)
//	jmp_sym / call_sym    sym (rel32, fixup — jumps/calls an external symbol)
//	jmp_r / call_r        s reg (indirect; FF /4 and FF /2 respectively)
//	push / pop            s reg / d reg (64-bit)
//	ret ud2 nop mfence cld std rep_movsb rep_stosb
//	syscall               SYSCALL trap (0F 05); no operands
//	bsr bsf               d reg, s reg/mem; sz 4|8
//	bswap                 d reg; sz 4|8
//	xchg / lock_xadd / lock_cmpxchg   d mem, s reg; sz 4|8
//	popcnt                d reg, s reg/mem; sz 4|8
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

// Fixup is a hole in the emitted bytes that a downstream consumer (an
// object-file writer, a JIT loader) must resolve.
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