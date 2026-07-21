package x86_64

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	isax86_64 "github.com/vertex-language/vvm/isa/x86_64"
)

// twoOpALU is the curated set of two-operand ALU mnemonics that map
// directly onto this package's shared Inst.Op names.
var twoOpALU = map[string]bool{"add": true, "or": true, "and": true, "sub": true, "xor": true, "cmp": true}
var shiftMnem = map[string]string{"shl": "shl", "sal": "shl", "shr": "shr", "sar": "sar", "rol": "rol", "ror": "ror"}
var oneOpForm = map[string]string{
	"not": "not", "neg": "neg", "mul": "mul1", "div": "div", "idiv": "idiv",
	"inc": "inc", "dec": "dec",
}

// LowerBlock lowers one verified asm block's code section into this
// package's shared Inst vocabulary — the same one isel.go targets. arch is
// the module's target architecture ("x86_64"); dialect is the module-wide
// asmdialect; labelPrefix namespaces this block's asm-local labels against
// the function's own label space (asm labels are block-scoped, §4).
func LowerBlock(dialect vir.AsmDialect, arch string, code []vir.AsmCodeLine, labelPrefix string) ([]Inst, error) {
	var out []Inst
	for _, cl := range code {
		if cl.LabelDeclaration != "" {
			out = append(out, Inst{Op: "label", Lbl: labelPrefix + cl.LabelDeclaration})
			continue
		}
		ops, err := canonicalOperands(dialect, cl.Mnemonic, cl.Operands)
		if err != nil {
			return nil, err
		}
		insts, err := lowerLine(arch, dialect, cl.Mnemonic, ops, labelPrefix)
		if err != nil {
			return nil, err
		}
		out = append(out, insts...)
	}
	return out, nil
}

// canonicalOperands reorders AT&T's src-first operands to canonical
// dst,src order; Intel is already dst-first and passes through unchanged.
// attReorder itself lives in att.go, alongside the rest of AT&T-specific
// parsing.
func canonicalOperands(dialect vir.AsmDialect, mnemonic string, ops []vir.AsmOperand) ([]vir.AsmOperand, error) {
	switch dialect {
	case vir.DialectIntel:
		return ops, nil
	case vir.DialectATT:
		return attReorder(mnemonic, ops), nil
	default:
		return nil, fmt.Errorf("asm: dialect %q not supported on x86_64", dialect)
	}
}

func literalInt(o vir.Operand) (int64, error) {
	switch o.Kind {
	case vir.OperandInt:
		return o.Int, nil
	case vir.OperandBool:
		if o.Bool {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("asm: immediate must be an integer literal (symbolic immediates TODO)")
}

func resolveOperand(arch string, dialect vir.AsmDialect, op vir.AsmOperand) (operand, error) {
	switch op.Kind {
	case vir.AsmOperandKindRegister:
		r, w, ok := Register(arch, op.Register)
		if !ok {
			return operand{}, fmt.Errorf("asm: unknown register %q", op.Register)
		}
		return operand{kind: opReg, reg: r, width: w}, nil
	case vir.AsmOperandKindImmediate:
		v, err := literalInt(op.Immediate)
		if err != nil {
			return operand{}, err
		}
		return operand{kind: opImm, imm: v}, nil
	case vir.AsmOperandKindMemory:
		var base isax86_64.Reg
		var disp int32
		var err error
		switch dialect {
		case vir.DialectIntel:
			base, disp, err = parseIntelMem(op.Memory, arch)
		case vir.DialectATT:
			base, disp, err = parseATTMem(op.Memory, arch)
		default:
			err = fmt.Errorf("asm: dialect %q not supported on x86_64", dialect)
		}
		if err != nil {
			return operand{}, err
		}
		return operand{kind: opMem, base: base, disp: disp}, nil
	case vir.AsmOperandKindLabel:
		return operand{kind: opLabel, label: op.Label}, nil
	}
	return operand{}, fmt.Errorf("asm: unknown operand kind %d", op.Kind)
}

type opKind int

const (
	opReg opKind = iota
	opImm
	opMem
	opLabel
)

// operand is this package's own intermediate shape for a resolved asm
// operand — reg/base hold isa/x86_64.Reg values directly, named via
// isax86_64.RAX etc. at every construction site (resolveOperand,
// parseIntelMem, parseATTMem), never re-declared under a local name.
type operand struct {
	kind  opKind
	reg   isax86_64.Reg
	width int
	imm   int64
	base  isax86_64.Reg
	disp  int32
	label string
}

func (o operand) toOpr() Opr {
	switch o.kind {
	case opReg:
		return R(o.reg)
	case opImm:
		return Imm(o.imm)
	case opMem:
		return Mem(o.base, o.disp)
	}
	return Opr{}
}

func lowerLine(arch string, dialect vir.AsmDialect, mnemonic string, raw []vir.AsmOperand, prefix string) ([]Inst, error) {
	m := strings.ToLower(mnemonic)

	if m == "syscall" {
		return []Inst{{Op: "syscall"}}, nil
	}
	if m == "nop" {
		return []Inst{{Op: "nop"}}, nil
	}
	if m == "jmp" || strings.HasPrefix(m, "j") {
		if len(raw) != 1 || raw[0].Kind != vir.AsmOperandKindLabel {
			return nil, fmt.Errorf("asm: %q needs exactly one asm-local label operand", mnemonic)
		}
		lbl := prefix + raw[0].Label
		if m == "jmp" {
			return []Inst{{Op: "jmp", Lbl: lbl}}, nil
		}
		cc, ok := isax86_64.CondMnemonics[m[1:]]
		if !ok {
			return nil, fmt.Errorf("asm: unknown jump-cc mnemonic %q", mnemonic)
		}
		return []Inst{{Op: "jcc", CC: cc, Lbl: lbl}}, nil
	}

	ops := make([]operand, len(raw))
	for i, r := range raw {
		o, err := resolveOperand(arch, dialect, r)
		if err != nil {
			return nil, err
		}
		ops[i] = o
	}

	switch {
	case m == "mov":
		return movForm(ops)
	case m == "lea":
		return leaForm(ops)
	case m == "test":
		return testForm(ops)
	case m == "imul" && len(ops) == 2:
		return twoOperandRM(ops, "imul")
	case twoOpALU[m]:
		return twoOperandForm(m, ops)
	case shiftMnem[m] != "":
		return shiftForm(shiftMnem[m], ops)
	case m == "push":
		if len(ops) != 1 || ops[0].kind != opReg {
			return nil, fmt.Errorf("asm: push needs one register operand")
		}
		return []Inst{{Op: "push", S: ops[0].toOpr()}}, nil
	case m == "pop":
		if len(ops) != 1 || ops[0].kind != opReg {
			return nil, fmt.Errorf("asm: pop needs one register operand")
		}
		return []Inst{{Op: "pop", D: ops[0].toOpr()}}, nil
	case oneOpForm[m] != "":
		return oneOperandForm(oneOpForm[m], ops)
	default:
		return nil, fmt.Errorf("asm: mnemonic %q not in the curated x86_64 set (TODO)", mnemonic)
	}
}

func opWidth(ops ...operand) int {
	for _, o := range ops {
		if o.kind == opReg {
			return widthBytes(o.width)
		}
	}
	return 4
}

func movForm(ops []operand) ([]Inst, error) {
	if len(ops) != 2 {
		return nil, fmt.Errorf("asm: mov needs two operands")
	}
	d, s := ops[0], ops[1]
	if d.kind != opReg && d.kind != opMem {
		return nil, fmt.Errorf("asm: mov destination must be a register or memory operand")
	}
	sz := opWidth(d, s)
	return []Inst{{Op: "mov", D: d.toOpr(), S: s.toOpr(), Sz: sz}}, nil
}

func leaForm(ops []operand) ([]Inst, error) {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opMem {
		return nil, fmt.Errorf("asm: lea needs dst reg, src mem")
	}
	return []Inst{{Op: "lea", D: ops[0].toOpr(), S: ops[1].toOpr(), Sz: 8}}, nil
}

func testForm(ops []operand) ([]Inst, error) {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opReg {
		return nil, fmt.Errorf("asm: test needs reg, reg (mem/imm forms TODO)")
	}
	return []Inst{{Op: "test", D: ops[0].toOpr(), S: ops[1].toOpr(), Sz: opWidth(ops...)}}, nil
}

func twoOperandForm(mnem string, ops []operand) ([]Inst, error) {
	if len(ops) != 2 {
		return nil, fmt.Errorf("asm: %s needs two operands", mnem)
	}
	d, s := ops[0], ops[1]
	if d.kind == opMem && s.kind == opImm {
		return nil, fmt.Errorf("asm: %s mem, imm not yet supported (TODO)", mnem)
	}
	return []Inst{{Op: mnem, D: d.toOpr(), S: s.toOpr(), Sz: opWidth(d, s)}}, nil
}

func twoOperandRM(ops []operand, mnem string) ([]Inst, error) {
	if ops[0].kind != opReg {
		return nil, fmt.Errorf("asm: %s destination must be a register", mnem)
	}
	return []Inst{{Op: mnem, D: ops[0].toOpr(), S: ops[1].toOpr(), Sz: opWidth(ops...)}}, nil
}

func shiftForm(mnem string, ops []operand) ([]Inst, error) {
	if len(ops) != 2 || ops[0].kind != opReg {
		return nil, fmt.Errorf("asm: %s needs dst reg, count", mnem)
	}
	s := ops[1]
	if s.kind == opReg && s.reg != isax86_64.RCX {
		return nil, fmt.Errorf("asm: %s by register requires CL", mnem)
	}
	return []Inst{{Op: mnem, D: ops[0].toOpr(), S: s.toOpr(), Sz: opWidth(ops[0])}}, nil
}

func oneOperandForm(mnem string, ops []operand) ([]Inst, error) {
	if len(ops) != 1 {
		return nil, fmt.Errorf("asm: %s needs exactly one operand", mnem)
	}
	return []Inst{{Op: mnem, S: ops[0].toOpr(), Sz: opWidth(ops...)}}, nil
}