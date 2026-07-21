package aarch64

import "fmt"

// Sf returns the "sf" (size flag) bit for a 32-bit (sz==4) or 64-bit
// (sz==8) data-processing form, positioned at its fixed bit 31.
func Sf(sz int) uint32 {
	if sz == 8 {
		return 1 << 31
	}
	return 0
}

// Idx64 selects which half of a [W-form, X-form] opcode-table entry (as
// used by DPImmOpcodes, DPRegOpcodes, DP1Opcodes) applies for sz.
func Idx64(sz int) int {
	if sz == 8 {
		return 1
	}
	return 0
}

// SizeBits packs a load/store-exclusive/ordered access size (1/2/4/8
// bytes) into its fixed 2-bit "size" field at bits [31:30].
func SizeBits(sz int) (uint32, error) {
	switch sz {
	case 1:
		return 0 << 30, nil
	case 2:
		return 1 << 30, nil
	case 4:
		return 2 << 30, nil
	case 8:
		return 3 << 30, nil
	}
	return 0, fmt.Errorf("isa/aarch64: bad load/store-exclusive size %d", sz)
}

// Bitfield-move (BFM) family base words — [W-form, X-form] for the
// unsigned/signed variants UBFM/SBFM. UXTB/UXTH/SXTB/SXTH/SXTW and the
// LSR/LSL/ASR-by-immediate pseudo-ops are all specific (immr, imms)
// encodings of one of these two instructions.
const (
	OpUBFMW uint32 = 0x53000000
	OpUBFMX uint32 = 0xD3400000
	OpSBFMW uint32 = 0x13000000
	OpSBFMX uint32 = 0x93400000
)

// PackBFM lays out one UBFM/SBFM-family instruction word: base carries
// the fixed opcode bits, immr/imms are the two 6-bit shift/width fields
// (bits [21:16] and [15:10]), and rn/rd are the usual 5-bit register
// fields. Picking which (immr, imms) pair a given pseudo-op needs is the
// caller's job — it depends on the pseudo-op and the operand width, which
// varies by lowering pipeline, not by anything true of the bit layout
// itself.
func PackBFM(base uint32, immr, imms uint32, rn, rd Reg) uint32 {
	return base | (immr&0x3F)<<16 | (imms&0x3F)<<10 | uint32(rn)<<5 | uint32(rd)
}

// PackPair lays out one STP(pre-index)/LDP(post-index) 64-bit pair
// instruction word: base carries the fixed opcode bits, disp7 is the
// pre-scaled displacement (actual byte displacement / 8), and rt/rt2/rn
// are the usual 5-bit register fields.
func PackPair(base uint32, rt, rt2, rn Reg, disp7 int32) uint32 {
	return base | (uint32(disp7)&0x7F)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt)
}