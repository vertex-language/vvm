// Package aarch64 is the static, data-only description of the AArch64
// (ARM64 / "A64") instruction set: register identity, the condition field,
// instruction-word layout, the immediate codecs, and the opcode<->mnemonic
// correspondence, with no control flow of consequence — describing the
// 64-bit ARM machine.
//
// This package covers the A64 instruction set only (the fixed-width 32-bit
// encoding executed in AArch64 state), not A32 or T32/Thumb, which are
// separate instruction sets and live in isa/arm.
//
// A64 has its own shape. Four facts drive almost everything here and are
// worth stating up front:
//
//   - There is no X31. The register field is five bits (0-31), but value 31
//     is not a general register: it denotes either the zero register (reads
//     as 0, discards writes) or the stack pointer, and which one is meant is
//     a per-operand-role fact of the instruction, not a property of the
//     value. RZR and RSP both encode to 31.
//   - Width is a per-instruction bit. Every GPR has a 64-bit (x) and a
//     32-bit (w) view of the same register, chosen by the sf bit; a w write
//     zeroes the upper half. The field is a flat five bits, with no split of
//     a low-3 field from a separate high bit.
//   - The PC is not a general register. Unlike A32's r15, the program
//     counter never appears in a register field; only ADR/ADRP and branches
//     read it. There is no RPC here.
//   - Conditionality is not universal. Unlike A32, only B.cond and the
//     conditional-select/compare families carry a condition; the codes in
//     condcodes.go are a per-instruction nibble, not a field on every
//     instruction.
package aarch64

// Reg is a physical A64 register slot. Values 0-30 are the thirty-one
// general-purpose registers X0-X30 (the value is the low five bits of the
// register field). Value 31 is the shared slot that spells as either the
// zero register or the stack pointer depending on the instruction; RZR and
// RSP name it. RNone is the "absent" sentinel for optional operands and is
// never encodable.
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
	R16 Reg = 16
	R17 Reg = 17
	R18 Reg = 18
	R19 Reg = 19
	R20 Reg = 20
	R21 Reg = 21
	R22 Reg = 22
	R23 Reg = 23
	R24 Reg = 24
	R25 Reg = 25
	R26 Reg = 26
	R27 Reg = 27
	R28 Reg = 28
	R29 Reg = 29
	R30 Reg = 30

	// Slot 31. RZR and RSP are the SAME encoding: which one an operand
	// means is decided by the instruction, not by this value. A naming or
	// encoding site that has the operand's role picks the spelling; the
	// value alone cannot tell them apart, and that is a machine fact, not a
	// modelling shortcut.
	RZR Reg = 31
	RSP Reg = 31

	RNone Reg = 0xFF
)

// Conventional/architectural aliases. Their status differs, and the
// difference is a machine fact:
//
//   - RLR (x30) is the link register. Architectural: BL/BLR write the return
//     address into x30. Outside that role it is an ordinary register.
//   - RFP (x29) is the frame pointer. Pure software convention (AAPCS64); no
//     instruction treats x29 specially.
//   - RSP (slot 31) is the stack pointer. Architectural: it is a dedicated
//     register (banked per exception level), not one of X0-X30 wearing a
//     convention the way A32's r13 is.
const (
	RFP = R29
	RLR = R30
)

// Width selects the 32-bit (W) or 64-bit (X) view of a register. The value
// is the instruction's sf bit: W32 => sf 0, W64 => sf 1.
type Width byte

const (
	W32 Width = 0 // "w" registers, 32-bit operation
	W64 Width = 1 // "x" registers, 64-bit operation
)

// SF returns the sf bit for the width (0 for W32, 1 for W64).
func (w Width) SF() byte { return byte(w) & 1 }

// Bits returns the operation width in bits (32 or 64).
func (w Width) Bits() int { return 32 << (w & 1) }

// NumGPR is the number of ordinary general-purpose registers, X0-X30. Slot
// 31 (RZR/RSP) is deliberately not counted: it is not a general register.
const NumGPR = 31

// IsGPR reports whether r names one of X0-X30 — an ordinary general
// register that may appear in a register field and is neither the zero
// register, the stack pointer, nor RNone.
func (r Reg) IsGPR() bool { return int(r) < NumGPR }

// IsSlot31 reports whether r is the shared encoding 31 (RZR/RSP).
func (r Reg) IsSlot31() bool { return r == 31 }

// Encodable reports whether r fits a 5-bit register field at all — any of
// X0-X30 or slot 31, but not RNone. Whether slot 31 is legal *as SP* or
// *as ZR* for a particular operand is an instruction fact the encoder
// checks, not something the value carries.
func (r Reg) Encodable() bool { return int(r) < 32 }

// Field returns r's 5-bit encoding — the value that goes directly into a
// register field. It is just the register number (slot 31 included); the
// method exists so call sites read as "place the field" rather than "cast
// the Reg".
func (r Reg) Field() byte { return byte(r) & 0x1F }

// Canonical name tables for the two widths. Index 31 is left out of these
// (the slot-31 spelling depends on the sp role and is handled in Name).
var (
	xName = [NumGPR]string{
		"x0", "x1", "x2", "x3", "x4", "x5", "x6", "x7",
		"x8", "x9", "x10", "x11", "x12", "x13", "x14", "x15",
		"x16", "x17", "x18", "x19", "x20", "x21", "x22", "x23",
		"x24", "x25", "x26", "x27", "x28", "x29", "x30",
	}
	wName = [NumGPR]string{
		"w0", "w1", "w2", "w3", "w4", "w5", "w6", "w7",
		"w8", "w9", "w10", "w11", "w12", "w13", "w14", "w15",
		"w16", "w17", "w18", "w19", "w20", "w21", "w22", "w23",
		"w24", "w25", "w26", "w27", "w28", "w29", "w30",
	}
)

// Name returns r's canonical assembly spelling at the given width. The sp
// argument resolves the slot-31 ambiguity: with sp true, encoding 31 spells
// as the stack pointer ("sp"/"wsp"); with sp false, as the zero register
// ("xzr"/"wzr"). Callers that lack the role (a raw dump with no operand
// context) conventionally pass sp=false, since the zero register is the far
// more common reading.
//
// RNone and any out-of-range value return "?" rather than panicking: this
// is a diagnostic path, and naming an absent register should yield "there
// isn't one," not a crash.
func (r Reg) Name(w Width, sp bool) string {
	if !r.Encodable() {
		return "?"
	}
	if r == 31 {
		switch {
		case sp && w == W64:
			return "sp"
		case sp:
			return "wsp"
		case w == W64:
			return "xzr"
		default:
			return "wzr"
		}
	}
	if w == W64 {
		return xName[r]
	}
	return wName[r]
}

// String names r as a 64-bit register with the zero-register reading of
// slot 31, for diagnostics that have no width or role in hand.
func (r Reg) String() string { return r.Name(W64, false) }

// regByName resolves canonical spellings and the documented synonyms. Built
// once in init(). The value carries no width or role; ParseReg returns
// those alongside.
type parsedReg struct {
	reg   Reg
	width Width
	sp    bool
}

var regByName map[string]parsedReg

func init() {
	regByName = make(map[string]parsedReg, 2*NumGPR+8)
	for i := 0; i < NumGPR; i++ {
		regByName[xName[i]] = parsedReg{Reg(i), W64, false}
		regByName[wName[i]] = parsedReg{Reg(i), W32, false}
	}
	// Slot 31, both readings and both widths. These are distinct spellings
	// of one encoding.
	regByName["xzr"] = parsedReg{RZR, W64, false}
	regByName["wzr"] = parsedReg{RZR, W32, false}
	regByName["sp"] = parsedReg{RSP, W64, true}
	regByName["wsp"] = parsedReg{RSP, W32, true}
	// Synonyms: call-site conveniences, not a second canonical vocabulary.
	// Name never emits these.
	regByName["lr"] = parsedReg{RLR, W64, false}
	regByName["fp"] = parsedReg{RFP, W64, false}
}

// ParseReg resolves a register spelling to its number, width, and slot-31
// role. The canonical x0-x30/w0-w30 forms, the zero-register (xzr/wzr) and
// stack-pointer (sp/wsp) spellings, and the lr/fp synonyms are accepted;
// anything else reports ok=false. Width and the sp role are part of the
// spelling in A64 — not separable the way A32's single-width ParseReg is —
// so they come back with the register.
func ParseReg(s string) (r Reg, w Width, sp bool, ok bool) {
	p, found := regByName[s]
	return p.reg, p.width, p.sp, found
}