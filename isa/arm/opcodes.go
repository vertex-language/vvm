package arm

// ---------------------------------------------------------------------------
// Data-processing group.
// ---------------------------------------------------------------------------
//
// The sixteen data-processing operations share one encoding family
// (Cond 00 I opcode S Rn Rd Operand2); the operation is selected by the
// 4-bit opcode in bits 24:21. The group is *not* homogeneous in how its
// operands are used, and — as with isa/x86's Group3Op — that irregularity
// is the point of tabulating it rather than just listing names:
//
//   - The full arithmetic/logical ops (and, eor, sub, rsb, add, adc, sbc,
//     rsc, orr, bic) compute Rd := Rn <op> Operand2.
//   - The move ops (mov, mvn) ignore Rn entirely: Rd := Operand2 (or its
//     bitwise NOT). Rn should be encoded zero.
//   - The test ops (tst, teq, cmp, cmn) write no Rd; they exist only to set
//     flags, and so always have the S bit set. An assembler forces S for
//     these even when the mnemonic omits it, and Rd should be encoded zero.
type DataProcOp struct {
	Name   string
	Opcode byte // bits 24:21
	// WritesRd is false for the four test-only ops.
	WritesRd bool
	// UsesRn is false for the two move ops.
	UsesRn bool
	// ForcesS marks the test-only ops, which must always set the S bit.
	ForcesS bool
}

// DataProcOps is the complete sixteen-entry group in opcode order.
var DataProcOps = []DataProcOp{
	{"and", 0, true, true, false},
	{"eor", 1, true, true, false},
	{"sub", 2, true, true, false},
	{"rsb", 3, true, true, false},
	{"add", 4, true, true, false},
	{"adc", 5, true, true, false},
	{"sbc", 6, true, true, false},
	{"rsc", 7, true, true, false},
	{"tst", 8, false, true, true},
	{"teq", 9, false, true, true},
	{"cmp", 10, false, true, true},
	{"cmn", 11, false, true, true},
	{"orr", 12, true, true, false},
	{"mov", 13, true, false, false},
	{"bic", 14, true, true, false},
	{"mvn", 15, true, false, false},
}

var (
	dpByName   = map[string]DataProcOp{}
	dpByOpcode = map[byte]DataProcOp{}
)

func init() {
	for _, d := range DataProcOps {
		dpByName[d.Name] = d
		dpByOpcode[d.Opcode] = d
	}
	if len(dpByOpcode) != 16 {
		panic("isa/arm: DataProcOps does not cover all sixteen opcodes")
	}
}

func DataProcByName(name string) (DataProcOp, bool) { d, ok := dpByName[name]; return d, ok }
func DataProcByOpcode(op byte) (DataProcOp, bool)   { d, ok := dpByOpcode[op]; return d, ok }

// ---------------------------------------------------------------------------
// Barrel-shifter shift types.
// ---------------------------------------------------------------------------
//
// When Operand2 is a register, the barrel shifter applies one of four shift
// types, selected by a 2-bit field. The type is part of the operand
// encoding, not a separate opcode.
const (
	ShiftLSL byte = 0 // logical left
	ShiftLSR byte = 1 // logical right
	ShiftASR byte = 2 // arithmetic right
	ShiftROR byte = 3 // rotate right
)

var shiftName = [4]string{"lsl", "lsr", "asr", "ror"}

// ShiftName returns the canonical mnemonic for a 2-bit shift type; "?" for
// out of range.
func ShiftName(t byte) string {
	if int(t) >= len(shiftName) {
		return "?"
	}
	return shiftName[t]
}

var shiftByName = map[string]byte{
	"lsl": ShiftLSL,
	"lsr": ShiftLSR,
	"asr": ShiftASR,
	"ror": ShiftROR,
	"asl": ShiftLSL, // documented synonym: ASL assembles identically to LSL
}

// ParseShift resolves a shift mnemonic. The four canonical names plus the
// asl->lsl synonym are accepted. Note two things ParseShift deliberately
// does *not* handle, because they are not shift *types* but immediate-form
// special cases of ROR that live in the shift-amount field:
//
//   - RRX (rotate right with extend) is encoded as ROR with a shift amount
//     of zero.
//   - LSR #0, ASR #0, and ROR #0 do not exist as written; the assembler
//     rewrites LSR #0 and ASR #0 to LSL #0, and the "amount 0" encodings of
//     LSR/ASR are reused to mean shift-by-32.
func ParseShift(s string) (byte, bool) {
	t, ok := shiftByName[s]
	return t, ok
}

// ---------------------------------------------------------------------------
// Single data transfer (LDR/STR) flag bits.
// ---------------------------------------------------------------------------
//
// The load/store encoding (Cond 01 I P U B W L Rn Rd Offset) carries five
// one-bit modifiers. Their values are facts; *which* combination a given
// addressing mode needs is encoder work.
const (
	LSBitL byte = 1 << 0 // 1 = load, 0 = store
	LSBitW byte = 1 << 1 // 1 = write back the (modified) base
	LSBitB byte = 1 << 2 // 1 = byte transfer, 0 = word
	LSBitU byte = 1 << 3 // 1 = add offset to base (up), 0 = subtract (down)
	LSBitP byte = 1 << 4 // 1 = pre-indexed, 0 = post-indexed
)

// ---------------------------------------------------------------------------
// Block data transfer (LDM/STM) addressing modes.
// ---------------------------------------------------------------------------
//
// A block transfer's addressing is fixed by the P (pre/post) and U (up/down)
// bits together with L (load/store). The architecture documents two naming
// vocabularies for the same bit patterns: a stack-oriented one (full/empty,
// ascending/descending) and a generic one (increment/decrement,
// before/after). They are the same four (P,U) encodings viewed two ways —
// the stack names' meaning depends on whether you are loading or storing,
// which is exactly why both vocabularies exist. This mirrors the
// condition-code synonyms: one set of encodings, two textual spellings.
type BlockMode struct {
	Generic string // IA, IB, DA, DB
	P       bool   // pre-indexed (before)
	U       bool   // up (increment)
	// StackLoad / StackStore are the full/empty stack spellings that map to
	// this (P,U) under LDM and STM respectively.
	StackLoad  string
	StackStore string
}

// BlockModes lists the four generic addressing modes with their stack
// aliases. Under load, IA=FD, IB=ED, DA=FA, DB=EA; under store the pairing
// flips (IA=EA, IB=FA, DA=ED, DB=FD), which is captured per-column below.
var BlockModes = []BlockMode{
	{"ia", false, true, "fd", "ea"},  // increment after
	{"ib", true, true, "ed", "fa"},   // increment before
	{"da", false, false, "fa", "ed"}, // decrement after
	{"db", true, false, "ea", "fd"},  // decrement before
}

var blockByGeneric = map[string]BlockMode{}

func init() {
	for _, m := range BlockModes {
		blockByGeneric[m.Generic] = m
	}
}

// BlockModeByGeneric resolves a generic addressing-mode name (ia/ib/da/db).
// The stack spellings (fd/ed/fa/ea) are load/store-dependent and so are not
// resolved here without knowing the direction; a caller with that context
// can scan BlockModes' StackLoad/StackStore columns.
func BlockModeByGeneric(name string) (BlockMode, bool) {
	m, ok := blockByGeneric[name]
	return m, ok
}