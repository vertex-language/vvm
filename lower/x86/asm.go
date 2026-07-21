package x86

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	isax86 "github.com/vertex-language/vvm/isa/x86"
)

type SymbolResolver interface {
	Resolve(ident string) (Opr, error)
}

type asmDialect interface {
	Register(name string) (r isax86.Reg, widthBits int, ok bool)
	Lower(line vir.AsmCodeLine, label func(string) string) ([]Inst, error)
}

var dialectFactories = map[vir.AsmDialect]func(SymbolResolver) asmDialect{
	vir.DialectIntel: func(r SymbolResolver) asmDialect { return intelDialect{resolver: r} },
	vir.DialectATT:   func(r SymbolResolver) asmDialect { return attDialect{resolver: r} },
}

func LowerBlock(dialect vir.AsmDialect, arch string, a *vir.AsmBlock, resolver SymbolResolver, uniqueLabel func(string) string) ([]Inst, error) {
	if arch != "x86" {
		return nil, fmt.Errorf("asm: inline assembly is only lowered for arch \"x86\" (32-bit); got %q", arch)
	}
	factory, ok := dialectFactories[dialect]
	if !ok {
		return nil, fmt.Errorf("asm: no x86 lowering for dialect %q", dialect)
	}
	d := factory(resolver)

	var insts []Inst
	boundOut := map[string]isax86.Reg{}
	var outOrder []string

	for _, bind := range a.Bindings {
		switch bind.Kind {
		case vir.BindingIn:
			r, w, ok := d.Register(bind.Register)
			if !ok {
				return nil, fmt.Errorf("asm: register %q not recognized by this backend (§9.35)", bind.Register)
			}
			if w != 32 {
				return nil, fmt.Errorf("asm: register %q is %d-bit; this backend only lowers 32-bit x86", bind.Register, w)
			}
			insts = append(insts, Inst{Op: "mov", D: R(r), S: Slot(bind.Ident), Sz: 4})
		case vir.BindingOut:
			r, w, ok := d.Register(bind.Register)
			if !ok {
				return nil, fmt.Errorf("asm: register %q not recognized by this backend (§9.35)", bind.Register)
			}
			if w != 32 {
				return nil, fmt.Errorf("asm: register %q is %d-bit; this backend only lowers 32-bit x86", bind.Register, w)
			}
			boundOut[bind.Ident] = r
			outOrder = append(outOrder, bind.Ident)
		case vir.BindingClobber:
			// No code needed — optimization/memory barrier is implicit.
		}
	}

	for _, line := range a.Code {
		if line.LabelDeclaration != "" {
			insts = append(insts, Inst{Op: "label", Lbl: uniqueLabel(line.LabelDeclaration)})
			continue
		}
		li, err := d.Lower(line, uniqueLabel)
		if err != nil {
			return nil, err
		}
		insts = append(insts, li...)
	}

	for _, ident := range outOrder {
		insts = append(insts, Inst{Op: "mov", D: Slot(ident), S: R(boundOut[ident]), Sz: 4})
	}
	return insts, nil
}

var physicalSlot = map[string]isax86.Reg{
	"RAX": isax86.REAX, "RCX": isax86.RECX, "RDX": isax86.REDX, "RBX": isax86.REBX,
	"RSP": isax86.RESP, "RBP": isax86.REBP, "RSI": isax86.RESI, "RDI": isax86.REDI,
}

func resolveRegister(name string) (r isax86.Reg, widthBits int, err error) {
	name = strings.TrimPrefix(name, "%")
	info, ok := vir.RegisterTableForArchitecture("x86")[name]
	if !ok {
		return isax86.RNone, 0, fmt.Errorf("asm: register %q is not in the x86 register table (§9.35)", name)
	}
	pr, ok := physicalSlot[info.PhysicalSlot]
	if !ok {
		return isax86.RNone, 0, fmt.Errorf("asm: register %q (physical %s) has no 32-bit encoding; this backend only lowers arch \"x86\"", name, info.PhysicalSlot)
	}
	return pr, info.WidthBits, nil
}

func resolveImmediate(o vir.Operand, resolver SymbolResolver) (Opr, error) {
	switch o.Kind {
	case vir.OperandInt:
		return Imm(o.Int), nil
	case vir.OperandBool:
		if o.Bool {
			return Imm(1), nil
		}
		return Imm(0), nil
	case vir.OperandNull:
		return Imm(0), nil
	case vir.OperandIdent:
		if resolver == nil {
			return Opr{}, fmt.Errorf("asm: %q used as an operand needs module context to resolve", o.Ident)
		}
		return resolver.Resolve(o.Ident)
	case vir.OperandFloat:
		return Opr{}, fmt.Errorf("asm: floating-point immediates are not lowered by this backend (TODO)")
	}
	return Opr{}, fmt.Errorf("asm: operand kind not legal in this position")
}

var jccTable = map[string]byte{
	"je": isax86.CondE, "jz": isax86.CondE,
	"jne": isax86.CondNE, "jnz": isax86.CondNE,
	"jl": isax86.CondL, "jnge": isax86.CondL,
	"jle": isax86.CondLE, "jng": isax86.CondLE,
	"jg": isax86.CondG, "jnle": isax86.CondG,
	"jge": isax86.CondGE, "jnl": isax86.CondGE,
	"jb": isax86.CondB, "jc": isax86.CondB, "jnae": isax86.CondB,
	"jbe": isax86.CondBE, "jna": isax86.CondBE,
	"ja": isax86.CondA, "jnbe": isax86.CondA,
	"jae": isax86.CondAE, "jnc": isax86.CondAE, "jnb": isax86.CondAE,
	"jo": isax86.CondO, "jno": isax86.CondNO,
	"js": isax86.CondS, "jns": isax86.CondNS,
}

func lowerJump(line vir.AsmCodeLine, isJcc bool, cc byte, label func(string) string) ([]Inst, error) {
	if len(line.Operands) != 1 || line.Operands[0].Kind != vir.AsmOperandKindLabel {
		return nil, fmt.Errorf("asm: %q expects a single local-label operand (§9.39)", line.Mnemonic)
	}
	lbl := label(line.Operands[0].Label)
	if isJcc {
		return []Inst{{Op: "jcc", CC: cc, Lbl: lbl}}, nil
	}
	return []Inst{{Op: "jmp", Lbl: lbl}}, nil
}

func lowerMnemonic(mnemonic string, ops []Opr) ([]Inst, error) {
	need := func(n int) error {
		if len(ops) != n {
			return fmt.Errorf("asm: %q expects %d operand(s), got %d", mnemonic, n, len(ops))
		}
		return nil
	}
	switch mnemonic {
	case "mov":
		if err := need(2); err != nil {
			return nil, err
		}
		return []Inst{{Op: "mov", D: ops[0], S: ops[1], Sz: 4}}, nil

	case "add", "sub", "and", "or", "xor", "cmp":
		if err := need(2); err != nil {
			return nil, err
		}
		return []Inst{{Op: mnemonic, D: ops[0], S: ops[1]}}, nil

	case "test":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg || ops[1].Kind != OReg {
			return nil, fmt.Errorf("asm: test requires two registers")
		}
		return []Inst{{Op: "test", D: ops[0], S: ops[1]}}, nil

	case "lea":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[1].Kind != OMem {
			return nil, fmt.Errorf("asm: lea's source must be a memory operand")
		}
		return []Inst{{Op: "lea", D: ops[0], S: ops[1]}}, nil

	case "imul":
		switch len(ops) {
		case 1:
			return []Inst{{Op: "imul32", S: ops[0]}}, nil
		case 2:
			return []Inst{{Op: "imul", D: ops[0], S: ops[1]}}, nil
		case 3:
			if ops[2].Kind != OImm {
				return nil, fmt.Errorf("asm: imul's third operand must be an immediate")
			}
			return []Inst{{Op: "imul3", D: ops[0], S: ops[1], Imm: ops[2].Imm}}, nil
		}
		return nil, fmt.Errorf("asm: %q expects 1, 2, or 3 operands, got %d", mnemonic, len(ops))

	case "mul":
		if err := need(1); err != nil {
			return nil, err
		}
		return []Inst{{Op: "mul32", S: ops[0]}}, nil

	case "not", "neg", "div", "idiv":
		if err := need(1); err != nil {
			return nil, err
		}
		return []Inst{{Op: mnemonic, S: ops[0]}}, nil

	case "inc", "dec", "bswap":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg {
			return nil, fmt.Errorf("asm: %q requires a register operand", mnemonic)
		}
		return []Inst{{Op: mnemonic, D: ops[0]}}, nil

	case "push":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg && ops[0].Kind != OImm {
			return nil, fmt.Errorf("asm: push requires a register or immediate operand")
		}
		return []Inst{{Op: "push", S: ops[0]}}, nil

	case "pop":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OReg {
			return nil, fmt.Errorf("asm: pop requires a register operand")
		}
		return []Inst{{Op: "pop", D: ops[0]}}, nil

	case "shl", "shr", "sar", "rol", "ror":
		if err := need(2); err != nil {
			return nil, err
		}
		if ops[1].Kind == OImm {
			return []Inst{{Op: mnemonic, D: ops[0], S: ops[1], Sz: 4}}, nil
		}
		if ops[1].Kind != OReg || ops[1].Reg != isax86.RECX {
			return nil, fmt.Errorf("asm: %q's shift count must be an immediate or cl/%%cl", mnemonic)
		}
		return []Inst{{Op: mnemonic, D: ops[0], Sz: 4}}, nil

	case "int":
		if err := need(1); err != nil {
			return nil, err
		}
		if ops[0].Kind != OImm {
			return nil, fmt.Errorf("asm: int requires an immediate operand")
		}
		return []Inst{{Op: "int", Imm: ops[0].Imm}}, nil

	case "nop":
		if err := need(0); err != nil {
			return nil, err
		}
		return []Inst{{Op: "nop"}}, nil

	case "cdq":
		if err := need(0); err != nil {
			return nil, err
		}
		return []Inst{{Op: "cdq"}}, nil

	case "syscall", "sysenter":
		return nil, fmt.Errorf("asm: %q has no 32-bit x86 encoding; use `int 0x80` for a Linux/*BSD-style trap", mnemonic)
	}
	return nil, fmt.Errorf("asm: mnemonic %q not lowered by this backend (§9.38 mnemonic table is a curated subset)", mnemonic)
}