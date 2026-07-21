// Package encoder is the generic A32 assembler: a pseudo-instruction
// stream (Inst/Opr) built entirely from physical registers, immediates,
// symbols, and plain memory operands — never a not-yet-placed value, and
// never an implicit stack frame — plus the Encode function that turns
// that stream into machine words in either byte order (arm/armeb).
//
// This package has no knowledge of vir, of register allocation policy, of
// any particular calling convention, and — unlike an encoder that grew a
// frame convention by accretion — no automatic prologue/epilogue
// splicing. A caller that wants a stack frame builds it as ordinary
// push/mov/sub/pop/b Insts and prepends/appends them itself (see
// lower/arm's own frame code, once written, for this backend's actual
// frame shape). push/pop here take an arbitrary register-list bitmask —
// the real STMDB/LDMIA-with-writeback shape A32 provides — not a
// hardcoded register pair, so nothing about "which registers get saved"
// leaks into this package.
//
// One scratch register is reserved by convention: RIP (r12, "intra-
// procedure-call scratch" in AAPCS terms) is used internally to
// materialize data-processing immediates that don't fit the rotated-
// immediate form. Callers building an Inst stream must keep RIP dead
// across any "add"/"sub"/"and"/etc. site that might carry a wide
// immediate.
package encoder

import isaarm "github.com/vertex-language/vvm/isa/arm"

// Reg and the register constants are the same type/values as isa/arm's —
// aliased here so callers building an Inst stream don't need to import
// isa/arm directly for anything but the odd diagnostic.
type Reg = isaarm.Reg

const (
	R0    = isaarm.R0
	R1    = isaarm.R1
	R2    = isaarm.R2
	R3    = isaarm.R3
	R4    = isaarm.R4
	R5    = isaarm.R5
	R6    = isaarm.R6
	R7    = isaarm.R7
	R8    = isaarm.R8
	R9    = isaarm.R9
	R10   = isaarm.R10
	RFP   = isaarm.RFP
	RIP   = isaarm.RIP
	RSP   = isaarm.RSP
	RLR   = isaarm.RLR
	RPC   = isaarm.RPC
	RNone = isaarm.RNone
)

// Condition codes, re-exported from isa/arm for callers building Inst
// values that carry a CC field ("bcc", "movcc").
const (
	CondEQ = isaarm.CondEQ
	CondNE = isaarm.CondNE
	CondHS = isaarm.CondHS
	CondLO = isaarm.CondLO
	CondMI = isaarm.CondMI
	CondPL = isaarm.CondPL
	CondVS = isaarm.CondVS
	CondVC = isaarm.CondVC
	CondHI = isaarm.CondHI
	CondLS = isaarm.CondLS
	CondGE = isaarm.CondGE
	CondLT = isaarm.CondLT
	CondGT = isaarm.CondGT
	CondLE = isaarm.CondLE
	CondAL = isaarm.CondAL
)

type OprKind byte

const (
	ONone OprKind = iota
	OReg
	OImm // immediate; Sym != "" makes it a symbol address (+ Imm addend)
	OMem // [Base+Disp], or [Base+Index] (register-offset) when Index != RNone
)

// Opr is one fully-resolved operand: a register, an immediate/symbol, or
// a memory reference. There is deliberately no "unresolved slot" variant
// here — that concept belongs one layer up, in whatever's building this
// stream, not in the generic encoder.
type Opr struct {
	Kind  OprKind
	Reg   Reg
	Imm   int64
	Sym   string // OImm: symbol whose address (+ Imm addend) this immediate is
	Base  Reg    // OMem: the base register
	Index Reg    // OMem: RNone for [Base+Disp]; else register-offset [Base+Index]
	Disp  int32  // OMem: displacement (meaningless when Index != RNone)
}

func R(r Reg) Opr        { return Opr{Kind: OReg, Reg: r} }
func Imm(v int64) Opr    { return Opr{Kind: OImm, Imm: v} }
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

// Mem is a [Base+Disp] memory operand (LDR/STR-family immediate-offset
// addressing).
func Mem(b Reg, d int32) Opr { return Opr{Kind: OMem, Base: b, Index: RNone, Disp: d} }

// MemIndexed is a [Base+Index] register-offset memory operand (the
// ldrb_r/strb_r loop-indexed addressing form).
func MemIndexed(base, index Reg) Opr {
	return Opr{Kind: OMem, Base: base, Index: index}
}

// Inst is one pre-encoding pseudo instruction. See encode.go's Encode
// switch for the authoritative list of Op spellings and which of
// D/S/T/X/CC/RegList/Lbl/Sym/Imm each one reads. Convention shared with
// isa/x86/encoder: for a store-shaped op (str/strb/...), D holds the
// memory operand and S holds the value; for a load-shaped op (ldr/...),
// S holds the memory operand and D holds the destination register.
type Inst struct {
	Op      string
	D, S, T, X Opr
	CC      byte   // condition code, read by "bcc" and "movcc"
	RegList uint16 // register bitmask (bit i = ri), read by "push"/"pop"
	Lbl     string
	Sym     string
	Imm     int64
}

// FixupKind is this backend's vocabulary for what kind of relocation hole
// a Fixup describes.
type FixupKind int

const (
	// FixupCall24: BL — imm24 := ((S + A - P) >> 2) & 0xFFFFFF, A = -PCBias.
	FixupCall24 FixupKind = iota
	// FixupJump24: B — same field arithmetic as FixupCall24, for a plain
	// (non-linking) branch to a symbol.
	FixupJump24
	// FixupMovwAbs: MOVW — split imm16 := (S + A) & 0xFFFF.
	FixupMovwAbs
	// FixupMovtAbs: MOVT — split imm16 := ((S + A) >> 16) & 0xFFFF.
	FixupMovtAbs
	// FixupAbs32: plain 32-bit data word := S + A (global initializers).
	FixupAbs32
)

func (k FixupKind) String() string {
	switch k {
	case FixupCall24:
		return "call24"
	case FixupJump24:
		return "jump24"
	case FixupMovwAbs:
		return "movw_abs"
	case FixupMovtAbs:
		return "movt_abs"
	case FixupAbs32:
		return "abs32"
	}
	return "fixup?"
}

// Fixup is a hole in emitted code/data that a downstream object writer
// must resolve. The addend is stored both here and pre-encoded into the
// field itself, so REL-style (implicit-addend) and RELA-style consumers
// both work without rewriting bytes, provided they read/write the
// patched container in the requested byte order.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}