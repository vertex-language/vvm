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

// Reg is a physical IA-32 general-purpose register. Values 0-7 are the
// eight GPRs in ModRM/SIB encoding order; RNone is the "absent" sentinel
// for optional base/index operands and is never encodable.
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

// NumGPR is the number of encodable general-purpose registers in 32-bit
// mode. Reaching r8-r15 requires a REX prefix, which this backend never
// emits.
const NumGPR = 8

// IsGPR reports whether r names one of the eight encodable GPRs — i.e.
// whether r may legally appear in a ModRM reg/rm or SIB base/index field.
// RNone, and any other out-of-range value, reports false.
func (r Reg) IsGPR() bool { return int(r) < NumGPR }

// ByteAddressable reports whether r has a low-byte spelling reachable
// without a REX prefix — AL/CL/DL/BL only. Any emitter of a byte-operand
// form (setcc, mov r/m8, a movzx source) has to check this, because
// indices 4-7 name AH/CH/DH/BH rather than the low byte of
// ESP/EBP/ESI/EDI: encoding "the low byte of esi" is not representable in
// 32-bit mode at all, so it can't be silently substituted.
func (r Reg) ByteAddressable() bool { return r <= REBX }

// reg32/reg16/reg8 are the three width-indexed name tables for the eight
// GPR encodings. Byte-register naming is the irregular case described on
// ByteAddressable.
var reg32 = [NumGPR]string{"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi"}
var reg16 = [NumGPR]string{"ax", "cx", "dx", "bx", "sp", "bp", "si", "di"}
var reg8 = [NumGPR]string{"al", "cl", "dl", "bl", "ah", "ch", "dh", "bh"}

// Name returns r's assembly spelling at the given operand width in bits
// (8, 16, or 32; anything else defaults to 32).
//
// Non-GPR values — RNone above all — return "?" rather than panicking.
// This is a diagnostic path, and a printer asked to name the base
// register of an absolute [disp32] operand should say "there isn't one",
// not take the process down.
func (r Reg) Name(widthBits int) string {
	if !r.IsGPR() {
		return "?"
	}
	switch widthBits {
	case 8:
		return reg8[r]
	case 16:
		return reg16[r]
	}
	return reg32[r]
}

// String is the width-free 32-bit spelling, for diagnostics that don't
// care about operand width.
func (r Reg) String() string { return r.Name(32) }