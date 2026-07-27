package aarch64

// ---------------------------------------------------------------------------
// Barrel-shift types (shifted-register operand).
// ---------------------------------------------------------------------------
//
// A shifted-register operand carries a 2-bit shift type. The four values are
// facts; which are legal depends on the format, and that restriction is
// itself a fact worth recording:
//
//   - Logical (shifted register) allows all four (LSL/LSR/ASR/ROR).
//   - Add/sub (shifted register) allows only LSL/LSR/ASR; ROR (0b11) is
//     UNDEFINED there.
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
}

// ParseShift resolves a shift mnemonic to its 2-bit type.
func ParseShift(s string) (byte, bool) {
	t, ok := shiftByName[s]
	return t, ok
}

// ShiftAllowsROR reports whether ROR is a legal shift type for a shifted-
// register operand in the given family. It is legal for logical operations
// and UNDEFINED for add/sub.
func ShiftAllowsROR(logical bool) bool { return logical }

// ---------------------------------------------------------------------------
// Add / subtract op and S bits.
// ---------------------------------------------------------------------------
//
// The immediate, shifted-register, and extended-register add/sub forms share
// two selector bits: op (bit 30) picks add vs sub, and S (bit 29) picks
// flag-setting. The four (op,S) combinations name add/adds/sub/subs; the
// flag-setting forms are what CMN/CMP and NEG/NEGS alias with a zero
// register in Rd or Rn. The operand *form* differs by opcode base (below);
// these bits are shared across all of them.
type AddSubOp struct {
	Name string
	Op   byte // bit 30: 0 add, 1 sub
	S    byte // bit 29: 0 plain, 1 set flags
}

var AddSubOps = []AddSubOp{
	{"add", 0, 0},
	{"adds", 0, 1},
	{"sub", 1, 0},
	{"subs", 1, 1},
}

// Fixed opcode bases for the three add/sub operand forms, given as the whole
// 32-bit word with sf, op, S, and the operands still zero. OR in the sf bit,
// op<<30, S<<29, the registers, and the form-specific immediate/shift.
const (
	AddSubImmBase      uint32 = 0x11000000 // sf 0 0 100010 sh imm12 Rn Rd
	AddSubShiftedBase  uint32 = 0x0B000000 // sf 0 0 01011 shift 0 Rm imm6 Rn Rd
	AddSubExtendedBase uint32 = 0x0B200000 // sf 0 0 01011 001 Rm option imm3 Rn Rd
)

var (
	addSubByName = map[string]AddSubOp{}
)

func init() {
	for _, a := range AddSubOps {
		addSubByName[a.Name] = a
	}
}

// AddSubByName resolves add/adds/sub/subs to its (op,S) bits.
func AddSubByName(name string) (AddSubOp, bool) { a, ok := addSubByName[name]; return a, ok }

// ---------------------------------------------------------------------------
// Logical group.
// ---------------------------------------------------------------------------
//
// The logical operations select on a 2-bit opc (bits 30:29) and, in the
// shifted-register form, an N bit (bit 21) that inverts the second operand.
// The immediate form has no N-as-invert (its bit 22 N is part of the bitmask
// element-size encoding), so the negated variants (bic/orn/eon/bics) exist
// only as shifted-register instructions — which is exactly the kind of
// per-form irregularity worth tabulating rather than just listing names.
type LogicalOp struct {
	Name string
	Opc  byte // bits 30:29
	// Negate is the N bit (bit 21) in the shifted-register form: true for
	// the bic/orn/eon/bics variants that invert the second operand.
	Negate bool
	// SetsFlags is true for ands/bics (opc 11), which update NZCV.
	SetsFlags bool
	// HasImmForm is false for the negated variants, which the immediate
	// encoding cannot express.
	HasImmForm bool
}

// LogicalOps lists the eight shifted-register logical operations. The four
// non-negated ones (and/orr/eor/ands) also have immediate forms.
var LogicalOps = []LogicalOp{
	{"and", 0, false, false, true},
	{"bic", 0, true, false, false},
	{"orr", 1, false, false, true},
	{"orn", 1, true, false, false},
	{"eor", 2, false, false, true},
	{"eon", 2, true, false, false},
	{"ands", 3, false, true, true},
	{"bics", 3, true, true, false},
}

const (
	LogicalImmBase     uint32 = 0x12000000 // sf opc 100100 N immr imms Rn Rd
	LogicalShiftedBase uint32 = 0x0A000000 // sf opc 01010 shift N Rm imm6 Rn Rd
)

var logicalByName = map[string]LogicalOp{}

func init() {
	for _, l := range LogicalOps {
		logicalByName[l.Name] = l
	}
}

// LogicalByName resolves a logical mnemonic to its opc/N/flags facts.
func LogicalByName(name string) (LogicalOp, bool) { l, ok := logicalByName[name]; return l, ok }

// ---------------------------------------------------------------------------
// Move-wide group.
// ---------------------------------------------------------------------------
//
// Move-wide selects on a 2-bit opc (bits 30:29): MOVN 00, MOVZ 10, MOVK 11.
// opc 01 is unallocated. MOVN writes the inverse of the shifted immediate,
// MOVZ writes it against a zeroed register, MOVK keeps the other halfwords.
type MoveWideOp struct {
	Name string
	Opc  byte // bits 30:29
}

var MoveWideOps = []MoveWideOp{
	{"movn", 0},
	{"movz", 2},
	{"movk", 3},
}

// MoveWideBase is the fixed word for the group: sf opc 100101 hw imm16 Rd.
const MoveWideBase uint32 = 0x12800000

var moveWideByName = map[string]MoveWideOp{}

func init() {
	for _, m := range MoveWideOps {
		moveWideByName[m.Name] = m
	}
}

// MoveWideByName resolves movn/movz/movk to its opc.
func MoveWideByName(name string) (MoveWideOp, bool) { m, ok := moveWideByName[name]; return m, ok }

// ---------------------------------------------------------------------------
// Data-processing (2 source): variable shifts and divides.
// ---------------------------------------------------------------------------
//
// These select on a 6-bit opcode field (bits 15:10) in a shared format
// (sf 0 0 11010110 Rm opcode Rn Rd). The variable-shift operations
// (lslv/lsrv/asrv/rorv) are the register-amount analog of the shift-type
// field above; udiv/sdiv are the only integer divides.
type DataProc2Op struct {
	Name   string
	Opcode byte // bits 15:10
}

var DataProc2Ops = []DataProc2Op{
	{"udiv", 0x02},
	{"sdiv", 0x03},
	{"lslv", 0x08},
	{"lsrv", 0x09},
	{"asrv", 0x0A},
	{"rorv", 0x0B},
}

// DataProc2Base is the shared word: sf 0 0 11010110 Rm opcode Rn Rd.
const DataProc2Base uint32 = 0x1AC00000

var dataProc2ByName = map[string]DataProc2Op{}

func init() {
	for _, d := range DataProc2Ops {
		dataProc2ByName[d.Name] = d
	}
}

// DataProc2ByName resolves a 2-source data-processing mnemonic to its
// opcode.
func DataProc2ByName(name string) (DataProc2Op, bool) { d, ok := dataProc2ByName[name]; return d, ok }