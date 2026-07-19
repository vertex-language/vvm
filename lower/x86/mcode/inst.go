// Package mcode is the x86 (IA-32) machine-instruction and encoding layer.
// It has no knowledge of Vertex IR: it is a thin, x86-shaped pseudo
// instruction stream over physical registers, immediates, symbols, and
// value slots, plus the encoder that turns that stream into bytes. Both
// the compiler's own instruction selector (package x86) and the inline-asm
// lowerer (package inlineasm) emit into this same Inst stream, so there is
// exactly one ModRM/SIB encoder and one relocation model in the backend.
package mcode

type Reg byte

const (
	REAX  Reg = 0
	RECX  Reg = 1
	REDX  Reg = 2
	REBX  Reg = 3
	RESP  Reg = 4
	REBP  Reg = 5
	RESI  Reg = 6
	REDI  Reg = 7
	RNone Reg = 0xFF
)

// String supports diagnostics — e.g. an inline-asm error naming which
// physical register was involved.
func (r Reg) String() string {
	names := [...]string{"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi"}
	if int(r) < len(names) {
		return names[r]
	}
	return "?"
}

// SavedRegBytes is the number of bytes the prologue pushes for the
// callee-saved registers (EBX, ESI, EDI) below the caller's saved EBP.
// Exposed so package abi can compute EBP-relative slot offsets.
const SavedRegBytes = 12

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm  // immediate; Sym != "" makes it a symbol address (+ Imm addend)
	OMem  // [Base(+Index*Scale)+Disp], or absolute when Base/Index == RNone
	OSlot // a vir value's home slot; resolved by package regalloc
)

// Opr is one operand: a register, an immediate/symbol, a memory reference
// (optionally SIB-indexed), or an unresolved value slot.
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
	Slot  string // OSlot: the vir value name
}

func R(r Reg) Opr          { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr      { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

// Mem is a simple [base+disp] memory operand — the only form the
// compiler's own instruction selector needs.
func Mem(b Reg, d int32) Opr { return Opr{Kind: OMem, Base: b, Index: RNone, Disp: d} }

// MemIndexed is a full [base+index*scale+disp] memory operand. base may be
// RNone (pure index*scale+disp32, e.g. AT&T "(,%eax,4)"); index may not be
// RESP (ESP cannot be a SIB index register).
func MemIndexed(base, index Reg, scale byte, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: index, Scale: scale, Disp: disp}
}

// MemAbs is an absolute [msym+disp] reference (mod=00,rm=101 form).
func MemAbs(sym string, disp int32) Opr {
	return Opr{Kind: OMem, Base: RNone, Index: RNone, MSym: sym, Disp: disp}
}

func Slot(n string) Opr { return Opr{Kind: OSlot, Slot: n} }

// Condition codes (Intel tttn encoding: 0F 8x jcc, 0F 9x setcc, 0F 4x cmovcc).
const (
	CondO  = 0
	CondNO = 1
	CondB  = 2 // unsigned < (carry)
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

// Inst is one pre-encoding pseudo instruction. See encode.go's Encode
// switch for the authoritative list of Op spellings and which of D/S/CC/
// Sz/Lbl/Sym/Imm each one reads.
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