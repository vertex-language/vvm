package aarch64

// Data-processing (immediate) ADD/SUB family — [W-form, X-form] base
// words; the 12-bit unsigned immediate (optionally LSL #12, bit 22) and
// Rn/Rd are filled in by the caller.
var DPImmOpcodes = map[string][2]uint32{
	"add":  {0x11000000, 0x91000000},
	"adds": {0x31000000, 0xB1000000},
	"sub":  {0x51000000, 0xD1000000},
	"subs": {0x71000000, 0xF1000000},
}

// Data-processing (register, shifted) ADD/SUB/AND/ORR/EOR/BIC family —
// [W-form, X-form] base words; Rm/Rn/Rd filled in by the caller.
var DPRegOpcodes = map[string][2]uint32{
	"add":  {0x0B000000, 0x8B000000},
	"adds": {0x2B000000, 0xAB000000},
	"sub":  {0x4B000000, 0xCB000000},
	"subs": {0x6B000000, 0xEB000000},
	"and":  {0x0A000000, 0x8A000000},
	"orr":  {0x2A000000, 0xAA000000},
	"eor":  {0x4A000000, 0xCA000000},
	"bic":  {0x0A200000, 0x8A200000},
}

// ADD/SUB (extended register) — the form used to compute SP-relative
// addresses that overflow the immediate ADD/SUB's 24-bit range. X-form
// only; this compiler never needs a W-form SP computation.
const (
	OpAddExtX uint32 = 0x8B206000
	OpSubExtX uint32 = 0xCB206000
)

// mov_r/mvn/neg's shifted-register forms with Rn fixed to ZR — a
// mechanical specialization of DPRegOpcodes/DP1Opcodes-shaped
// instructions, named directly rather than composed at every call site.
const (
	OpMovReg uint32 = 0x2A0003E0 // ORR Wd, WZR, Wm  (mov_r, W-form)
	OpMovRegX uint32 = 0xAA0003E0 // ORR Xd, XZR, Xm  (mov_r, X-form)
	OpMvnReg uint32 = 0x2A2003E0 // ORN Wd, WZR, Wm
	OpMvnRegX uint32 = 0xAA2003E0 // ORN Xd, XZR, Xm
	OpNegReg uint32 = 0x4B0003E0 // SUB Wd, WZR, Wm
	OpNegRegX uint32 = 0xCB0003E0 // SUB Xd, XZR, Xm
)

// Data-processing (2-source) family — the 3-bit "opcode" subfield shared
// by UDIV/SDIV/LSLV/LSRV/ASRV/RORV (base word supplied separately, see
// OpDP2Base in encoding.go, since it also carries the sf bit).
var DP2Opcodes = map[string]uint32{
	"udiv": 0x2, "sdiv": 0x3,
	"lslv": 0x8, "lsrv": 0x9, "asrv": 0xA, "rorv": 0xB,
}

const OpDP2Base uint32 = 0x1AC00000

// Data-processing (1-source) family — [W-form, X-form] base words for
// RBIT/REV16/REV/CLZ.
var DP1Opcodes = map[string][2]uint32{
	"rbit":  {0x5AC00000, 0xDAC00000},
	"rev16": {0x5AC00400, 0xDAC00400},
	"rev":   {0x5AC00800, 0xDAC00C00},
	"clz":   {0x5AC01000, 0xDAC01000},
}

// 3-source multiply family — fixed base words; Rd/Rn/Rm/Ra filled in by
// the caller. MUL is MADD with Ra=ZR baked into the base word rather than
// a separate opcode.
const (
	OpMul   uint32 = 0x1B007C00
	OpMSub  uint32 = 0x1B008000
	OpSMulH uint32 = 0x9B407C00
	OpUMulH uint32 = 0x9BC07C00
	OpSMull uint32 = 0x9B207C00
	OpUMull uint32 = 0x9BA07C00
)

// CSET/CSEL — CSET is CSINC Rd, ZR, ZR, invert(cond) with a fixed base
// word; CSEL carries the sf bit itself (see encoding.go).
const (
	OpCSet  uint32 = 0x1A9F07E0
	OpCSel  uint32 = 0x1A800000
)

// LdStClass is one integer load/store's {scaled unsigned-immediate form,
// unscaled ("LDUR"-style) form, and the scale (== access size) the scaled
// form's immediate field is a multiple of}.
type LdStClass struct {
	Scaled, Unscaled uint32
	Scale            uint32
}

var LdClasses = map[int]LdStClass{
	1: {0x39400000, 0x38400000, 1},
	2: {0x79400000, 0x78400000, 2},
	4: {0xB9400000, 0xB8400000, 4},
	8: {0xF9400000, 0xF8400000, 8},
}

var StClasses = map[int]LdStClass{
	1: {0x39000000, 0x38000000, 1},
	2: {0x79000000, 0x78000000, 2},
	4: {0xB9000000, 0xB8000000, 4},
	8: {0xF9000000, 0xF8000000, 8},
}

// Byte load/store, register offset — LDRB/STRB Wt, [Xn, Xm].
const (
	OpLdrbReg uint32 = 0x38606800
	OpStrbReg uint32 = 0x38206800
)

// Load-acquire/store-release/exclusive family. The 2-bit size field is
// packed separately (SizeBits, encoding.go) since it's shared across all
// four.
const (
	OpLdar  uint32 = 0x08DFFC00
	OpStlr  uint32 = 0x089FFC00
	OpLdaxr uint32 = 0x085FFC00
	OpStlxr uint32 = 0x0800FC00
	OpClrex uint32 = 0xD5033F5F
	OpDmb   uint32 = 0xD5033BBF
)

// Branch/system fixed words. Bxx/BL/B.cond/CBZ/CBNZ leave their
// label/symbol-relative field for a fixup or patch to fill in; RET's
// implicit-X30 form and SVC/BRK's fixed encodings need nothing further.
const (
	OpB     uint32 = 0x14000000 // B          — imm26
	OpBL    uint32 = 0x94000000 // BL         — imm26
	OpBCond uint32 = 0x54000000 // B.cond     — cond[3:0], imm19
	OpCBase uint32 = 0x34000000 // CBZ (|1<<24 for CBNZ, sf in bit 31) — imm19
	OpBLR   uint32 = 0xD63F0000 // BLR Xn
	OpBR    uint32 = 0xD61F0000 // BR Xn
	OpRet   uint32 = 0xD65F03C0 // RET        (implicit X30)
	OpSVC   uint32 = 0xD4000001 // SVC #imm16
	OpBrk   uint32 = 0xD4200000 // BRK #0
)

// MOVZ/MOVN/MOVK — X-form only (this compiler never needs a W-form
// 64-bit-immediate sequence). The 2-bit "hw" shift-amount field (shift/16)
// and 16-bit immediate are filled in by the caller.
const (
	OpMovnX uint32 = 0x92800000
	OpMovzX uint32 = 0xD2800000
	OpMovkX uint32 = 0xF2800000
)

// STP (pre-index)/LDP (post-index), 64-bit pair — the two instructions
// lower/aarch64 will use to save/restore FP/LR as ordinary instructions
// rather than have the encoder splice them in automatically (see
// isa/aarch64/encoder's README note on this). imm7 = disp/8, packed by
// PackPair in encoding.go.
const (
	OpSTPPre64  uint32 = 0xA9800000
	OpLDPPost64 uint32 = 0xA8C00000
)