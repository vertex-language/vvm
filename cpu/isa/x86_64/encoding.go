package x86_64

// ModRM.mod field values — unchanged from IA-32.
const (
	ModIndir  byte = 0 // [rm], no displacement — but see RMRIP/SIBNoBase
	ModDisp8  byte = 1 // [rm+disp8]
	ModDisp32 byte = 2 // [rm+disp32]
	ModReg    byte = 3 // rm is a register operand, not a memory reference
)

// The irregular field values every ModRM emitter and decoder special-cases.
// The bit values are the same as IA-32's; two of the *meanings* change in
// long mode and are renamed here to say so.
const (
	// RMSIB in ModRM.rm (mod != ModReg) means "a SIB byte follows". As in
	// 32-bit mode it occupies RSP's low-3 encoding, so [rsp+disp] always
	// needs SIB. (r12 shares the low 3 bits and inherits the same rule.)
	RMSIB byte = 4

	// RMRIP in ModRM.rm with mod == ModIndir is the form that was pure
	// disp32 absolute in 32-bit mode. In long mode it is instead
	// RIP-relative: [rip + disp32], a signed 32-bit displacement from the
	// *next* instruction's address. This is the single biggest semantic
	// break from isa/x86, where the identical bit pattern (its RMDisp32)
	// meant an absolute address. An absolute [disp32] in long mode is no
	// longer reachable this way and must go through the SIB no-base form
	// below. It occupies RBP's low-3 encoding, which is why an RBP (or
	// r13) base can never use mod == ModIndir and always carries at least
	// a disp8.
	RMRIP byte = 5

	// SIBNoIndex in SIB.index means "no index register". Occupies RSP's
	// encoding, which is why RSP can never be a SIB index. Unlike a base,
	// this is *not* extended by REX.X: index 0b0100 with REX.X set names
	// r12, a real index, not "no index".
	SIBNoIndex byte = 4

	// SIBNoBase in SIB.base with mod == ModIndir means "no base register,
	// a disp32 follows". Combined with SIBNoIndex it gives the absolute
	// [disp32] form (SIB byte 0x25) that RMRIP no longer provides.
	SIBNoBase byte = 5
)

// PackModRM builds a ModRM byte from its three (low-3-bit) fields.
func PackModRM(mod, reg, rm byte) byte { return mod<<6 | reg<<3 | rm }

// UnpackModRM splits a ModRM byte into (mod, reg, rm).
func UnpackModRM(b byte) (mod, reg, rm byte) { return b >> 6, b >> 3 & 7, b & 7 }

// PackSIB builds a SIB byte from its three (low-3-bit) fields.
func PackSIB(scale, index, base byte) byte { return scale<<6 | index<<3 | base }

// UnpackSIB splits a SIB byte into (scale, index, base).
func UnpackSIB(b byte) (scale, index, base byte) { return b >> 6, b >> 3 & 7, b & 7 }

// ScaleBits maps a SIB scale factor to its 2-bit field encoding; 0 is a
// synonym for 1. Unchanged from IA-32.
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

// ScaleFactor inverts ScaleBits. Total: the field is two bits wide.
func ScaleFactor(bits byte) byte { return 1 << (bits & 3) }

// Legacy/mandatory prefix bytes, unchanged from IA-32. Note Prefix67 now
// switches addressing 64->32 bit rather than 32->16, and Prefix66 selects
// 16-bit operand size against a 32-bit default (REX.W selects 64-bit).
const (
	Prefix66 = 0x66 // operand-size override (-> 16 bit)
	Prefix67 = 0x67 // address-size override (64 -> 32 bit addressing)
	PrefixF0 = 0xF0 // LOCK
	PrefixF2 = 0xF2 // REPNE/REPNZ; also a mandatory prefix
	PrefixF3 = 0xF3 // REP/REPE; also a mandatory prefix (e.g. popcnt)
)

// FitsDisp8 / FitsImm8 — unchanged; displacement and sign-extended-imm8
// widths are the same in long mode.
func FitsDisp8(d int32) bool { return d >= -128 && d <= 127 }
func FitsImm8(v int64) bool  { return v >= -128 && v <= 127 }

// FitsImm32 reports whether a 64-bit immediate fits a sign-extended imm32
// field — the widest immediate every instruction *except* the special
// movabs form accepts. A value that fails this and is needed as a full
// 64-bit constant must use mov r64, imm64 (0xB8+r with REX.W).
func FitsImm32(v int64) bool { return v >= -1<<31 && v <= 1<<31-1 }