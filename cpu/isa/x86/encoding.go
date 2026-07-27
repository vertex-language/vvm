package x86

// ModRM.mod field values. ModReg means "rm names a register"; the other
// three name a memory form carrying 0, 1, or 4 bytes of displacement.
const (
	ModIndir  byte = 0 // [rm], no displacement — but see RMDisp32/SIBNoBase
	ModDisp8  byte = 1 // [rm+disp8]
	ModDisp32 byte = 2 // [rm+disp32]
	ModReg    byte = 3 // rm is a register operand, not a memory reference
)

// The four irregular field values every ModRM emitter and decoder has to
// special-case. Each is an escape that collides with a register encoding,
// which is what makes the corresponding addressing form unrepresentable
// the obvious way.
const (
	// RMSIB in ModRM.rm (mod != ModReg) means "a SIB byte follows". It
	// occupies ESP's encoding, which is why [esp+disp] always needs SIB.
	RMSIB byte = 4

	// RMDisp32 in ModRM.rm with mod == ModIndir means "no base register,
	// a disp32 follows" — the absolute form. It occupies EBP's encoding,
	// which is why an EBP base can never use mod == ModIndir and always
	// carries at least a disp8, even when the displacement is zero.
	RMDisp32 byte = 5

	// SIBNoIndex in SIB.index means "no index register". It occupies
	// ESP's encoding, which is why ESP can never be a SIB index.
	SIBNoIndex byte = 4

	// SIBNoBase in SIB.base with mod == ModIndir means "no base
	// register, a disp32 follows".
	SIBNoBase byte = 5
)

// PackModRM builds a ModRM byte from its three fields.
func PackModRM(mod, reg, rm byte) byte { return mod<<6 | reg<<3 | rm }

// UnpackModRM splits a ModRM byte into (mod, reg, rm).
func UnpackModRM(b byte) (mod, reg, rm byte) {
	return b >> 6, b >> 3 & 7, b & 7
}

// PackSIB builds a SIB byte from its three fields.
func PackSIB(scale, index, base byte) byte { return scale<<6 | index<<3 | base }

// UnpackSIB splits a SIB byte into (scale, index, base).
func UnpackSIB(b byte) (scale, index, base byte) {
	return b >> 6, b >> 3 & 7, b & 7
}

// ScaleBits maps a SIB scale factor to its 2-bit field encoding. 1, 2, 4
// and 8 are the four real factors; 0 is accepted as a synonym for 1
// because an operand with no index register leaves its scale unset. ok is
// false for anything else.
func ScaleBits(v byte) (bits byte, ok bool) {
	switch v {
	case 0, 1:
		return 0, true
	case 2:
		return 1, true
	case 4:
		return 2, true
	case 8:
		return 3, true
	}
	return 0, false
}

// ScaleFactor inverts ScaleBits. Total, because the field is two bits
// wide and all four values are meaningful.
func ScaleFactor(bits byte) byte { return 1 << (bits & 3) }

// Legacy prefix bytes the encoder and decoder both need named rather than
// left as bare hex.
const (
	Prefix66 = 0x66 // operand-size override (32 -> 16 bit)
	Prefix67 = 0x67 // address-size override (32 -> 16 bit addressing)
	PrefixF0 = 0xF0 // LOCK
	PrefixF2 = 0xF2 // REPNE/REPNZ; also a mandatory prefix (SSE2 scalar-double)
	PrefixF3 = 0xF3 // REP/REPE; also a mandatory prefix (e.g. popcnt)
)

// FitsDisp8 reports whether a displacement fits the mod == ModDisp8 form,
// saving three bytes over ModDisp32.
func FitsDisp8(d int32) bool { return d >= -128 && d <= 127 }

// FitsImm8 reports whether an immediate fits the sign-extended imm8
// encodings — AluImm8 (0x83) for the two-operand ALU group and Imul3Imm8
// (0x6B) for three-operand imul. Both are exactly equivalent to their
// imm32 counterparts when this holds.
func FitsImm8(v int64) bool { return v >= -128 && v <= 127 }