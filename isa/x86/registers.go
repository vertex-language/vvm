// Package x86 is the static, data-only description of the IA-32 (x86)
// instruction set that both the generic encoder (isa/x86/encoder) and the
// debug disassembler (format/asm/x86/text) build on: register identity,
// condition codes, ModRM/SIB bit layout, and the opcode<->mnemonic
// correspondence.
//
// Nothing here decodes or encodes an instruction stream — there is no
// control flow of consequence, only declarations (plus mechanical
// reverse-index maps built once in init()). See the package README for the
// test used to decide what belongs here vs. in lower/x86 or the printer.
package x86

// Reg is a physical IA-32 general-purpose register.
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

// reg32/reg16/reg8 are the three width-indexed name tables for the eight
// GPR encodings (0-7). Byte-register naming is the one irregular case:
// indices 4-7 name AH/CH/DH/BH rather than a low byte of ESP/EBP/ESI/EDI.
// Reaching SPL/BPL/SIL/DIL instead requires a REX prefix, which this
// 32-bit-only backend never emits, so that distinction doesn't need
// representing here.
var reg32 = [8]string{"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi"}
var reg16 = [8]string{"ax", "cx", "dx", "bx", "sp", "bp", "si", "di"}
var reg8 = [8]string{"al", "cl", "dl", "bl", "ah", "ch", "dh", "bh"}

// Name returns r's assembly spelling at the given operand width in bits
// (8, 16, or 32; anything else defaults to 32).
func (r Reg) Name(widthBits int) string {
	switch widthBits {
	case 8:
		return reg8[r]
	case 16:
		return reg16[r]
	}
	return reg32[r]
}

// String is the 32-bit spelling, for diagnostics that don't care about
// operand width (e.g. naming which physical register an inline-asm
// binding involved).
func (r Reg) String() string {
	if int(r) < len(reg32) {
		return reg32[r]
	}
	return "?"
}