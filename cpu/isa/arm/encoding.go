package arm

import "math/bits"

// InstrBytes is the width of every A32 instruction: four bytes, fixed.
// There is no variable-length encoding to measure the way x86 needs, and
// no operand-size prefix — width is carried in the opcode/fields instead.
const InstrBytes = 4

// CondShift is the bit position of the 4-bit condition field. It occupies
// bits 31:28 of nearly every A32 encoding; the exceptions are the 0b1111
// unconditional-instruction group (see CondNV), which use the same field
// position for a different purpose.
const CondShift = 28

// SetCond places condition code cc into the top nibble of an otherwise-
// built instruction word, and Cond reads it back. These are the only field
// accessors that are universal across the instruction set — register and
// operand field positions vary by format (multiply, for instance, swaps
// the roles of the two upper register nibbles), so there is no single
// register-field packer here the way isa/x86_64 has PackModRM.
func SetCond(word uint32, cc byte) uint32 { return word&0x0FFFFFFF | uint32(cc&0xF)<<CondShift }
func Cond(word uint32) byte                { return byte(word >> CondShift & 0xF) }

// ---------------------------------------------------------------------------
// Modified immediates ("Operand2" immediate form).
// ---------------------------------------------------------------------------
//
// A data-processing immediate is not an arbitrary 32-bit constant. It is an
// 8-bit value rotated right by an even amount: a 4-bit rotate field r gives
// a rotate of 2*r, so the representable constants are exactly {ror(imm8,
// 2*r) : imm8 in 0..255, r in 0..15}. This is the A32 analog of x86's
// FitsImm8/FitsImm32 — the question "can the machine carry this constant
// inline?" — but the representable set is scattered (all byte values, plus
// those bytes rotated to any even bit position) rather than a contiguous
// range. A constant that fails EncodeModImm must be built some other way
// (a MOV/MOVT pair on ARMv6T2+, or a literal-pool load), which is a lowering
// decision one layer up, not a fact here.

// EncodeModImm finds a (rotate, imm8) encoding of v, if one exists. It
// returns the smallest rotate that works, so a value that already fits in
// eight bits encodes with rotate 0. The encoding is not unique in general
// (e.g. small values have several rotations), and callers that care about a
// canonical form should rely on this smallest-rotate choice.
func EncodeModImm(v uint32) (rotate, imm8 byte, ok bool) {
	for r := 0; r < 16; r++ {
		// v == ror(imm8, 2*r)  <=>  rol(v, 2*r) == imm8 (zero-extended).
		cand := bits.RotateLeft32(v, 2*r)
		if cand <= 0xFF {
			return byte(r), byte(cand), true
		}
	}
	return 0, 0, false
}

// DecodeModImm inverts EncodeModImm: it applies the machine's rule to a
// (rotate, imm8) pair. Total — every pair names some value.
//
// math/bits has no RotateRight32; rotating right by n is rotating left by
// -n, which RotateLeft32 handles directly (its shift argument may be
// negative).
func DecodeModImm(rotate, imm8 byte) uint32 {
	return bits.RotateLeft32(uint32(imm8), -2*int(rotate&0xF))
}

// FitsModImm reports whether v is representable as a modified immediate at
// all, without returning the encoding.
func FitsModImm(v uint32) bool {
	_, _, ok := EncodeModImm(v)
	return ok
}

// ---------------------------------------------------------------------------
// Branch offset (B, BL).
// ---------------------------------------------------------------------------
//
// A branch carries a signed 24-bit word offset in its low 24 bits. The
// processor shifts it left two (branches are word-aligned) and adds it to
// the PC, which — because the PC reads as the branch's own address plus 8
// (pipeline prefetch) — makes the reachable range the instruction address
// +/- 32MB. The +8 is an architectural read value, but *applying* it to
// turn a target address into a field is encoder work; the pure facts here
// are the field width, its signedness, and the word (x4) scaling.

// BranchImm24Bits is the width of the signed branch word-offset field.
const BranchImm24Bits = 24

// FitsBranchImm24 reports whether a signed word offset fits the 24-bit
// field (-2^23 .. 2^23-1 words, i.e. -32MB .. +32MB-4 bytes after the x4
// scaling).
func FitsBranchImm24(wordOffset int32) bool {
	return wordOffset >= -(1<<23) && wordOffset <= 1<<23-1
}

// EncodeBranchImm24 masks a signed word offset into the 24-bit field.
// Callers should check FitsBranchImm24 first.
func EncodeBranchImm24(wordOffset int32) uint32 { return uint32(wordOffset) & 0x00FFFFFF }

// DecodeBranchImm24 sign-extends a 24-bit field back to a word offset.
func DecodeBranchImm24(field uint32) int32 {
	v := field & 0x00FFFFFF
	if v&0x00800000 != 0 {
		v |= 0xFF000000 // sign-extend bit 23
	}
	return int32(v)
}