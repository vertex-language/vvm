// Package x86 lowers verified vir modules to 32-bit x86 (IA-32) machine
// code.
//
// vir.Module -> x86.Program, and nothing more. This package (plus its
// mcode/abi/regalloc/syscallabi/inlineasm helpers) knows x86 instruction
// encoding and the i386 System V cdecl ABI; it knows nothing about
// ELF/COFF/Mach-O.
//
// ABI: cdecl. Arguments on the stack in 4-byte slots, first argument at the
// lowest address, caller cleans up, result in EAX. EAX/ECX/EDX
// caller-saved; EBX/ESI/EDI/EBP callee-saved; EBP is the frame pointer.
//
// Code model: non-PIC. Globals/function addresses are 32-bit absolute
// (FixupAbs32); calls and cross-function jumps are 32-bit PC-relative
// (FixupPCRel32). An object writer maps these onto R_386_32/R_386_PC32-
// shaped relocations.
//
// Coverage: the integer/pointer subset of Vertex IR, inline assembly
// (Intel and AT&T dialects, curated mnemonic set — package inlineasm), and
// syscalls (per-target-OS trap convention — package syscallabi). Floats,
// vectors, i64/i128 named values, saturating arithmetic, and bitrev are
// still rejected with explicit errors (TODO, tier work).
package x86

import "github.com/vertex-language/vvm/lower/x86/mcode"

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

// Fixup and FixupKind are the encoder's relocation vocabulary; re-exported
// here so downstream object writers only need to import package x86.
type Fixup = mcode.Fixup
type FixupKind = mcode.FixupKind

const (
	FixupPCRel32 = mcode.FixupPCRel32
	FixupAbs32   = mcode.FixupAbs32
)