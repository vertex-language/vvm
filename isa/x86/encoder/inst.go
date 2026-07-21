// Package encoder is the generic IA-32 assembler: a pseudo-instruction
// stream (Inst/Opr) built entirely from physical registers, immediates,
// symbols, and plain memory operands — never a not-yet-placed value — plus
// the Encode function that turns that stream into machine bytes.
//
// This package has no knowledge of vir, of register allocation policy, or
// of any particular calling convention. It is reusable by anything that
// wants "turn this Inst stream into IA-32 bytes": lower/x86's instruction
// selector and inline-asm lowerer both build into this Inst type (via
// lower/x86/mcode, which adds the one concept this package deliberately
// omits — an unresolved value slot — and converts down to this package's
// Opr once regalloc has resolved every slot to a real memory operand).
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
// values that carry a CC field.
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
	CondL  = isax86.CondL
	CondGE = isax86.CondGE
	CondLE = isax86.CondLE
	CondG  = isax86.CondG
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