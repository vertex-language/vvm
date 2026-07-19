// Package x86_64 lowers verified vir modules to x86-64 (AMD64) machine code.
//
// vir.Module -> x86_64.Program, and nothing more. This package imports only
// ir/vir (plus its own abi/mcode/regalloc/syscallabi/inlineasm helpers); it
// knows x86-64 instruction encoding and the System V AMD64 ABI, and knows
// nothing about ELF/Mach-O/PE. See README.md for the ABI, code model, and
// coverage table.
package x86_64

import "github.com/vertex-language/vvm/lower/x86_64/mcode"

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

// Fixup/FixupKind are mcode's own vocabulary (the encoder is the thing that
// knows what shape of hole it left); re-exported here so downstream object
// writers only need to import the top-level package.
type Fixup = mcode.Fixup
type FixupKind = mcode.FixupKind

const (
	FixupPCRel32Call = mcode.FixupPCRel32Call
	FixupPCRel32     = mcode.FixupPCRel32
	FixupAbs64       = mcode.FixupAbs64
)