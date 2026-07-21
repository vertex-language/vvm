// Package arm is the static, data-only description of the A32 (32-bit
// ARM) instruction set that both the generic encoder (isa/arm/encoder)
// and any future disassembler/inline-asm parser build on: register
// identity, condition codes, rotated-immediate and shift bit encodings,
// and the opcode<->mnemonic correspondence for data-processing ops.
//
// Nothing here decodes or encodes an instruction stream — there is no
// control flow of consequence, only declarations (plus mechanical
// reverse-index maps built once in init()). See the package README for
// the test used to decide what belongs here vs. in lower/arm.
package arm

// Reg is a physical A32 general-purpose register (r0-r15).
type Reg byte

const (
	R0  Reg = 0
	R1  Reg = 1
	R2  Reg = 2
	R3  Reg = 3
	R4  Reg = 4
	R5  Reg = 5
	R6  Reg = 6
	R7  Reg = 7
	R8  Reg = 8
	R9  Reg = 9
	R10 Reg = 10
	RFP Reg = 11 // frame pointer (AAPCS convention, universal assembler spelling)
	RIP Reg = 12 // intra-procedure-call scratch register
	RSP Reg = 13 // stack pointer
	RLR Reg = 14 // link register
	RPC Reg = 15 // program counter

	RNone Reg = 0xFF
)

// regName is the canonical assembler spelling for each encoding 0-15.
// Indices 11-15 use their AAPCS/assembler names (fp/ip/sp/lr/pc) rather
// than r11-r15 — the same convention every A32 assembler and disassembler
// uses, the same way isa/x86's reg32 table spells encoding 4 "esp" rather
// than "r4".
var regName = [16]string{
	"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"r8", "r9", "r10", "fp", "ip", "sp", "lr", "pc",
}

// String is r's assembler spelling, for diagnostics.
func (r Reg) String() string {
	if int(r) < len(regName) {
		return regName[r]
	}
	return "?"
}