package x86

// AluOp describes one two-operand ALU instruction's three opcode forms:
// MR (r/m, r), RM (r, r/m), and its /ext digit under the 0x81 r/m,imm32
// group.
type AluOp struct {
	Name string
	MR   byte
	RM   byte
	Ext  byte
}

var AluOps = []AluOp{
	{"add", 0x01, 0x03, 0},
	{"or", 0x09, 0x0B, 1},
	{"and", 0x21, 0x23, 4},
	{"sub", 0x29, 0x2B, 5},
	{"xor", 0x31, 0x33, 6},
	{"cmp", 0x39, 0x3B, 7},
}

var (
	aluByName = map[string]AluOp{}
	aluByMR   = map[byte]AluOp{}
	aluByRM   = map[byte]AluOp{}
	aluByExt  = map[byte]AluOp{}
)

func init() {
	for _, a := range AluOps {
		aluByName[a.Name] = a
		aluByMR[a.MR] = a
		aluByRM[a.RM] = a
		aluByExt[a.Ext] = a
	}
}

func AluByName(name string) (AluOp, bool) { a, ok := aluByName[name]; return a, ok }
func AluByMR(op byte) (AluOp, bool)       { a, ok := aluByMR[op]; return a, ok }
func AluByRM(op byte) (AluOp, bool)       { a, ok := aluByRM[op]; return a, ok }
func AluByExt(ext byte) (AluOp, bool)     { a, ok := aluByExt[ext]; return a, ok }

// ShiftOp describes one shift/rotate instruction's /ext digit under the
// 0xC0/0xC1 (imm8) and 0xD2/0xD3 (cl) groups.
type ShiftOp struct {
	Name string
	Ext  byte
}

var ShiftOps = []ShiftOp{
	{"rol", 0}, {"ror", 1}, {"shl", 4}, {"shr", 5}, {"sar", 7},
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

// Group3Op describes one single-operand instruction's /ext digit under the
// 0xF7 group ("group 3" in Intel's manual). Names are the real assembly
// mnemonics ("mul"/"imul") — lower/x86/mcode uses "mul32"/"imul32"
// internally to disambiguate this one-operand EDX:EAX-writing form from
// the unrelated two/three-operand 0F AF/0x69 "imul" it also emits; that
// disambiguation is mcode's own routing choice, translated at its call
// site rather than duplicated into this table.
type Group3Op struct {
	Name string
	Ext  byte
}

var Group3Ops = []Group3Op{
	{"not", 2}, {"neg", 3}, {"mul", 4}, {"imul", 5}, {"div", 6}, {"idiv", 7},
}

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