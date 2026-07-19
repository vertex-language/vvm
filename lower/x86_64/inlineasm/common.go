package inlineasm

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/lower/x86_64/mcode"
)

// jccTable maps a jCC mnemonic's condition suffix to mcode's condition code.
var jccTable = map[string]byte{
	"o": mcode.CondO, "no": mcode.CondNO,
	"b": mcode.CondB, "c": mcode.CondB, "nae": mcode.CondB,
	"ae": mcode.CondAE, "nb": mcode.CondAE, "nc": mcode.CondAE,
	"e": mcode.CondE, "z": mcode.CondE, "ne": mcode.CondNE, "nz": mcode.CondNE,
	"be": mcode.CondBE, "na": mcode.CondBE, "a": mcode.CondA, "nbe": mcode.CondA,
	"s": mcode.CondS, "ns": mcode.CondNS,
	"l": mcode.CondL, "nge": mcode.CondL, "ge": mcode.CondGE, "nl": mcode.CondGE,
	"le": mcode.CondLE, "ng": mcode.CondLE, "g": mcode.CondG, "nle": mcode.CondG,
}

// twoOpALU is the curated set of two-operand ALU mnemonics that map
// directly onto mcode's shared op names.
var twoOpALU = map[string]bool{"add": true, "or": true, "and": true, "sub": true, "xor": true, "cmp": true}
var shiftMnem = map[string]string{"shl": "shl", "sal": "shl", "shr": "shr", "sar": "sar", "rol": "rol", "ror": "ror"}
var oneOpMcode = map[string]string{
	"not": "not", "neg": "neg", "mul": "mul1", "div": "div", "idiv": "idiv",
	"inc": "inc", "dec": "dec",
}

// LowerBlock lowers one verified asm block's code section into the shared
// mcode.Inst vocabulary isel also targets. arch is the module's target
// architecture ("x86_64"); dialect is the module-wide asmdialect;
// labelPrefix namespaces this block's asm-local labels against the
// function's own label space (asm labels are block-scoped, §4).
func LowerBlock(dialect vir.AsmDialect, arch string, code []vir.AsmCodeLine, labelPrefix string) ([]mcode.Inst, error) {
	var out []mcode.Inst
	for _, cl := range code {
		if cl.LabelDeclaration != "" {
			out = append(out, mcode.Inst{Op: "label", Lbl: labelPrefix + cl.LabelDeclaration})
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
		var base mcode.Reg
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

type operand struct {
	kind  opKind
	reg   mcode.Reg
	width int
	imm   int64
	base  mcode.Reg
	disp  int32
	label string
}

func (o operand) toOpr() mcode.Opr {
	switch o.kind {
	case opReg:
		return mcode.R(o.reg)
	case opImm:
		return mcode.Imm(o.imm)
	case opMem:
		return mcode.Mem(o.base, o.disp)
	}
	return mcode.Opr{}
}

func lowerLine(arch string, dialect vir.AsmDialect, mnemonic string, raw []vir.AsmOperand, prefix string) ([]mcode.Inst, error) {
	m := strings.ToLower(mnemonic)

	if m == "syscall" {
		return []mcode.Inst{{Op: "syscall"}}, nil
	}
	if m == "nop" {
		return []mcode.Inst{{Op: "nop"}}, nil
	}
	if m == "jmp" || strings.HasPrefix(m, "j") {
		if len(raw) != 1 || raw[0].Kind != vir.AsmOperandKindLabel {
			return nil, fmt.Errorf("asm: %q needs exactly one asm-local label operand", mnemonic)
		}
		lbl := prefix + raw[0].Label
		if m == "jmp" {
			return []mcode.Inst{{Op: "jmp", Lbl: lbl}}, nil
		}
		cc, ok := jccTable[m[1:]]
		if !ok {
			return nil, fmt.Errorf("asm: unknown jump-cc mnemonic %q", mnemonic)
		}
		return []mcode.Inst{{Op: "jcc", CC: cc, Lbl: lbl}}, nil
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
		return []mcode.Inst{{Op: "push", S: ops[0].toOpr()}}, nil
	case m == "pop":
		if len(ops) != 1 || ops[0].kind != opReg {
			return nil, fmt.Errorf("asm: pop needs one register operand")
		}
		return []mcode.Inst{{Op: "pop", D: ops[0].toOpr()}}, nil
	case oneOpMcode[m] != "":
		return oneOperandForm(oneOpMcode[m], ops)
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

func movForm(ops []operand) ([]mcode.Inst, error) {
	if len(ops) != 2 {
		return nil, fmt.Errorf("asm: mov needs two operands")
	}
	d, s := ops[0], ops[1]
	if d.kind != opReg && d.kind != opMem {
		return nil, fmt.Errorf("asm: mov destination must be a register or memory operand")
	}
	sz := opWidth(d, s)
	return []mcode.Inst{{Op: "mov", D: d.toOpr(), S: s.toOpr(), Sz: sz}}, nil
}

func leaForm(ops []operand) ([]mcode.Inst, error) {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opMem {
		return nil, fmt.Errorf("asm: lea needs dst reg, src mem")
	}
	return []mcode.Inst{{Op: "lea", D: ops[0].toOpr(), S: ops[1].toOpr(), Sz: 8}}, nil
}

func testForm(ops []operand) ([]mcode.Inst, error) {
	if len(ops) != 2 || ops[0].kind != opReg || ops[1].kind != opReg {
		return nil, fmt.Errorf("asm: test needs reg, reg (mem/imm forms TODO)")
	}
	return []mcode.Inst{{Op: "test", D: ops[0].toOpr(), S: ops[1].toOpr(), Sz: opWidth(ops...)}}, nil
}

func twoOperandForm(mnem string, ops []operand) ([]mcode.Inst, error) {
	if len(ops) != 2 {
		return nil, fmt.Errorf("asm: %s needs two operands", mnem)
	}
	d, s := ops[0], ops[1]
	if d.kind == opMem && s.kind == opImm {
		return nil, fmt.Errorf("asm: %s mem, imm not yet supported (TODO)", mnem)
	}
	return []mcode.Inst{{Op: mnem, D: d.toOpr(), S: s.toOpr(), Sz: opWidth(d, s)}}, nil
}

func twoOperandRM(ops []operand, mnem string) ([]mcode.Inst, error) {
	if ops[0].kind != opReg {
		return nil, fmt.Errorf("asm: %s destination must be a register", mnem)
	}
	return []mcode.Inst{{Op: mnem, D: ops[0].toOpr(), S: ops[1].toOpr(), Sz: opWidth(ops...)}}, nil
}

func shiftForm(mnem string, ops []operand) ([]mcode.Inst, error) {
	if len(ops) != 2 || ops[0].kind != opReg {
		return nil, fmt.Errorf("asm: %s needs dst reg, count", mnem)
	}
	s := ops[1]
	if s.kind == opReg && s.reg != mcode.RCX {
		return nil, fmt.Errorf("asm: %s by register requires CL", mnem)
	}
	return []mcode.Inst{{Op: mnem, D: ops[0].toOpr(), S: s.toOpr(), Sz: opWidth(ops[0])}}, nil
}

func oneOperandForm(mnem string, ops []operand) ([]mcode.Inst, error) {
	if len(ops) != 1 {
		return nil, fmt.Errorf("asm: %s needs exactly one operand", mnem)
	}
	return []mcode.Inst{{Op: mnem, S: ops[0].toOpr(), Sz: opWidth(ops...)}}, nil
}