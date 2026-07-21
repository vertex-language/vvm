package arm

import "math/bits"

// PackImm12 encodes v as an A32 rotated-immediate Operand2 — an 8-bit
// value rotated right by an even number of bits (0-30) — the form every
// data-processing instruction's immediate operand accepts. ok is false if
// no rotation makes v fit in 8 bits.
func PackImm12(v uint32) (imm12 uint32, ok bool) {
	for r := uint(0); r < 32; r += 2 {
		if x := bits.RotateLeft32(v, int(r)); x <= 0xFF {
			return (uint32(r)/2)<<8 | x, true
		}
	}
	return 0, false
}

// UnpackImm12 reverses PackImm12: recovers the 32-bit value a rotated
// immediate field encodes.
func UnpackImm12(imm12 uint32) uint32 {
	rot := (imm12 >> 8) & 0xF
	val := imm12 & 0xFF
	return bits.RotateLeft32(val, -int(rot*2))
}

// SplitImm16 splits a 16-bit value into the imm4:imm12 halves that
// MOVW/MOVT pack into bits 19:16 and 11:0 of their own instruction word.
// Materializing a full 32-bit immediate takes two calls: one for the low
// 16 bits (MOVW) and one for the high 16 bits (MOVT).
func SplitImm16(v uint32) (imm4, imm12 uint32) {
	return (v >> 12) & 0xF, v & 0xFFF
}

// PCBias is how far ahead of the currently-executing instruction A32's PC
// reads (two instructions = 8 bytes) — the bias every PC-relative field
// (branch offsets, symbol-relative fixups) measures from.
const PCBias = 8