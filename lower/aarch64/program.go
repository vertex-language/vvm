// Package aarch64 lowers verified vir modules to 64-bit ARM (A64) machine
// code for arch "aarch64" (little-endian data) or "aarch64_be" (big-endian
// data), selected by the Arch argument to Lower (see arch.go for why the
// distinction never touches Code).
//
// Arrow 3 of the README taxonomy: vir.Module -> aarch64.Program, and
// nothing more. This package imports only ir/vir; it knows A64 instruction
// encoding and the AAPCS64 ABI, and knows nothing about ELF containers.
//
// ABI: AAPCS64. First eight word arguments in x0-x7, the rest on the stack
// (first stacked argument at SP at the call, 8 bytes per slot); result in
// x0; x8 is the indirect-result register (reserved here until sret is
// lowered); caller owns the stacked-argument area. x0-x17 caller-saved;
// x19-x28 callee-saved; x29 is the frame pointer, x30 the link register,
// x18 the platform register (never touched). SP is 16-byte aligned at
// every public call boundary. Data endianness changes no offsets and no
// calling-convention rules — it governs only Data serialization.
//
// Code model: non-PIC. Symbol addresses materialize as a movz+movk×3 quad
// (FixupMovzG3 + FixupMovkG2/G1/G0 — the R_AARCH64_MOVW_UABS_G3/G2_NC/
// G1_NC/G0_NC shapes: the checking form relocates the MOVZ, the _NC forms
// relocate MOVKs). Calls and cross-function jumps are 26-bit PC-relative
// branches (FixupCall26 / FixupJump26 — R_AARCH64_CALL26 / R_AARCH64_JUMP26,
// S + A - P with A = 0: A64 has no A32-style PC+8 bias). Data references
// in globals are FixupAbs64 (R_AARCH64_ABS64). adrp+lo12 small-code-model
// addressing is the deliberate codegen upgrade path, not the bring-up.
//
// Coverage: the integer/pointer subset of Vertex IR, now including native
// i64 named values (usize is i64). Floats, vectors, i128 named values,
// saturating arithmetic, popcnt (scalar CNT is FEAT_CSSC; NEON otherwise),
// inline asm, byval/sret, sub-32-bit atomic RMW, TLS global addressing
// (TPIDR_EL0 + TLS relocs), and frame-growing tailcalls are rejected with
// explicit errors (TODO, tier work). LSE atomics (ARMv8.1) are a §10.4
// feature-tier candidate; the baseline emits ldaxr/stlxr loops.
package aarch64

// Program is a self-contained description of the lowered module:
// instruction bytes, symbols, and unresolved fixups — plus the Arch that
// fixes the byte order of every scalar in Data (and only Data; see arch.go).
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
// For the branch kinds, Offset addresses the whole instruction word and
// the imm26 field is pre-encoded to zero (A = 0 on A64 — no PC bias); for
// the movz/movk kinds the addend's group halfword is pre-encoded into the
// imm16 field. Instruction words are always little-endian, so REL-style
// (implicit-addend) and RELA-style consumers both work without byte-order
// caveats; Data fixup fields follow Program.Arch's byte order.
type Fixup struct {
	Offset uint32 // byte offset of the instruction word / 64-bit data field
	Symbol string
	Kind   FixupKind
	Addend int64
}

// FixupKind is this package's own vocabulary for what kind of hole this is.
type FixupKind int

const (
	// FixupCall26: BL — imm26 := ((S + A - P) >> 2) & 0x3FFFFFF, A = 0.
	FixupCall26 FixupKind = iota
	// FixupJump26: B — same field arithmetic as FixupCall26 (tailcalls).
	FixupJump26
	// FixupMovzG3: MOVZ — imm16 := ((S + A) >> 48) & 0xFFFF (checking form).
	FixupMovzG3
	// FixupMovkG2: MOVK — imm16 := ((S + A) >> 32) & 0xFFFF (no check).
	FixupMovkG2
	// FixupMovkG1: MOVK — imm16 := ((S + A) >> 16) & 0xFFFF (no check).
	FixupMovkG1
	// FixupMovkG0: MOVK — imm16 := (S + A) & 0xFFFF (no check).
	FixupMovkG0
	// FixupAbs64: plain 64-bit data word := S + A (global initializers).
	FixupAbs64
)

func (k FixupKind) String() string {
	switch k {
	case FixupCall26:
		return "call26"
	case FixupJump26:
		return "jump26"
	case FixupMovzG3:
		return "movz_uabs_g3"
	case FixupMovkG2:
		return "movk_uabs_g2"
	case FixupMovkG1:
		return "movk_uabs_g1"
	case FixupMovkG0:
		return "movk_uabs_g0"
	case FixupAbs64:
		return "abs64"
	}
	return "fixup?"
}