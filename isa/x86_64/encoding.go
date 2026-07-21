// ModRM/SIB byte layout, REX prefix construction, and the legacy prefix
// bytes this encoder uses. These are bit-layout facts straight out of the
// Intel/AMD manuals — which field a ModRM byte's bits are, which REX bit
// extends which field, which rm/mod combinations mean "add a SIB byte" or
// "this is [RIP+disp32] instead of [reg]" — not anything specific to how
// this compiler represents an instruction stream.
package x86_64

// ModRM mod-field values.
const (
	ModDisp0  = 0 // [reg]; also the mod for [RIP+disp32] and for SIB-escape forms
	ModDisp8  = 1 // [reg+disp8]
	ModDisp32 = 2 // [reg+disp32]
	ModReg    = 3 // reg, reg (register-direct)
)

// rm/SIB field values that change meaning instead of naming a register.
const (
	RMNeedsSIB    = 4 // rm==4 (RSP/R12): this rm value always means "read a SIB byte next"
	RMRipOrDisp32 = 5 // rm==5: mod==0 means [RIP+disp32]; mod!=0 means [RBP/R13+disp]
	SIBNoIndex    = 4 // SIB.index==4 means "no index register"
	SIBBaseEscape = 5 // SIB.base==5 with SIB mod==0 means absolute [disp32], no base reg
)

// PackModRM assembles a ModRM byte from its three fields. reg and rm are
// taken as their low 3 bits only — callers fold the REX.R/B extension bit
// in separately (see PackREX / HiBit).
func PackModRM(mod, reg, rm byte) byte { return mod<<6 | (reg&7)<<3 | (rm & 7) }

// UnpackModRM splits a ModRM byte back into its three raw fields.
func UnpackModRM(b byte) (mod, reg, rm byte) { return b >> 6, b >> 3 & 7, b & 7 }

// PackSIB assembles a SIB byte from scale (0-3, meaning a stride of 2^scale),
// index, and base, using the same low-3-bits convention as PackModRM.
func PackSIB(scale, index, base byte) byte { return scale<<6 | (index&7)<<3 | (base & 7) }

// HiBit is register r's REX extension bit — bit 3 of its 4-bit encoding,
// which selects r8-r15 when set. Callers fold this into whichever of
// REX.R/X/B corresponds to the field r occupies (ModRM.reg, SIB.index, or
// ModRM.rm/SIB.base).
func HiBit(r Reg) byte { return byte(r) >> 3 & 1 }

// LoBits is register r's 3-bit ModRM/SIB field value, with the REX
// extension bit already stripped off.
func LoBits(r Reg) byte { return byte(r) & 7 }

// PackREX assembles a REX prefix byte (0100WRXB) from its four bits. w
// selects 64-bit operand size; r/x/b extend ModRM.reg, SIB.index, and
// ModRM.rm/SIB.base respectively to reach r8-r15.
func PackREX(w, r, x, b bool) byte {
	var bits byte
	if w {
		bits |= 1 << 3
	}
	if r {
		bits |= 1 << 2
	}
	if x {
		bits |= 1 << 1
	}
	if b {
		bits |= 1
	}
	return 0x40 | bits
}

// Legacy prefix bytes this encoder emits.
const (
	PrefixOperandSize = 0x66 // 16-bit operand-size override
	PrefixLock        = 0xF0
	PrefixRepne       = 0xF2 // REPNE / mandatory prefix (scalar-double SSE forms)
	PrefixRep         = 0xF3 // REP / mandatory prefix (scalar-single SSE forms, POPCNT)
)