// Package x86 lowers verified vir modules to 32-bit x86 (IA-32) machine code.
//
// Arrow 3 of the README taxonomy: vir.Module -> x86.Program, and nothing
// more. This package imports only ir/vir; it knows x86 instruction encoding
// and the i386 System V cdecl ABI, and knows nothing about ELF/COFF/Mach-O.
//
// ABI: cdecl. Arguments on the stack in 4-byte slots, first argument at the
// lowest address, caller cleans up, result in EAX. EAX/ECX/EDX caller-saved;
// EBX/ESI/EDI/EBP callee-saved; EBP is the frame pointer.
//
// Code model: non-PIC. Globals/function addresses are 32-bit absolute
// (FixupAbs32); calls and cross-function jumps are 32-bit PC-relative
// (FixupPCRel32). object/x86 maps these onto R_386_32 / R_386_PC32-shaped
// relocations.
//
// Coverage: the integer/pointer subset of Vertex IR. Floats, vectors,
// i64/i128 named values, saturating arithmetic, bitrev, inline asm, and
// sub-32-bit atomic RMW are rejected with explicit errors (TODO, tier work).
package x86

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
// The addend is stored both in the Fixup and written into the 32-bit field
// itself, so REL-style (implicit-addend, i386 ELF) and RELA-style consumers
// both work without rewriting bytes.
type Fixup struct {
	Offset uint32 // byte offset of the 32-bit field within Code/Data
	Symbol string // referenced name; may be external (extern fn)
	Kind   FixupKind
	Addend int64
}

// FixupKind is x86's own vocabulary for what kind of hole this is.
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