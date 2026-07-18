// Package x86_64 lowers verified vir modules to x86-64 (AMD64) machine code.
//
// Arrow 3 of the README taxonomy: vir.Module -> x86_64.Program, and nothing
// more. This package imports only ir/vir; it knows x86-64 instruction
// encoding and the System V AMD64 ABI, and knows nothing about ELF/Mach-O/PE.
//
// ABI: System V AMD64. First six integer/pointer arguments in RDI, RSI, RDX,
// RCX, R8, R9; further arguments on the stack in 8-byte slots; result in RAX;
// RSP 16-byte aligned before every call; AL carries the vector-register count
// for variadic calls (always 0 here — no float lowering yet). RBX, RBP,
// R12–R15 callee-saved (the spill-everything baseline touches none of them
// except RBP, the frame pointer).
//
// Code model: PIC-clean small model. Address materialization and data
// references are RIP-relative rel32 (FixupPCRel32); calls and cross-function
// jumps are rel32 (FixupPCRel32Call); 8-byte pointers in global initializers
// are absolute (FixupAbs64). object/x86_64 maps these onto the
// R_X86_64_PC32 / R_X86_64_PLT32 / R_X86_64_64 relocation shapes.
//
// Coverage: the integer/pointer subset of Vertex IR, now including native
// i64 named values. Floats, vectors, i128 named values, saturating
// arithmetic, bitrev, inline asm, byval struct passing (SysV classification
// TODO), and sub-32-bit atomic RMW are rejected with explicit errors.
package x86_64

// Program is a self-contained description of the lowered module:
// instruction bytes, symbols, and unresolved fixups — nothing else.
type Program struct {
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
// For the rel32 kinds the addend is stored both in the Fixup and written
// into the 32-bit field itself, so REL- and RELA-style consumers both work;
// FixupAbs64 fields are written as zero (x86-64 ELF is RELA-only anyway).
type Fixup struct {
	Offset uint32 // byte offset of the field within Code/Data
	Symbol string // referenced name; may be external (extern fn)
	Kind   FixupKind
	Addend int64
}

// FixupKind is x86_64's own vocabulary for what kind of hole this is.
type FixupKind int

const (
	// FixupPCRel32Call: field := S + A - P, A = -4. Emitted for call/jmp
	// rel32. Kept distinct from data refs because modern toolchains reserve
	// the branch relocation type (PLT32 shape) for exactly these sites.
	FixupPCRel32Call FixupKind = iota
	// FixupPCRel32: field := S + A - P, A = -4. Emitted for RIP-relative
	// lea/mov data and address references (PC32 shape).
	FixupPCRel32
	// FixupAbs64: field := S + A, 8-byte field. Emitted for pointer-typed
	// global initializers (R_X86_64_64 shape).
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