package aarch64

import "math/bits"

// InstrBytes is the width of every A64 instruction: four bytes, fixed.
// There is no variable-length encoding to measure the way x86 needs, and no
// operand-size prefix — width is the sf bit in the opcode.
const InstrBytes = 4

// ---------------------------------------------------------------------------
// Register field positions.
// ---------------------------------------------------------------------------
//
// A64 places register operands at fixed positions across almost all
// formats: Rd/Rt in bits 4:0, Rn in bits 9:5, Rm in bits 20:16, Ra in bits
// 14:10. A few formats reassign these (the multiply-accumulate forms use Ra;
// LDP/STP add Rt2), but the common positions are facts worth naming so call
// sites read as "place Rn" rather than shifting a literal. Unlike
// isa/x86_64's PackModRM there is no single packer — each field is placed
// independently.
const (
	RdShift = 0
	RtShift = 0
	RnShift = 5
	RmShift = 16
	RaShift = 10
)

// PlaceRd/Rn/Rm/Ra/Rt OR a register's 5-bit field into a word at its
// standard position. They mask to five bits; the caller is responsible for
// the operand being encodable (Reg.Encodable) and for slot-31's role.
func PlaceRd(w uint32, r Reg) uint32 { return w | uint32(r.Field())<<RdShift }
func PlaceRt(w uint32, r Reg) uint32 { return w | uint32(r.Field())<<RtShift }
func PlaceRn(w uint32, r Reg) uint32 { return w | uint32(r.Field())<<RnShift }
func PlaceRm(w uint32, r Reg) uint32 { return w | uint32(r.Field())<<RmShift }
func PlaceRa(w uint32, r Reg) uint32 { return w | uint32(r.Field())<<RaShift }

// ---------------------------------------------------------------------------
// Move-wide immediate (MOVZ / MOVN / MOVK).
// ---------------------------------------------------------------------------
//
// A move-wide instruction carries a 16-bit immediate placed at one of four
// halfword positions within the register, selected by the 2-bit hw field:
// the shift is hw*16. For a 32-bit (sf 0) operation only hw 0 and 1 are
// legal (positions 0 and 16); hw 2/3 are UNDEFINED there. imm16 sits in bits
// 20:5, hw in bits 22:21.

// MoveWideHW returns the hw field for a left-shift amount that must be one
// of 0/16/32/48. ok is false for any other shift, and for shifts 32/48 when
// the operation is 32-bit (W32).
func MoveWideHW(shift int, w Width) (hw byte, ok bool) {
	if shift%16 != 0 {
		return 0, false
	}
	h := shift / 16
	if h < 0 || h > 3 {
		return 0, false
	}
	if w == W32 && h > 1 {
		return 0, false
	}
	return byte(h), true
}

// MoveWideShift inverts MoveWideHW: the left-shift amount a hw field names.
func MoveWideShift(hw byte) int { return int(hw&3) * 16 }

// ---------------------------------------------------------------------------
// Arithmetic immediate (ADD/SUB/CMP immediate forms).
// ---------------------------------------------------------------------------
//
// An add/sub immediate is a 12-bit unsigned value (imm12, bits 21:10)
// optionally shifted left 12 by the sh bit (bit 22): sh 0 => LSL #0, sh 1 =>
// LSL #12. So the representable magnitudes are 0..4095 and (0..4095)<<12.
// This is the analog of A32's modified immediate and x86's FitsImm — "can
// the machine carry this constant inline?" — with a small, structured set.
// A constant that fits neither form must be built with move-wide or a
// literal load, which is a lowering decision one layer up.

// EncodeAddSubImm finds a (sh, imm12) encoding of an unsigned value, if one
// exists. It prefers the unshifted form: a value under 4096 encodes with
// sh 0.
func EncodeAddSubImm(v uint64) (sh, imm12 byte12, ok bool) { //nolint:revive // see below
	return encodeAddSubImm(v)
}

// byte12 is a 12-bit immediate carried in a uint16. Named so the return
// signature reads truthfully (a plain byte cannot hold imm12).
type byte12 = uint16

func encodeAddSubImm(v uint64) (sh byte, imm12 byte12, ok bool) {
	if v <= 0xFFF {
		return 0, byte12(v), true
	}
	if v&0xFFF == 0 && (v>>12) <= 0xFFF {
		return 1, byte12(v >> 12), true
	}
	return 0, 0, false
}

// FitsAddSubImm reports whether v is representable as an add/sub immediate
// without returning the encoding.
func FitsAddSubImm(v uint64) bool {
	_, _, ok := encodeAddSubImm(v)
	return ok
}

// DecodeAddSubImm inverts the codec: it applies sh to imm12. Total.
func DecodeAddSubImm(sh byte, imm12 byte12) uint64 {
	x := uint64(imm12 & 0xFFF)
	if sh&1 == 1 {
		x <<= 12
	}
	return x
}

// ---------------------------------------------------------------------------
// Bitmask immediate (logical AND/ORR/EOR/ANDS immediate forms).
// ---------------------------------------------------------------------------
//
// A logical immediate is not an arbitrary constant. It is a single run of
// ones within an element of 2, 4, 8, 16, 32 or 64 bits, that element
// replicated across the operand width — or, equivalently, described by the
// (N, immr, imms) triple: immr rotates the run, imms encodes both the
// element size and the run length. All-zeros and all-ones are NOT
// representable (they would be the degenerate cases the encoding reserves).
//
// This is the A64 analog of A32's rotated modified immediate: the
// representable set is scattered, not a contiguous range, and a constant
// that fails EncodeBitmaskImm must be built some other way — a lowering
// decision, not a fact here.
//
// EncodeBitmaskImm implements the standard element-size / shifted-mask
// derivation (the inverse of the architecture's DecodeBitMasks). datasize
// must be 32 or 64; a 32-bit immediate is treated as its own 32-bit pattern
// replicated into 64 bits before analysis, which is how the machine reads
// it.
func EncodeBitmaskImm(imm uint64, datasize int) (n, immr, imms byte, ok bool) {
	switch datasize {
	case 32:
		imm = uint64(uint32(imm))
		imm |= imm << 32
	case 64:
	default:
		return 0, 0, 0, false
	}
	if imm == 0 || imm == ^uint64(0) {
		return 0, 0, 0, false
	}

	// Smallest element size in {2,4,...,64} for which imm is periodic.
	size := 64
	for {
		size >>= 1
		mask := (uint64(1) << uint(size)) - 1
		if imm&mask != (imm>>uint(size))&mask {
			size <<= 1
			break
		}
		if size <= 2 {
			break
		}
	}

	var mask uint64
	if size == 64 {
		mask = ^uint64(0)
	} else {
		mask = (uint64(1) << uint(size)) - 1
	}
	imm &= mask

	var i, cto int
	if isShiftedMask(imm) {
		i = bits.TrailingZeros64(imm)
		cto = bits.TrailingZeros64(^(imm >> uint(i)))
	} else {
		imm |= ^mask
		if !isShiftedMask(^imm) {
			return 0, 0, 0, false
		}
		clo := bits.LeadingZeros64(^imm)
		i = 64 - clo
		cto = clo + bits.TrailingZeros64(^imm) - (64 - size)
	}

	immrV := (size - i) & (size - 1)
	nimms := (^uint(size-1) << 1) | uint(cto-1)
	nBit := ((nimms >> 6) & 1) ^ 1

	return byte(nBit), byte(uint(immrV) & 0x3F), byte(nimms & 0x3F), true
}

// FitsBitmaskImm reports whether imm is representable as a logical immediate
// at the given operand width, without returning the encoding.
func FitsBitmaskImm(imm uint64, datasize int) bool {
	_, _, _, ok := EncodeBitmaskImm(imm, datasize)
	return ok
}

// DecodeBitmaskImm inverts EncodeBitmaskImm: it applies the architecture's
// DecodeBitMasks rule to an (N, immr, imms) triple at the given operand
// width. Unlike the total decoders above, it reports ok=false for the
// genuinely-reserved encodings (element size too large for the width, or the
// all-ones run length that logical immediates forbid) rather than inventing
// a value. It never panics.
func DecodeBitmaskImm(n, immr, imms byte, datasize int) (uint64, bool) {
	if datasize != 32 && datasize != 64 {
		return 0, false
	}
	// len = position of the highest set bit of (N : NOT(imms)), 7 bits wide.
	combined := (uint(n&1) << 6) | (uint(^imms) & 0x3F)
	length := bits.Len(combined) - 1
	if length < 1 {
		return 0, false
	}
	esize := 1 << uint(length)
	if esize > datasize {
		return 0, false
	}
	levels := uint(esize - 1)
	s := uint(imms) & levels
	r := uint(immr) & levels
	if s == levels {
		return 0, false // all-ones run: reserved for logical immediates
	}

	// welem = Ones(s+1) right-aligned within esize, rotated right by r.
	welem := (uint64(1) << (s + 1)) - 1
	elem := rorInElem(welem, r, esize)

	// Replicate the esize-bit element across 64 bits, then narrow to width.
	out := replicate(elem, esize)
	if datasize == 32 {
		out = uint64(uint32(out))
	}
	return out, true
}

// ---------------------------------------------------------------------------
// Branch word-offset codecs.
// ---------------------------------------------------------------------------
//
// Every A64 branch stores a signed word offset (the byte offset is always a
// multiple of four, so the low two bits are not encoded) added to the
// address of the branch itself — there is no A32-style PC-read skew to
// account for. Three field widths exist, and their reach is exactly the
// field width times four bytes:
//
//   - imm26 (B, BL):            +/-128MB, low bits 25:0.
//   - imm19 (B.cond, CBZ, CBNZ): +/-1MB,  bits 23:5.
//   - imm14 (TBZ, TBNZ):        +/-32KB,  bits 18:5.
//
// The pure facts here are the field widths, their signedness, and the word
// (x4) scaling. *Applying* an offset to a resolved target is encoder work.

const (
	BranchImm26Bits = 26
	BranchImm19Bits = 19
	BranchImm14Bits = 14
)

// FitsBranchImm26 reports whether a signed word offset fits the 26-bit field.
func FitsBranchImm26(word int64) bool { return word >= -(1<<25) && word <= 1<<25-1 }

// FitsBranchImm19 reports whether a signed word offset fits the 19-bit field.
func FitsBranchImm19(word int64) bool { return word >= -(1<<18) && word <= 1<<18-1 }

// FitsBranchImm14 reports whether a signed word offset fits the 14-bit field.
func FitsBranchImm14(word int64) bool { return word >= -(1<<13) && word <= 1<<13-1 }

// EncodeBranchImm26 masks a signed word offset into the 26-bit field (bits
// 25:0). Callers should check FitsBranchImm26 first.
func EncodeBranchImm26(word int64) uint32 { return uint32(word) & 0x03FFFFFF }

// EncodeBranchImm19 masks a signed word offset into the 19-bit field, placed
// at bits 23:5. Callers should check FitsBranchImm19 first.
func EncodeBranchImm19(word int64) uint32 { return (uint32(word) & 0x0007FFFF) << 5 }

// EncodeBranchImm14 masks a signed word offset into the 14-bit field, placed
// at bits 18:5. Callers should check FitsBranchImm14 first.
func EncodeBranchImm14(word int64) uint32 { return (uint32(word) & 0x00003FFF) << 5 }

// DecodeBranchImm26 sign-extends a 26-bit field back to a word offset.
func DecodeBranchImm26(field uint32) int64 { return signExtend(field&0x03FFFFFF, 26) }

// DecodeBranchImm19 sign-extends the 19-bit field (from bits 23:5) to a word
// offset.
func DecodeBranchImm19(word uint32) int64 { return signExtend((word>>5)&0x0007FFFF, 19) }

// DecodeBranchImm14 sign-extends the 14-bit field (from bits 18:5) to a word
// offset.
func DecodeBranchImm14(word uint32) int64 { return signExtend((word>>5)&0x00003FFF, 14) }

// ---------------------------------------------------------------------------
// Small pure helpers.
// ---------------------------------------------------------------------------

// isMask reports whether v is 0...01...1 (a right-aligned run of ones,
// nonzero).
func isMask(v uint64) bool { return v != 0 && (v+1)&v == 0 }

// isShiftedMask reports whether v is 0...01...10...0 (one contiguous run of
// ones, anywhere, nonzero).
func isShiftedMask(v uint64) bool { return v != 0 && isMask((v-1)|v) }

// rorInElem rotates the low esize bits of v right by r, within esize.
func rorInElem(v uint64, r uint, esize int) uint64 {
	r %= uint(esize)
	mask := ^uint64(0)
	if esize < 64 {
		mask = (uint64(1) << uint(esize)) - 1
	}
	v &= mask
	return ((v >> r) | (v << (uint(esize) - r))) & mask
}

// replicate tiles an esize-bit pattern across a full 64-bit word.
func replicate(elem uint64, esize int) uint64 {
	out := elem
	for s := esize; s < 64; s <<= 1 {
		out |= out << uint(s)
	}
	return out
}

// signExtend sign-extends the low n bits of v to a signed 64-bit value.
func signExtend(v uint32, n uint) int64 {
	shift := 64 - n
	return int64(uint64(v)<<shift) >> shift
}