// Package encoder is the generic IA-32 assembler: a pseudo-instruction
// stream (Inst/Opr) built entirely from physical registers, immediates,
// symbols, and plain memory operands — never a not-yet-placed value —
// plus the Encode function that turns that stream into machine bytes.
//
// This package has no knowledge of vir, of register allocation policy, or
// of any particular calling convention. It is reusable by anything that
// wants "turn this Inst stream into IA-32 bytes". lower/x86 is this
// repository's only caller: it builds into its own near-identical Inst
// type that adds the one concept this package deliberately omits — an
// unresolved value slot — and converts down to this package's Opr once
// every slot has been resolved to a real [ebp+disp] memory operand.
package encoder

import isax86 "github.com/vertex-language/vvm/isa/x86"

// Reg, register constants, and RNone are the same type/values as isa/x86's
// — aliased here so callers building an Inst stream don't need to import
// isa/x86 directly for anything but the odd diagnostic.
type Reg = isax86.Reg

const (
	REAX  = isax86.REAX
	RECX  = isax86.RECX
	REDX  = isax86.REDX
	REBX  = isax86.REBX
	RESP  = isax86.RESP
	REBP  = isax86.REBP
	RESI  = isax86.RESI
	REDI  = isax86.REDI
	RNone = isax86.RNone
)

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm // immediate; Sym != "" makes it a symbol address (+ Imm addend)
	OMem // [Base(+Index*Scale)+Disp], or absolute when Base/Index == RNone
)

// Opr is one fully-resolved operand: a register, an immediate/symbol, or a
// memory reference (optionally SIB-indexed). There is deliberately no
// "unresolved slot" variant here — that concept belongs one layer up, in
// whatever's building this stream, not in the generic encoder.
type Opr struct {
	Kind  OprKind
	Reg   Reg
	Imm   int64
	Sym   string // OImm: symbol whose address (+ Imm addend) this immediate is
	Base  Reg    // OMem: RNone if absent (pure index, or absolute)
	Index Reg    // OMem: RNone if no index register
	Scale byte   // OMem: 1, 2, 4, or 8 (only meaningful when Index != RNone)
	Disp  int32  // OMem: displacement, or the absolute address's low bits
	MSym  string // OMem: symbol for an absolute [msym+disp] reference
}

func R(r Reg) Opr          { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr      { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

// Mem is a simple [base+disp] memory operand.
func Mem(b Reg, d int32) Opr { return Opr{Kind: OMem, Base: b, Index: RNone, Disp: d} }

// MemIndexed is a full [base+index*scale+disp] memory operand. base may be
// RNone (pure index*scale+disp32); index may not be RESP (ESP cannot be a
// SIB index register).
func MemIndexed(base, index Reg, scale byte, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: index, Scale: scale, Disp: disp}
}

// MemAbs is an absolute [msym+disp] reference (mod=00,rm=101 form).
func MemAbs(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: RNone, Index: RNone, MSym: sym, Disp: disp}
}

// Condition codes, re-exported from isa/x86 for callers building Inst
// values that carry a CC field. NegateCond is re-exported too, since
// inverting a branch is something an instruction selector does constantly
// and shouldn't need a second import for.
const (
	CondO  = isax86.CondO
	CondNO = isax86.CondNO
	CondB  = isax86.CondB
	CondAE = isax86.CondAE
	CondE  = isax86.CondE
	CondNE = isax86.CondNE
	CondBE = isax86.CondBE
	CondA  = isax86.CondA
	CondS  = isax86.CondS
	CondNS = isax86.CondNS
	CondP  = isax86.CondP
	CondNP = isax86.CondNP
	CondL  = isax86.CondL
	CondGE = isax86.CondGE
	CondLE = isax86.CondLE
	CondG  = isax86.CondG
)

// NegateCond returns the condition code testing the complementary
// condition.
func NegateCond(cc byte) byte { return isax86.NegateCond(cc) }

// Inst is one pre-encoding pseudo instruction. encode.go's Encode switch
// is the authoritative list of Op spellings and of which of D/S/CC/Sz/
// Lbl/Sym/Imm each one reads; the summary below is a map, not the
// territory.
//
//	Op            reads          notes
//	----------------------------------------------------------------
//	label         Lbl            defines a local branch target
//	mov           D, S, Sz
//	movzx/movsx   D, S, Sz       Sz is the *source* width: 1 or 2
//	lea           D, S           S must be OMem
//	add/or/and/
//	 sub/xor/cmp  D, S, Sz
//	test          D, S           two registers
//	imul1         S              0xF7 /5, widening into EDX:EAX
//	imul2         D, S           0x0F 0xAF, two-operand
//	imul3         D, S, Imm      0x69 or 0x6B, three-operand
//	mul/div/idiv  S              0xF7 /4,/6,/7 — implicit EDX:EAX
//	not/neg       S              0xF7 /2,/3 — read-modify-write on S
//	inc/dec       D
//	cdq           —              sign-extend EAX into EDX:EAX
//	shl/shr/sar/
//	 rol/ror      D, S, Sz       S is OImm (count) or a register (CL)
//	setcc         D, CC          D must be byte-addressable
//	cmovcc        D, S, CC
//	jmp/jcc       Lbl, CC        always rel32; resolved by Encode
//	call_sym/
//	 jmp_sym      Sym            rel32 + FixupPCRel32
//	call_r/jmp_r  S
//	push          S              register, immediate, or memory
//	pop           D              register or memory
//	ret/ud2/nop/
//	 cld/std/
//	 mfence       —
//	int           Imm            Imm == 3 emits the one-byte int3
//	bsr/bsf/
//	 popcnt       D, S
//	bswap         D
//	xchg          D, S
//	lock_xadd/
//	 lock_cmpxchg D, S
//	rep_movsb/
//	 rep_stosb    —
//
// Sz is an operand width in bytes: 1, 2, or 4. Zero means unset and is
// treated as 4, which is what almost every instruction in a 32-bit
// backend wants.
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

// FixupKind is this backend's vocabulary for what kind of relocation hole
// a Fixup describes.
type FixupKind int

const (
	// FixupPCRel32: field := S + A - P, where P is the field's address.
	// Emitted for call/jmp rel32 with A = -4 (field precedes next insn by 4).
	FixupPCRel32 FixupKind = iota
	// FixupAbs32: field := S + A. Emitted for absolute data/address refs.
	FixupAbs32
)

func (k FixupKind) String() string {
	switch k {
	case FixupPCRel32:
		return "pcrel32"
	case FixupAbs32:
		return "abs32"
	}
	return "fixup?"
}

// Fixup is a hole in emitted code/data that a downstream object writer must
// resolve. The addend is stored both here and written into the 32-bit
// field itself, so REL-style (implicit-addend, i386 ELF) and RELA-style
// consumers both work without rewriting bytes.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}