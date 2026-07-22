package x86

// ---------------------------------------------------------------------------
// Two-operand ALU group.
// ---------------------------------------------------------------------------

// Immediate-form opcode bytes shared by the whole ALU group; which
// operation applies comes from AluOp.Ext in the ModRM.reg field.
const (
	AluImm32 byte = 0x81 // r/m32, imm32
	AluImm8  byte = 0x83 // r/m32, imm8 sign-extended — three bytes shorter,
	// and exactly equivalent whenever FitsImm8 holds
	AluImm8B byte = 0x80 // r/m8, imm8
)

// AluOp describes one two-operand ALU instruction's opcode forms: MR
// (r/m, r), RM (r, r/m), the accumulator short form (eax, imm32), and its
// /ext digit under the AluImm32/AluImm8 groups.
type AluOp struct {
	Name string
	MR   byte
	RM   byte
	Acc  byte // eax, imm32 — one byte shorter than the AluImm32 group form
	Ext  byte
}

// AluOps is the complete eight-entry group, in /ext order. adc and sbb
// are here because IA-32 has them and a disassembler must be able to name
// them — not because any lowering in this repository emits them. That
// asymmetry is the point of the package: these are facts about the
// machine, not a menu of what the compiler happens to use.
var AluOps = []AluOp{
	{"add", 0x01, 0x03, 0x05, 0},
	{"or", 0x09, 0x0B, 0x0D, 1},
	{"adc", 0x11, 0x13, 0x15, 2},
	{"sbb", 0x19, 0x1B, 0x1D, 3},
	{"and", 0x21, 0x23, 0x25, 4},
	{"sub", 0x29, 0x2B, 0x2D, 5},
	{"xor", 0x31, 0x33, 0x35, 6},
	{"cmp", 0x39, 0x3B, 0x3D, 7},
}

var (
	aluByName = map[string]AluOp{}
	aluByMR   = map[byte]AluOp{}
	aluByRM   = map[byte]AluOp{}
	aluByAcc  = map[byte]AluOp{}
	aluByExt  = map[byte]AluOp{}
)

func init() {
	for _, a := range AluOps {
		aluByName[a.Name] = a
		aluByMR[a.MR] = a
		aluByRM[a.RM] = a
		aluByAcc[a.Acc] = a
		aluByExt[a.Ext] = a
	}
	if len(aluByExt) != 8 {
		panic("isa/x86: AluOps does not cover all eight /ext digits")
	}
}

func AluByName(name string) (AluOp, bool) { a, ok := aluByName[name]; return a, ok }
func AluByMR(op byte) (AluOp, bool)       { a, ok := aluByMR[op]; return a, ok }
func AluByRM(op byte) (AluOp, bool)       { a, ok := aluByRM[op]; return a, ok }
func AluByAcc(op byte) (AluOp, bool)      { a, ok := aluByAcc[op]; return a, ok }
func AluByExt(ext byte) (AluOp, bool)     { a, ok := aluByExt[ext]; return a, ok }

// ---------------------------------------------------------------------------
// Shift/rotate group ("group 2").
// ---------------------------------------------------------------------------

// Opcode bytes for the shift group. The count comes from an immediate
// byte (ShiftImm8), from CL (ShiftCL), or is implicitly 1 (ShiftOne,
// which is one byte shorter than the imm8 form).
const (
	ShiftImm8  byte = 0xC1 // r/m32, imm8
	ShiftImm8B byte = 0xC0 // r/m8, imm8
	ShiftCL    byte = 0xD3 // r/m32, cl
	ShiftCLB   byte = 0xD2 // r/m8, cl
	ShiftOne   byte = 0xD1 // r/m32, 1
	ShiftOneB  byte = 0xD0 // r/m8, 1
)

// ShiftOp describes one shift/rotate instruction's /ext digit, shared by
// all six opcode bytes above.
type ShiftOp struct {
	Name string
	Ext  byte
}

// ShiftOps covers the seven mapped members of group 2. /ext 6 is
// deliberately absent: it is unmapped on IA-32, and while most
// implementations execute it as an undocumented alias of shl, that isn't
// an architectural fact and doesn't get a name here.
var ShiftOps = []ShiftOp{
	{"rol", 0}, {"ror", 1}, {"rcl", 2}, {"rcr", 3},
	{"shl", 4}, {"shr", 5}, {"sar", 7},
}

var (
	shiftByName = map[string]ShiftOp{}
	shiftByExt  = map[byte]ShiftOp{}
)

func init() {
	for _, s := range ShiftOps {
		shiftByName[s.Name] = s
		shiftByExt[s.Ext] = s
	}
}

func ShiftByName(name string) (ShiftOp, bool) { s, ok := shiftByName[name]; return s, ok }
func ShiftByExt(ext byte) (ShiftOp, bool)     { s, ok := shiftByExt[ext]; return s, ok }

// ---------------------------------------------------------------------------
// Single-r/m-operand group ("group 3").
// ---------------------------------------------------------------------------

const (
	Group3     byte = 0xF7 // r/m32
	Group3Byte byte = 0xF6 // r/m8
)

// Group3Op describes one instruction's /ext digit under the 0xF6/0xF7
// group ("group 3" in Intel's manual).
//
// The group is not homogeneous in how its operand is *used*: not and neg
// read-modify-write their r/m operand; mul/imul/div/idiv read it and
// write the implicit EDX:EAX pair; test reads it against a trailing
// immediate and writes only flags. What every member shares — and the
// only thing this table claims — is the encoding shape: one r/m operand,
// no reg operand, the mnemonic selected by ModRM.reg.
type Group3Op struct {
	Name string
	Ext  byte
	// HasImm marks the single member carrying a trailing immediate.
	// Encoders must not emit one for the others and decoders must not
	// consume one, so the distinction can't live in a caller's memory.
	HasImm bool
}

// Group3Ops lists the seven named members. /ext 1 is an undocumented
// alias of /ext 0 (test) and is not named, for the same reason group 2's
// /ext 6 isn't.
var Group3Ops = []Group3Op{
	{"test", 0, true},
	{"not", 2, false},
	{"neg", 3, false},
	{"mul", 4, false},
	{"imul", 5, false},
	{"div", 6, false},
	{"idiv", 7, false},
}

// The two multiply forms that are *not* in group 3, listed here because
// IA-32 spells all three "imul" and the ambiguity is a property of the
// machine that every consumer has to resolve somehow:
//
//	imul r/m32            0xF7 /5   — widening, EAX * r/m -> EDX:EAX
//	imul r32, r/m32       0x0F 0xAF — same-width, two-operand
//	imul r32, r/m32, imm  0x69/0x6B — same-width, three-operand
//
// isa/x86 resolves it by arity at the call site rather than by inventing
// spellings: Group3ByName("imul") is unambiguously the one-operand form,
// because the group-3 table only has one-operand members in it.
const (
	Imul2Esc   byte = 0x0F
	Imul2Op    byte = 0xAF
	Imul3Imm32 byte = 0x69
	Imul3Imm8  byte = 0x6B
)

var (
	grp3ByName = map[string]Group3Op{}
	grp3ByExt  = map[byte]Group3Op{}
)

func init() {
	for _, g := range Group3Ops {
		grp3ByName[g.Name] = g
		grp3ByExt[g.Ext] = g
	}
}

func Group3ByName(name string) (Group3Op, bool) { g, ok := grp3ByName[name]; return g, ok }
func Group3ByExt(ext byte) (Group3Op, bool)     { g, ok := grp3ByExt[ext]; return g, ok }