// Package arm lowers verified vir modules to 32-bit ARM (A32) machine code,
// in either byte order: arch "arm" (little-endian) or "armeb" (big-endian),
// selected by the Arch argument to Lower (see arch.go for the armeb object
// convention and the BE-8 link-time contract).
//
// Arrow 3 of the README taxonomy: vir.Module -> arm.Program, and nothing
// more. This package imports only ir/vir; it knows A32 instruction encoding
// and the AAPCS ABI, and knows nothing about ELF/COFF/Mach-O.
//
// ABI: AAPCS. First four word arguments in r0-r3, the rest on the stack
// (first stacked argument at SP at the call); result in r0; caller owns the
// stacked-argument area. r0-r3 and r12 (IP) caller-saved; r4-r11
// callee-saved; r11 is the frame pointer. SP is 8-byte aligned at every
// public call boundary. Endianness changes no offsets and no calling
// convention rules — it governs only serialization.
//
// Code model: non-PIC, ARM state only. Symbol addresses materialize as
// movw/movt pairs (FixupMovwAbs + FixupMovtAbs); calls and cross-function
// jumps are 24-bit PC-relative branches (FixupCall24 / FixupJump24, PC bias
// A = -8). Data references in globals are FixupAbs32. object/arm maps these
// onto the R_ARM_MOVW_ABS_NC / R_ARM_MOVT_ABS / R_ARM_CALL / R_ARM_JUMP24 /
// R_ARM_ABS32 shapes — AAELF32 relocation codes are endian-agnostic; only
// the byte order of the containers they patch differs, and Program.Arch
// tells the consumer which order that is.
//
// Coverage: the integer/pointer subset of Vertex IR. Floats, vectors,
// i64/i128 named values, saturating arithmetic, popcnt, inline asm, byval/
// sret, sub-32-bit atomic RMW, and frame-growing tailcalls are rejected
// with explicit errors (TODO, tier work). udiv/sdiv lowering emits the
// UDIV/SDIV instructions, which require an integer-divide-capable core
// (ARMv7VE / ARMv8 A32) — tier gating TODO (§10.4).
package arm

// Program is a self-contained description of the lowered module:
// instruction bytes, symbols, and unresolved fixups — plus the Arch that
// fixes the byte order of every word in Code and every scalar in Data.
type Program struct {
	Arch    Arch
	Funcs   []Func
	Globals []Global
}

type Func struct {
	Name   string
	Code   []byte
	Align  uint32
	Export bool
	Fixups []Fixup
}

type Global struct {
	Name   string
	Data   []byte // nil for zero (BSS-style) storage
	Size   uint32 // may exceed len(Data) for zero fill
	Align  uint32
	Export bool
	TLS    bool
	Fixups []Fixup
}

// Fixup is a hole in Code/Data that a downstream consumer must resolve.
// For the branch kinds, Offset addresses the whole instruction word and the
// addend (-8, the A32 PC bias) is pre-encoded into the imm24 field; for the
// movw/movt kinds the addend halves are pre-encoded into the split imm16
// field. Pre-encoding happens at the word level and is therefore
// byte-order-neutral: REL-style (implicit-addend) and RELA-style consumers
// both work without rewriting bytes, provided they read/write the patched
// container in Program.Arch's byte order.
type Fixup struct {
	Offset uint32 // byte offset of the instruction word / 32-bit field
	Symbol string
	Kind   FixupKind
	Addend int64
}

// FixupKind is arm's own vocabulary for what kind of hole this is.
type FixupKind int

const (
	// FixupCall24: BL — imm24 := ((S + A - P) >> 2) & 0xFFFFFF, A = -8.
	FixupCall24 FixupKind = iota
	// FixupJump24: B — same field arithmetic as FixupCall24 (tailcalls).
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