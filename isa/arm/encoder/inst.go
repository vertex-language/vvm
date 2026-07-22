// encoder/inst.go
//
// Package encoder is the generic A32 (32-bit ARM) assembler: a pseudo-
// instruction stream (Inst/Opr) built entirely from physical registers,
// immediates, symbols, and plain memory operands — never a not-yet-placed
// value — plus the Encode function that turns that stream into machine
// words.
//
// It is the AArch32 sibling of isa/x86_64/encoder and keeps the same
// contract: no knowledge of any register-allocation policy or calling
// convention, reusable by anything that wants "turn this Inst stream into
// A32 words". The differences from the x86 encoders are all consequences
// of the machine — fixed 32-bit words, a condition field on every
// instruction, the rotated modified-immediate operand, and 16-bit MOVW/MOVT
// halves for building constants that don't fit one — and are called out at
// the sites where they appear.
package encoder

import isaarm "github.com/vertex-language/vvm/isa/arm"

// Reg and the register constants are the same type/values as isa/arm's,
// aliased so callers building an Inst stream don't need to import isa/arm
// directly for anything but the odd diagnostic.
type Reg = isaarm.Reg

const (
	R0  = isaarm.R0
	R1  = isaarm.R1
	R2  = isaarm.R2
	R3  = isaarm.R3
	R4  = isaarm.R4
	R5  = isaarm.R5
	R6  = isaarm.R6
	R7  = isaarm.R7
	R8  = isaarm.R8
	R9  = isaarm.R9
	R10 = isaarm.R10
	R11 = isaarm.R11
	R12 = isaarm.R12
	R13 = isaarm.R13
	R14 = isaarm.R14
	R15 = isaarm.R15

	SP    = isaarm.RSP
	LR    = isaarm.RLR
	PC    = isaarm.RPC
	RNone = isaarm.RNone
)

// Shift-type selectors, re-exported for building shifted-register operands.
const (
	LSL = isaarm.ShiftLSL
	LSR = isaarm.ShiftLSR
	ASR = isaarm.ShiftASR
	ROR = isaarm.ShiftROR
)

// Cond is a condition selector for an Inst.
//
// Unlike the x86 encoders, which re-export the ISA condition codes raw, A32
// puts a condition on *every* instruction and "always" is the common case —
// so a raw re-export would make the zero-value Inst mean EQ, a trap. This
// type instead numbers AL as zero, so an Inst built without an explicit
// condition executes unconditionally, and code() maps back to the ISA
// encoding at emit time.
type Cond byte

const (
	AL Cond = iota // always — the zero value / default
	EQ
	NE
	CS
	CC
	MI
	PL
	VS
	VC
	HI
	LS
	GE
	LT
	GT
	LE
)

// Documented synonyms — the carry flag read as an unsigned comparison.
const (
	HS = CS
	LO = CC
)

// code maps a Cond to its 4-bit ISA condition encoding.
func (c Cond) code() byte {
	if c == AL {
		return isaarm.CondAL
	}
	return byte(c) - 1
}

// condFromCode is the inverse, for Negate. Both AL (14) and the reserved
// NV (15) map to AL, since negating an unconditional instruction is
// degenerate (see Negate).
func condFromCode(code byte) Cond {
	if code >= isaarm.CondAL {
		return AL
	}
	return Cond(code + 1)
}

// Negate returns the condition testing the complementary case, for
// inverting a two-way branch. It is a single bit flip on the ISA encoding.
// Negating AL is degenerate — an unconditional branch has no condition to
// invert — and yields AL rather than the unusable NV code.
func (c Cond) Negate() Cond { return condFromCode(isaarm.NegateCond(c.code())) }

type OprKind byte

const (
	ONone   OprKind = iota
	OReg            // a register, with an optional shift (Operand2 / index)
	OImm            // immediate; Sym != "" makes it a symbol address
	OMem            // [base, offset], pre/post-indexed
	ORegList        // a set of registers, for block transfer (LDM/STM)
)

// Opr is one fully-resolved operand. There is deliberately no "unresolved
// slot" variant — that belongs one layer up, in whatever builds this
// stream.
//
// A register operand (OReg) doubles as an A32 shifter operand: with no
// shift it is a plain register; with Shift/ShiftAmt it is shifted by an
// immediate (ShiftAmt 0 with ROR meaning RRX); with Shift/ShiftReg it is
// shifted by a register. ShiftReg == RNone selects the immediate-shift
// form.
//
// A memory operand (OMem) is [Base {, offset}] where the offset is either a
// signed immediate (Index == RNone; the sign picks the U bit) or a
// register Index, optionally shifted, with Add giving U. Pre selects
// pre-indexed addressing and Wback the write-back (`!`); a post-indexed
// operand (Pre == false) always writes back architecturally.
type Opr struct {
	Kind OprKind

	Reg Reg   // OReg: the register
	Imm int64 // OImm value, or ORegList 16-bit mask
	Sym string // OImm: symbol whose address this is (built via MOVW/MOVT)

	// Shifter fields (OReg operand2, or OMem register index).
	Shift    byte // LSL/LSR/ASR/ROR
	ShiftAmt byte // 0-31 immediate shift amount
	ShiftReg Reg  // RNone => immediate/no shift

	// Memory fields (OMem).
	Base  Reg
	Index Reg   // RNone => immediate offset
	Disp  int32 // signed immediate offset (sign selects U)
	Add   bool  // U bit for a register index (true => add)
	Pre   bool  // pre-indexed
	Wback bool  // write back the base (`!`)
}

func R(r Reg) Opr     { return Opr{Kind: OReg, Reg: r, ShiftReg: RNone} }
func Imm(v int64) Opr { return Opr{Kind: OImm, Imm: v} }

// RShift is a register shifted by an immediate amount (Rm, <shift> #amt).
// ROR with amt 0 encodes RRX.
func RShift(r Reg, shift, amt byte) Opr {
	return Opr{Kind: OReg, Reg: r, Shift: shift, ShiftAmt: amt, ShiftReg: RNone}
}

// RShiftReg is a register shifted by another register (Rm, <shift> Rs). Rs
// may not be the PC.
func RShiftReg(r Reg, shift byte, rs Reg) Opr {
	return Opr{Kind: OReg, Reg: r, Shift: shift, ShiftReg: rs}
}

// SymAddr names a symbol's address, materialized by a MOVW/MOVT pair.
func SymAddr(s string) Opr { return Opr{Kind: OImm, Sym: s} }

// Mem is a pre-indexed [base, #disp] with no write-back.
func Mem(base Reg, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Pre: true}
}

// MemPre is a pre-indexed [base, #disp]{!}.
func MemPre(base Reg, disp int32, wback bool) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Pre: true, Wback: wback}
}

// MemPost is a post-indexed [base], #disp — always writes back.
func MemPost(base Reg, disp int32) Opr {
	return Opr{Kind: OMem, Base: base, Index: RNone, Disp: disp, Pre: false}
}

// MemIdx is [base, {+/-}index, <shift> #amt]{!}, pre- or post-indexed.
func MemIdx(base, index Reg, shift, amt byte, add, pre, wback bool) Opr {
	return Opr{
		Kind: OMem, Base: base, Index: index, Shift: shift, ShiftAmt: amt,
		Add: add, Pre: pre, Wback: wback,
	}
}

// RegList builds a block-transfer register set. Order is irrelevant — the
// hardware always transfers lowest-numbered register to lowest address.
func RegList(regs ...Reg) Opr {
	var mask uint16
	for _, r := range regs {
		if r.IsGPR() {
			mask |= 1 << r.Field()
		}
	}
	return Opr{Kind: ORegList, Imm: int64(mask)}
}

// Inst is one pre-encoding pseudo instruction. encode.go's Encode switch is
// the authoritative list of Op spellings and of which fields each reads;
// the summary below is a map, not the territory. Every Op carries the CC
// condition (default AL) and, where meaningful, the S set-flags bit.
//
//	Op                        reads              notes
//	--------------------------------------------------------------------
//	label                     Lbl                defines a local branch target
//	and/eor/sub/rsb/add/
//	 adc/sbc/rsc/orr/bic       D, N, M            Rd := Rn op Op2; M is a shifter operand
//	mov/mvn                    D, M               Rd := Op2 (Rn unused); mov #imm may
//	                                              auto-promote to movw
//	cmp/cmn/tst/teq            N, M               flags only; S forced, no Rd
//	movw/movt                  D, Imm|Sym         16-bit immediate / reloc half
//	mul                        D, M, A            Rd := Rm*Rs   (M=Rm, A=Rs)
//	mla                        D, M, A, N         Rd := Rm*Rs+Rn
//	umull/umlal/
//	 smull/smlal               D, N, M, A         RdLo=D, RdHi=N, Rm=M, Rs=A
//	ldr/str/ldrb/strb          D, M               M is an OMem operand
//	ldrh/strh/ldrsb/ldrsh      D, M               8-bit split offset; SB/SH are load-only
//	push/pop                   M                  M is a register list; SP, write-back implied
//	ldmia/ldmib/ldmda/ldmdb/
//	 stmia/stmib/stmda/stmdb   N, M, Wb           base=N, list=M
//	b/bl                       Lbl or Sym, CC     rel24; label patched, symbol relocated
//	bx/blx                     M                  M is a register
//	clz                        D, M               Rd := clz(Rm)
//	svc                        Imm                the 24-bit comment field
//	nop/ud                     —
type Inst struct {
	Op  string
	CC  Cond // condition; zero value AL (always)
	S   bool // set-flags (the S bit) for data-processing / multiply
	Wb  bool // write-back for block transfer (`!`)
	D   Opr  // Rd / RdLo
	N   Opr  // Rn / RdHi / base
	M   Opr  // Op2 / Rm / memory operand / register list
	A   Opr  // Rs (multiplies) / — 
	Lbl string
	Sym string
	Imm int64
}

// FixupKind is this backend's vocabulary for the relocation holes Encode
// leaves. All A32 relocations are word-internal bit-field patches, not the
// byte fields x86 uses.
type FixupKind int

const (
	// FixupPCRel24: the B/BL word-offset field := ((S + A) - P) >> 2,
	// masked to 24 bits, where P is the instruction's own address. A is
	// -8 by default (the PC reads as instr+8).
	FixupPCRel24 FixupKind = iota
	// FixupMovwAbs: the low 16 bits of (S + A) into the imm4:imm12 field
	// of a MOVW.
	FixupMovwAbs
	// FixupMovtAbs: the high 16 bits of (S + A) into the imm4:imm12 field
	// of a MOVT.
	FixupMovtAbs
)

func (k FixupKind) String() string {
	switch k {
	case FixupPCRel24:
		return "pcrel24"
	case FixupMovwAbs:
		return "movw_abs"
	case FixupMovtAbs:
		return "movt_abs"
	}
	return "fixup?"
}

// Fixup is a hole in emitted code that a downstream object writer must
// resolve. Offset points at the 4-byte instruction word to patch; the
// writer inserts the resolved value into that word's relevant bit field
// per Kind.
type Fixup struct {
	Offset uint32
	Symbol string
	Kind   FixupKind
	Addend int64
}