// Package aarch64 holds static, data-only facts about the A64 (64-bit ARM)
// instruction set: register identity, condition-code numbering, and the
// bit-layout/opcode<->mnemonic tables a generic assembler needs to turn an
// instruction stream into machine words. The generic assembler itself
// lives in isa/aarch64/encoder.
//
// Nothing here knows what a vir.Module is, what OS it's targeting, how a
// compiler allocates registers, or how a stack frame is shaped. Same test
// as isa/x86, isa/x86_64, and isa/arm: would this still be true on bare
// metal, compiling a language that isn't vir, with a different register
// allocator? If yes, it belongs here. If the answer changes based on this
// compiler's own choices — which registers get saved, who cleans the
// stack, how a not-yet-placed value is represented — it's a lowering
// decision and belongs in lower/aarch64.
package aarch64

// Reg is a physical A64 general-purpose register encoding, 0-31.
//
// Encoding 31 is architecturally context-dependent: in a stack-pointer
// context (a load/store base register, the extended-register ADD/SUB used
// for SP arithmetic) it names SP; in an ordinary register-operand context
// it names the zero register (XZR/WZR — reads as zero, writes discarded).
// Both roles share one encoding, so SP and ZR below are deliberately the
// same value. Which meaning applies is determined by which field of which
// instruction carries it, not by anything this package decides.
type Reg byte

const (
	X0  Reg = 0
	X1  Reg = 1
	X2  Reg = 2
	X3  Reg = 3
	X4  Reg = 4
	X5  Reg = 5
	X6  Reg = 6
	X7  Reg = 7
	X8  Reg = 8
	X9  Reg = 9
	X10 Reg = 10
	X11 Reg = 11
	X12 Reg = 12
	X13 Reg = 13
	X14 Reg = 14
	X15 Reg = 15
	X16 Reg = 16
	X17 Reg = 17
	X18 Reg = 18
	X19 Reg = 19
	X20 Reg = 20
	X21 Reg = 21
	X22 Reg = 22
	X23 Reg = 23
	X24 Reg = 24
	X25 Reg = 25
	X26 Reg = 26
	X27 Reg = 27
	X28 Reg = 28
	X29 Reg = 29
	X30 Reg = 30

	SP Reg = 31 // stack-pointer context
	ZR Reg = 31 // zero-register context (XZR/WZR)
)

// FP and LR are the AAPCS64 names for X29 and X30. LR is more than a
// naming convention: BL/BLR write it implicitly and a bare RET reads it
// implicitly, so "X30 is the link register" is true of the architecture
// itself, not just this compiler's frame layout. FP's role as a frame
// pointer is the calling convention's choice, not the hardware's, but the
// alias is what any AAPCS64-aware disassembler would print, so it lives
// here rather than being re-declared per lowering pipeline.
const (
	FP Reg = X29
	LR Reg = X30
)

// IP0, IP1, and PR are the AAPCS64 names for X16, X17, and X18: the two
// intra-procedure-call scratch registers a linker/PLT stub is permitted to
// clobber, and the platform register a target OS may reserve. Naming
// facts, not policy — whether *this* compiler's lowering pipeline actually
// uses them as scratch registers is a lower/aarch64 decision.
const (
	IP0 Reg = X16
	IP1 Reg = X17
	PR  Reg = X18
)

// XNames/WNames are the 64-bit (Xn) and 32-bit (Wn) register mnemonics,
// indexed by Reg for encodings 0-30. Encoding 31's spelling depends on
// which of SP/ZR applies at the call site, so it's handled by XName/WName
// below rather than folded into these arrays.
var (
	XNames = [31]string{
		"x0", "x1", "x2", "x3", "x4", "x5", "x6", "x7",
		"x8", "x9", "x10", "x11", "x12", "x13", "x14", "x15",
		"x16", "x17", "x18", "x19", "x20", "x21", "x22", "x23",
		"x24", "x25", "x26", "x27", "x28", "x29", "x30",
	}
	WNames = [31]string{
		"w0", "w1", "w2", "w3", "w4", "w5", "w6", "w7",
		"w8", "w9", "w10", "w11", "w12", "w13", "w14", "w15",
		"w16", "w17", "w18", "w19", "w20", "w21", "w22", "w23",
		"w24", "w25", "w26", "w27", "w28", "w29", "w30",
	}
)

// XName/WName return the Xn/Wn mnemonic for r, given which encoding-31
// meaning applies at the call site (isSP picks "sp"/"wsp" over "xzr"/"wzr").
func XName(r Reg, isSP bool) string {
	if r == 31 {
		if isSP {
			return "sp"
		}
		return "xzr"
	}
	return XNames[r]
}

func WName(r Reg, isSP bool) string {
	if r == 31 {
		if isSP {
			return "wsp"
		}
		return "wzr"
	}
	return WNames[r]
}