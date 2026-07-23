// encoder/inst.go
//
// Package encoder is the generic A64 (AArch64 / ARM64) assembler: a pseudo-
// instruction stream (Inst/Opr) built entirely from physical registers,
// immediates, symbols, and plain memory operands — never a not-yet-placed
// value — plus the Encode function that turns that stream into machine
// words.
//
// It is the AArch64 sibling of isa/arm/encoder and isa/x86_64/encoder and
// keeps the same contract: no knowledge of any register-allocation policy or
// calling convention, reusable by anything that wants "turn this Inst stream
// into A64 words". The differences from the A32 encoder are all consequences
// of the machine, and are called out at the sites where they appear. Four
// are structural enough to state here:
//
//   - Conditionality is an operand, not a property. A32 put a condition on
//     every instruction, which forced that encoder's Cond type to renumber AL
//     to zero so a zero-value Inst wasn't secretly EQ. Only b.cond, the
//     conditional-select family, and the conditional-compare family read a
//     condition in A64, and none of them is meaningful without one, so the
//     ISA codes are re-exported raw here, as the x86 encoders do.
//
//   - Width *is* a property, and needs the trick A32 used for the condition.
//     isa/aarch64's Width numbers W32 as zero, so a zero-value Inst built
//     against it would silently mean 32-bit. The Width type below numbers the
//     64-bit view as zero instead, so an Inst built without an explicit width
//     is a doubleword operation, and sf() maps back at emit time.
//
//   - Slot 31 needs a role, and the value cannot carry it. RZR and RSP are
//     the same Reg. Which one an operand means is a per-field fact of the
//     instruction, so Opr carries an SP flag that the constructors set, and
//     Encode checks it against what each field actually accepts. This is the
//     "whether encoding 31 should be sp or xzr" decision isa/aarch64's README
//     hands to the encoder.
//
//   - There is no PC-read skew. An A64 branch offset is relative to the
//     branch's own address, so nothing here does A32's +8 dance — the
//     FixupPCRel* addends default to 0, not -8.
package encoder

import isaa64 "github.com/vertex-language/vvm/isa/aarch64"

// Reg and the register constants are the same type/values as isa/aarch64's,
// aliased so callers building an Inst stream don't need to import
// isa/aarch64 directly for anything but the odd diagnostic.
type Reg = isaa64.Reg

const (
	R0  = isaa64.R0
	R1  = isaa64.R1
	R2  = isaa64.R2
	R3  = isaa64.R3
	R4  = isaa64.R4
	R5  = isaa64.R5
	R6  = isaa64.R6
	R7  = isaa64.R7
	R8  = isaa64.R8
	R9  = isaa64.R9
	R10 = isaa64.R10
	R11 = isaa64.R11
	R12 = isaa64.R12
	R13 = isaa64.R13
	R14 = isaa64.R14
	R15 = isaa64.R15
	R16 = isaa64.R16
	R17 = isaa64.R17
	R18 = isaa64.R18
	R19 = isaa64.R19
	R20 = isaa64.R20
	R21 = isaa64.R21
	R22 = isaa64.R22
	R23 = isaa64.R23
	R24 = isaa64.R24
	R25 = isaa64.R25
	R26 = isaa64.R26
	R27 = isaa64.R27
	R28 = isaa64.R28
	R29 = isaa64.R29
	R30 = isaa64.R30

	// ZR and SPr are the same value (31). They are named separately only so
	// call sites read as one or the other; use R(ZR) and Rsp() to build
	// operands, which is where the role actually gets recorded.
	ZR  = isaa64.RZR
	SPr = isaa64.RSP

	FP = isaa64.RFP
	LR = isaa64.RLR

	RNone = isaa64.RNone
)

// Width selects the operand width — the instruction's sf bit.
//
// The zero value is X (64-bit), deliberately inverting isa/aarch64's Width,
// whose zero value is W32. A compiler backend's default operand is pointer-
// width, and an Inst literal that omits the field should mean that rather
// than quietly truncating to 32 bits. Same reasoning as the A32 encoder's
// AL-as-zero Cond, applied to the other axis.
type Width byte

const (
	X Width = iota // 64-bit, "x" registers — the zero value / default
	W              // 32-bit, "w" registers
)

func (w Width) isa() isaa64.Width {
	if w == W {
		return isaa64.W32
	}
	return isaa64.W64
}

func (w Width) sf() uint32 { return uint32(w.isa().SF()) }
func (w Width) bits() int  { return w.isa().Bits() }
func (w Width) is32() bool { return w == W }

// Cond is a condition selector. Unlike the A32 encoder, the ISA codes are
// re-exported raw: a condition is an operand of three instruction families
// here, not a field on every instruction, so there is no zero-value trap to
// design around.
type Cond = byte

const (
	EQ = isaa64.CondEQ
	NE = isaa64.CondNE
	CS = isaa64.CondCS
	CC = isaa64.CondCC
	MI = isaa64.CondMI
	PL = isaa64.CondPL
	VS = isaa64.CondVS
	VC = isaa64.CondVC
	HI = isaa64.CondHI
	LS = isaa64.CondLS
	GE = isaa64.CondGE
	LT = isaa64.CondLT
	GT = isaa64.CondGT
	LE = isaa64.CondLE
	AL = isaa64.CondAL

	// Documented synonyms — the carry flag read as an unsigned comparison.
	HS = isaa64.CondHS
	LO = isaa64.CondLO
)

// Negate returns the complementary condition, for inverting a two-way
// b.cond or flipping a csel. Feeding it AL yields the NV spelling, which
// behaves as "always" rather than "never" — see isa/aarch64's CondNV. An
// unconditional branch has no negation to take, so callers should not be
// asking.
func Negate(c Cond) Cond { return isaa64.NegateCond(c) }

// Shift-type selectors for a shifted-register operand. ROR is legal only in
// the logical family; the add/sub forms reject it (isaa64.ShiftAllowsROR).
const (
	LSL = isaa64.ShiftLSL
	LSR = isaa64.ShiftLSR
	ASR = isaa64.ShiftASR
	ROR = isaa64.ShiftROR
)

// Extend-type selectors for an extended-register operand (the option field,
// bits 15:13) and for a scaled register index in a memory operand.
//
// These are machine facts and arguably belong in isa/aarch64/opcodes.go
// beside the shift types; they are here because that package does not name
// them yet. The LSL spelling is width-dependent — it is UXTW for a 32-bit
// operation and UXTX for a 64-bit one — which is why there is no LSL
// constant in this set. Encode applies that mapping itself.
const (
	UXTB byte = 0
	UXTH byte = 1
	UXTW byte = 2
	UXTX byte = 3
	SXTB byte = 4
	SXTH byte = 5
	SXTW byte = 6
	SXTX byte = 7
)

type OprKind byte

const (
	ONone OprKind = iota
	OReg          // a register, with an optional shift (shifted-register form)
	OExt          // a register with an extend and optional shift (extended form)
	OImm          // immediate; Sym != "" makes it a symbol address
	OMem          // [base {, offset}], offset / pre-index / post-index
)

// MemMode names the three load/store addressing classes. A64 keeps them as
// genuinely distinct encodings with different immediate formats — the offset
// forms take a scaled unsigned imm12 (or an unscaled signed imm9 via the
// LDUR/STUR encodings), the indexed forms only the unscaled imm9 — so this
// is an enum rather than the A32 encoder's Pre/Wback bool pair.
//
// Named ModeOffset/ModePre/ModePost (not MemOffset/MemPre/MemPost) because
// the latter two names belong to the Opr-builder functions below
// (MemPre(base, disp), MemPost(base, disp)); an enum value and a function
// can't share an identifier in the same package.
type MemMode byte

const (
	ModeOffset MemMode = iota // [Xn{, #imm}] or [Xn, Xm{, ext}] — no write-back
	ModePre                   // [Xn, #imm]! — write back the updated base
	ModePost                  // [Xn], #imm — write back the updated base
)

// Opr is one fully-resolved operand. There is deliberately no "unresolved
// slot" variant — that belongs one layer up, in whatever builds this stream.
//
// SP records the slot-31 role for Reg: with SP true, encoding 31 is the
// stack pointer; with it false, the zero register. The flag is meaningless
// for any other register number and is ignored there. Encode checks it
// against what each field of each format actually accepts, which is where
// the irregularity lives: an add/sub *shifted-register* operand may not be
// SP at all, while the immediate and extended forms take Xn|SP in Rn and
// Xd|SP in Rd unless they set flags.
//
// A memory operand's Base is always Xn|SP by the format, so it carries no
// flag; slot 31 there is unconditionally the stack pointer.
type Opr struct {
	Kind OprKind

	Reg Reg    // OReg / OExt: the register
	SP  bool   // slot-31 role for Reg: true => sp/wsp, false => xzr/wzr
	Imm int64  // OImm value
	Sym string // OImm / OMem: symbol whose address (or low 12 bits) this is

	// Shifted-register fields (OReg).
	Shift    byte // LSL/LSR/ASR/ROR
	ShiftAmt byte // imm6: 0-63 for a 64-bit operation, 0-31 for 32-bit

	// Extended-register fields (OExt), also used for a memory index.
	Ext    byte // UXTB..SXTX; for a memory index only UXTW/UXTX/SXTW/SXTX
	ExtAmt byte // imm3: 0-4, the left shift applied after extension

	// Memory fields (OMem).
	Mode   MemMode
	Base   Reg // always Xn|SP
	Index  Reg // RNone => immediate offset
	Disp   int64
	Scaled bool // register index: apply the access-size shift (the S bit)
}

// R names a register operand whose slot-31 reading, if it is slot 31, is the
// zero register. This is the common case: most fields are Xd|ZR or Xn|ZR.
func R(r Reg) Opr { return Opr{Kind: OReg, Reg: r} }

// Rsp names the stack pointer as an operand. It encodes to 31 exactly as
// R(ZR) does; the difference is the role recorded alongside, which Encode
// validates against the field.
func Rsp() Opr { return Opr{Kind: OReg, Reg: SPr, SP: true} }

// RShift is a register shifted by an immediate (Rm, <shift> #amt). ROR is
// accepted only by the logical family.
func RShift(r Reg, shift, amt byte) Opr {
	return Opr{Kind: OReg, Reg: r, Shift: shift, ShiftAmt: amt}
}

// RExt is a register sign- or zero-extended and then shifted left by amt
// (Rm, <extend> #amt), the second operand of the extended-register add/sub
// forms. amt is 0-4.
func RExt(r Reg, ext, amt byte) Opr {
	return Opr{Kind: OExt, Reg: r, Ext: ext, ExtAmt: amt}
}

func Imm(v int64) Opr { return Opr{Kind: OImm, Imm: v} }

// SymAddr names a symbol's address. What the encoder does with it depends on
// the instruction: adrp takes the page, an add immediate takes the low 12
// bits, and a load/store offset takes the low 12 bits scaled to the access.
// Materializing a full 64-bit address some other way (a movz/movk chain, a
// literal-pool load) is a lowering decision, not one made here.
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

// Mem is [base{, #disp}] with no write-back.
func Mem(base Reg, disp int64) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Mode: ModeOffset}
}

// MemPre is [base, #disp]! — the base is updated before the access.
func MemPre(base Reg, disp int64) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Mode: ModePre}
}

// MemPost is [base], #disp — the access uses the old base, which is then
// updated.
func MemPost(base Reg, disp int64) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Mode: ModePost}
}

// MemIdx is [base, index, <ext> {#amt}]. scaled selects the S bit, which
// shifts the index left by the access size's log2 — the only shift amount
// the register-offset form can express. ext must be UXTW, UXTX, SXTW or
// SXTX; the sub-word extends are UNDEFINED as an index.
func MemIdx(base, index Reg, ext byte, scaled bool) Opr {
	return Opr{Kind: OMem, Base: base, Index: index, Ext: ext, Scaled: scaled, Mode: ModeOffset}
}

// MemSym is [base, #:lo12:sym] — the low-12 half of an adrp/add or
// adrp/ldr pair. The encoder emits the access-size-appropriate
// LDST*_ABS_LO12_NC fixup and leaves the imm12 field zero.
func MemSym(base Reg, sym string) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Sym: sym, Mode: ModeOffset}
}

// Inst is one pre-encoding pseudo instruction. encode.go's Encode switch is
// the authoritative list of Op spellings and of which fields each reads; the
// summary below is a map, not the territory.
//
//	Op                          reads                notes
//	----------------------------------------------------------------------
//	label                       Lbl                  defines a local branch target
//	add/adds/sub/subs           D, N, M, W           M picks the form: OImm =>
//	                                                 immediate, OReg => shifted,
//	                                                 OExt => extended
//	cmp/cmn                     N, M, W              subs/adds with Rd = zr
//	neg/negs                    D, M, W              sub/subs with Rn = zr
//	and/orr/eor/ands            D, N, M, W           OImm => bitmask form
//	bic/orn/eon/bics            D, N, M, W           shifted-register only
//	tst                         N, M, W              ands with Rd = zr
//	mvn                         D, M, W              orn with Rn = zr
//	mov                         D, M, W              orr Rd, zr, Rm, or add #0
//	                                                 when either side is sp
//	movz/movn/movk              D, Imm, Imm2, W      Imm2 is the lsl amount
//	udiv/sdiv                   D, N, M, W
//	lslv/lsrv/asrv/rorv         D, N, M, W           register-amount shifts
//	lsl/lsr/asr                 D, N, M, W           OReg => the *v form,
//	                                                 OImm => the ubfm/sbfm alias
//	sbfm/ubfm/bfm               D, N, Imm, Imm2, W   Imm=immr, Imm2=imms
//	sxtb/sxth/sxtw/uxtb/uxth    D, N, W              bitfield aliases
//	extr                        D, N, M, Imm, W      Imm is lsb
//	clz/cls/rbit/rev/rev16/rev32 D, N, W
//	madd/msub                   D, N, M, A, W        Rd := Ra +/- Rn*Rm
//	mul/mneg                    D, N, M, W           madd/msub with Ra = zr
//	smull/umull/smnegl/umnegl   D, N, M              64-bit result, W sources
//	smaddl/umaddl/smsubl/umsubl D, N, M, A
//	smulh/umulh                 D, N, M
//	ldr/str                     D, M, W              M is an OMem
//	ldrb/strb/ldrh/strh         D, M                 W selects the register view
//	ldrsb/ldrsh                 D, M, W              W selects the target width
//	ldrsw                       D, M                 64-bit target only
//	ldp/stp                     D, A, M, W           D=Rt, A=Rt2, M is an OMem
//	adr/adrp                    D, Lbl or Sym        21-bit byte / page offset
//	b/bl                        Lbl or Sym           imm26
//	b.cond                      CC, Lbl or Sym       imm19
//	cbz/cbnz                    D, Lbl or Sym, W     imm19
//	tbz/tbnz                    D, Imm, Lbl or Sym   Imm is the bit number; imm14
//	br/blr/ret                  N                    register target; ret
//	                                                 defaults to x30
//	csel/csinc/csinv/csneg      D, N, M, CC, W
//	cset/csetm                  D, CC, W             csinc/csinv on zr, cond
//	                                                 inverted
//	cinc/cinv/cneg              D, N, CC, W
//	ccmp/ccmn                   N, M, Imm, CC, W     Imm is the nzcv value; M
//	                                                 may be OImm (imm5)
//	svc/brk                     Imm                  the 16-bit comment field
//	nop/udf                     —
type Inst struct {
	Op   string
	W    Width // operand width; zero value X (64-bit)
	CC   Cond  // condition, for b.cond / csel family / ccmp family
	D    Opr   // Rd / Rt
	N    Opr   // Rn / base / branch target register
	M    Opr   // Rm / Op2 / memory operand
	A    Opr   // Ra (multiply-accumulate) / Rt2 (ldp/stp)
	Lbl  string
	Sym  string
	Imm  int64 // immr / bit number / nzcv / svc comment / movz shift
	Imm2 int64 // imms / move-wide immediate's second field where needed
}

// FixupKind is this backend's vocabulary for the relocation holes Encode
// leaves. Every A64 relocation is a word-internal bit-field patch, and the
// names track AAELF64's so an object writer can map them without a table.
//
// The low-12 load/store relocations are split by access size because the
// linker has to know the scale to place bits 11:0 into a scaled imm12 —
// which is why A32 needed one movw_abs where this needs four.
type FixupKind int

const (
	// FixupCall26: the BL imm26 field := (S + A - P) >> 2. P is the
	// instruction's own address; A defaults to 0, since A64 has no PC-read
	// skew to compensate. (R_AARCH64_CALL26)
	FixupCall26 FixupKind = iota
	// FixupJump26: the same field for a plain B. (R_AARCH64_JUMP26)
	FixupJump26
	// FixupCondBr19: the imm19 field of B.cond / CBZ / CBNZ, bits 20:2 of
	// (S + A - P). (R_AARCH64_CONDBR19)
	FixupCondBr19
	// FixupTestBr14: the imm14 field of TBZ / TBNZ, bits 15:2 of
	// (S + A - P). (R_AARCH64_TSTBR14)
	FixupTestBr14
	// FixupAdrPrelPgHi21: the ADRP immhi:immlo split field, bits 32:12 of
	// Page(S + A) - Page(P). (R_AARCH64_ADR_PREL_PG_HI21)
	FixupAdrPrelPgHi21
	// FixupAdrPrelLo21: the ADR immhi:immlo field, bits 20:0 of
	// (S + A - P). (R_AARCH64_ADR_PREL_LO21)
	FixupAdrPrelLo21
	// FixupAddAbsLo12Nc: the ADD imm12 field, bits 11:0 of (S + A), no
	// overflow check. (R_AARCH64_ADD_ABS_LO12_NC)
	FixupAddAbsLo12Nc
	// FixupLdSt8AbsLo12Nc and friends: the load/store imm12 field, bits
	// 11:0, 11:1, 11:2 and 11:3 of (S + A) respectively — the scaled
	// immediate for a 1-, 2-, 4- or 8-byte access.
	FixupLdSt8AbsLo12Nc
	FixupLdSt16AbsLo12Nc
	FixupLdSt32AbsLo12Nc
	FixupLdSt64AbsLo12Nc
)

func (k FixupKind) String() string {
	switch k {
	case FixupCall26:
		return "call26"
	case FixupJump26:
		return "jump26"
	case FixupCondBr19:
		return "condbr19"
	case FixupTestBr14:
		return "tstbr14"
	case FixupAdrPrelPgHi21:
		return "adr_prel_pg_hi21"
	case FixupAdrPrelLo21:
		return "adr_prel_lo21"
	case FixupAddAbsLo12Nc:
		return "add_abs_lo12_nc"
	case FixupLdSt8AbsLo12Nc:
		return "ldst8_abs_lo12_nc"
	case FixupLdSt16AbsLo12Nc:
		return "ldst16_abs_lo12_nc"
	case FixupLdSt32AbsLo12Nc:
		return "ldst32_abs_lo12_nc"
	case FixupLdSt64AbsLo12Nc:
		return "ldst64_abs_lo12_nc"
	}
	return "fixup?"
}

// Fixup is a hole in emitted code that a downstream object writer must
// resolve. Offset points at the 4-byte instruction word to patch; the writer
// inserts the resolved value into that word's relevant bit field per Kind.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}