package x86

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

// ScaleBits maps a SIB scale factor (1, 2, 4, or 8) to its 2-bit field
// encoding. ok is false if v isn't one of those four values.
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

// Legacy prefix bytes the encoder and decoder both need to recognize.
const (
	Prefix66 = 0x66 // operand-size override (32 -> 16 bit)
	PrefixF0 = 0xF0 // LOCK
	PrefixF3 = 0xF3 // REP/REPE (also a mandatory prefix, e.g. popcnt)
)