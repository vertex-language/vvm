// encoder/inst.go
//
// Package encoder is the generic x86-64 assembler: a pseudo-instruction
// stream (Inst/Opr) built entirely from physical registers, immediates,
// symbols, and plain memory operands — never a not-yet-placed value —
// plus the Encode function that turns that stream into machine bytes.
//
// It is the 64-bit sibling of isa/x86/encoder and keeps the same contract:
// no knowledge of any register-allocation policy or calling convention,
// reusable by anything that wants "turn this Inst stream into x86-64
// bytes". The differences from the 32-bit encoder are all consequences of
// long mode — a REX prefix computed from the operands, RIP-relative
// symbolic memory, the movabs 64-bit-immediate form, and REX-dependent
// byte-register rules — and are called out at the sites where they appear.
package encoder

import isax64 "github.com/vertex-language/vvm/isa/x86_64"

// Reg, register constants, and RNone are the same type/values as
// isa/x86_64's — aliased so callers building an Inst stream don't need to
// import isa/x86_64 directly for anything but the odd diagnostic.
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
)

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm // immediate; Sym != "" makes it a symbol address (+ Imm addend)
	OMem // [Base(+Index*Scale)+Disp], RIP-relative, or absolute
)

// Opr is one fully-resolved operand. There is deliberately no "unresolved
// slot" variant — that belongs one layer up, in whatever builds this
// stream.
//
// The memory operand covers three long-mode forms, distinguished by which
// fields are set:
//
//   - [base(+index*scale)+disp]   Base != RNone (and/or Index != RNone)
//   - [rip+sym+disp]              RIPSym != "" — RIP-relative, PC32 fixup
//   - [sym+disp] absolute         MSym != "", via the SIB no-base form
//   - [disp] absolute             Base == Index == RNone, no symbol
//
// RIPSym and MSym are mutually exclusive, and neither may be combined with
// a base or index register.
type Opr struct {
	Kind   OprKind
	Reg    Reg
	Imm    int64
	Sym    string // OImm: symbol whose address (+ Imm addend) this is
	Base   Reg    // OMem: RNone if absent
	Index  Reg    // OMem: RNone if no index register
	Scale  byte   // OMem: 1, 2, 4, or 8 (meaningful only when Index != RNone)
	Disp   int32  // OMem: displacement, or an absolute address's low bits
	MSym   string // OMem: symbol for an absolute [msym+disp] reference
	RIPSym string // OMem: symbol for a RIP-relative [rip+sym+disp] reference
}

func R(r Reg) Opr          { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr      { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

// Mem is a simple [base+disp] memory operand.
func Mem(b Reg, d int32) Opr { return Opr{Kind: OMem, Base: b, Index: RNone, Disp: d} }

// MemIndexed is a full [base+index*scale+disp] operand. base may be RNone
// (pure index*scale+disp32); index may not be RRSP (RSP cannot be a SIB
// index register).
func MemIndexed(base, index Reg, scale byte, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: index, Scale: scale, Disp: disp}
}

// MemRIP is a RIP-relative [rip+sym+disp] reference — the long-mode idiom
// for naming a global. It encodes as mod=00 rm=101 and carries a
// PC-relative disp32 fixup; the displacement is folded into the fixup
// addend. This is the default for symbolic memory: one byte shorter than
// an absolute form and position-independent.
func MemRIP(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: RNone, Index: RNone, RIPSym: sym, Disp: disp}
}

// MemAbs is an absolute [sym+disp] reference. In long mode the old
// mod=00 rm=101 disp32 form means RIP-relative, so a true absolute address
// must go through the SIB no-base form (mod=00, rm=100, SIB base=101,
// index=100). Prefer MemRIP unless an absolute address is specifically
// required.
func MemAbs(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: RNone, Index: RNone, MSym: sym, Disp: disp}
}

// Condition codes, re-exported from isa/x86_64. NegateCond is re-exported
// too, since inverting a branch is constant work for an instruction
// selector.
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

// NegateCond returns the condition code testing the complementary
// condition.
func NegateCond(cc byte) byte { return isax64.NegateCond(cc) }

// Inst is one pre-encoding pseudo instruction. encode.go's Encode switch
// is the authoritative list of Op spellings and of which fields each reads;
// the summary below is a map, not the territory.
//
//	Op            reads          notes
//	----------------------------------------------------------------
//	label         Lbl            defines a local branch target
//	mov           D, S, Sz       auto-promotes to movabs on a >imm32 const
//	movabs        D, S           D reg, S OImm — explicit 64-bit imm load
//	movzx/movsx   D, S, Sz       Sz is the *source* width: 1 or 2
//	lea           D, S           S must be OMem
//	add/or/and/
//	 sub/xor/cmp  D, S, Sz
//	test          D, S           two registers
//	imul1         S              F7 /5, widening into RDX:RAX
//	imul2         D, S           0F AF, two-operand
//	imul3         D, S, Imm      69/6B, three-operand
//	mul/div/idiv  S              F7 /4,/6,/7 — implicit RDX:RAX
//	not/neg       S              F7 /2,/3 — read-modify-write on S
//	inc/dec       D              FF /0,/1 — the one-byte forms are gone
//	cqo           —              sign-extend RAX into RDX:RAX (REX.W CDQ)
//	shl/shr/sar/
//	 rol/ror      D, S, Sz       S is OImm (count) or a register (CL)
//	setcc         D, CC          D must be byte-addressable
//	cmovcc        D, S, CC
//	jmp/jcc       Lbl, CC        always rel32; resolved by Encode
//	call_sym/
//	 jmp_sym      Sym            rel32 + FixupPCRel32
//	call_r/jmp_r  S              default 64-bit operand size, no REX.W
//	push          S              register, immediate, or memory (default 64)
//	pop           D              register or memory (default 64)
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
// Sz is an operand width in bytes: 1, 2, 4, or 8. Zero means unset and is
// treated as 8 — the natural default in a 64-bit backend, and the width at
// which pointer-sized work happens. (Note this differs from the 32-bit
// encoder, whose unset default is 4.)
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
	// Emitted for call/jmp rel32 (A = -4) and for RIP-relative memory
	// operands (A = disp - 4, since the reference point is the end of the
	// instruction, which for a trailing disp32 is the field's own end).
	FixupPCRel32 FixupKind = iota
	// FixupAbs32: field := S + A. Emitted for absolute 32-bit refs (the
	// SIB no-base absolute form).
	FixupAbs32
	// FixupAbs64: field := S + A. Emitted for movabs r64, imm64 when the
	// immediate is a symbol address — the only 64-bit-wide relocation this
	// backend produces.
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

// Fixup is a hole in emitted code/data that a downstream object writer must
// resolve. The addend is stored both here and written into the field
// itself, so REL-style (implicit-addend) and RELA-style consumers both work
// without rewriting bytes.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}