// Package arm is the static, data-only description of the 32-bit ARM
// (A32 / "ARM state") instruction set: register identity, condition codes,
// instruction-word layout, and the opcode<->mnemonic correspondence, with
// no control flow of consequence — describing the 32-bit ARM machine.
//
// This package covers the A32 instruction set only (the fixed-width 32-bit
// "ARM" encoding), not T32/Thumb and not A64/AArch64, which are separate
// instruction sets with their own encodings. It serves both the `arm`
// (little-endian) and `armeb` (big-endian) targets: endianness is a
// property of how data words are laid out in memory, not of these tables,
// so a single description covers both.
//
// A32 has its own shape. Three facts drive almost everything here and are
// worth stating up front:
//
//   - Every instruction is conditional. Bits 31:28 of nearly every A32
//     encoding are a 4-bit condition field checked against the CPSR flags;
//     an instruction whose condition fails retires with no effect. The
//     condition codes in condcodes.go are therefore not a per-opcode nibble
//     but a field on the whole instruction set.
//   - Register fields are a flat four bits. All sixteen GPRs are named by a
//     contiguous 4-bit field wherever a register appears; there is no
//     split of a low-3 field from a separate high bit.
//   - The PC is a general register. r15 *is* the program counter and may
//     appear directly in most register fields. Reading it yields the
//     current instruction's address plus 8 (a pipeline artifact), and
//     writing it branches.
package arm

// Reg is a physical A32 general-purpose register. Values 0-15 are the
// sixteen GPRs in encoding order (the value *is* the 4-bit register field).
// RNone is the "absent" sentinel for optional operands and is never
// encodable.
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
	R11 Reg = 11
	R12 Reg = 12
	R13 Reg = 13
	R14 Reg = 14
	R15 Reg = 15

	RNone Reg = 0xFF
)

// Architectural/conventional aliases for the top three registers. Their
// status differs, and the difference is a machine fact, not a naming
// preference:
//
//   - RPC (r15) is the program counter. This is architectural: the register
//     number genuinely denotes the PC, hardware reads it as instr+8 and a
//     write to it is a branch.
//   - RLR (r14) is the link register. Also partly architectural: BL/BLX
//     write the return address into r14, and it is banked per exception
//     mode. Outside that role it is an ordinary register.
//   - RSP (r13) is the stack pointer. This is pure software convention in
//     A32 (the architecture only special-cases r13 by banking it per mode);
//     no instruction treats r13 as a stack pointer in any special way at
//     the hardware level.
const (
	RSP = R13
	RLR = R14
	RPC = R15
)

// NumGPR is the number of encodable general-purpose registers. There is
// nothing to "reach": all sixteen are named by the same 4-bit field, with
// no prefix bit involved.
const NumGPR = 16

// IsGPR reports whether r names one of the sixteen encodable GPRs — i.e.
// whether r may legally appear in a register field. RNone, and any other
// out-of-range value, reports false.
func (r Reg) IsGPR() bool { return int(r) < NumGPR }

// Field returns r's 4-bit encoding — the value that goes directly into a
// register field. It is just the register number; the method exists so
// call sites read as "place the field" rather than "cast the Reg".
func (r Reg) Field() byte { return byte(r) & 0xF }

// regName is the canonical name table. r13/r14/r15 spell as r13/r14/r15
// here so a disassembler that round-trips through ParseReg/Name gets back
// what it started with; sp/lr/pc are documented synonyms accepted by
// ParseReg but not emitted (the same choice condcodes.go makes for the
// condition synonyms).
var regName = [NumGPR]string{
	"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
}

// Name returns r's canonical assembly spelling ("r0".."r15"). A32 GPRs are
// a single 32-bit width, so there is no width argument. Non-GPR values —
// RNone above all — return "?" rather than panicking: this is a
// diagnostic path, and a caller asking to name an absent register should
// get "there isn't one," not a crash.
func (r Reg) Name() string {
	if !r.IsGPR() {
		return "?"
	}
	return regName[r]
}

// String is Name, for diagnostics.
func (r Reg) String() string { return r.Name() }

// regByName resolves canonical spellings and the sp/lr/pc synonyms. Built
// once in init().
var regByName map[string]Reg

func init() {
	regByName = make(map[string]Reg, NumGPR+3)
	for i, n := range regName {
		regByName[n] = Reg(i)
	}
	// Synonyms: conveniences at call sites, not a second canonical
	// vocabulary. Name never emits these.
	regByName["sp"] = RSP
	regByName["lr"] = RLR
	regByName["pc"] = RPC
}

// ParseReg resolves a register spelling to its number. The canonical
// "rN" forms and the sp/lr/pc synonyms are accepted; anything else reports
// false.
func ParseReg(s string) (Reg, bool) {
	r, ok := regByName[s]
	return r, ok
}