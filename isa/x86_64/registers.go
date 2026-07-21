// Package x86_64 holds static, data-only facts about the x86-64 (AMD64)
// instruction set: register identity, condition-code numbering,
// REX/ModRM/SIB bit layout, and the opcode<->mnemonic correspondence.
// (in isa/x86_64/encoder) a generic assembler that turns an instruction
// stream built from those facts into machine bytes.
//
// Nothing under isa/x86_64 knows what a vir.Module is, what OS it's
// targeting, or how this compiler allocates registers or lays out a
// stack frame. See README.md for the full rationale and the test for
// what belongs here.
package x86_64

// Reg identifies one of the sixteen general-purpose registers by its
// 4-bit encoding: bit 3 (folded into REX.R, REX.X, or REX.B depending on
// which ModRM/SIB field the register sits in) selects r8-r15; bits 0-2
// are the raw ModRM/SIB field value.
type Reg byte

const (
	RAX Reg = 0
	RCX Reg = 1
	RDX Reg = 2
	RBX Reg = 3
	RSP Reg = 4
	RBP Reg = 5
	RSI Reg = 6
	RDI Reg = 7
	R8  Reg = 8
	R9  Reg = 9
	R10 Reg = 10
	R11 Reg = 11
	R12 Reg = 12
	R13 Reg = 13
	R14 Reg = 14
	R15 Reg = 15

	RNone Reg = 0xFF
)

// Name64/Name32/Name16/Name8 are the canonical Intel-syntax register
// names at each operand width, indexed by Reg.
//
// The 8-bit row always uses the REX-requiring spl/bpl/sil/dil spellings
// for RSP/RBP/RSI/RDI rather than the legacy REX-free ah/ch/dh/bh forms:
// this table has no representation of the high-byte registers at all,
// matching the fact that nothing in this compiler's encoder ever omits
// REX when addressing RSP/RBP/RSI/RDI at 8-bit width.
var Name64 = [16]string{"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"}
var Name32 = [16]string{"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"}
var Name16 = [16]string{"ax", "cx", "dx", "bx", "sp", "bp", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"}
var Name8 = [16]string{"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"}

// NameForWidth returns r's canonical name at the given operand width in
// bytes (1, 2, 4, or 8), defaulting to the 64-bit spelling for any other
// value.
func NameForWidth(r Reg, width int) string {
	switch width {
	case 1:
		return Name8[r]
	case 2:
		return Name16[r]
	case 4:
		return Name32[r]
	default:
		return Name64[r]
	}
}