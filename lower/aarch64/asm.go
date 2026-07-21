// asm.go lowers verified vir.AsmBlock values into this package's own Inst
// stream (isel.go's), so there's exactly one encoder for both. Only the
// native dialect is supported — the only one ir/vir/targets.go registers
// for aarch64/aarch64_be.
//
// Coverage is a curated mnemonic set, not the full A64 assembly language:
// mov, mvn, neg, add, sub, and, orr, eor, mul, udiv, sdiv, cmp, ldr, str,
// b, cbz, cbnz, svc, brk, ret, nop. add/sub/and/orr/eor are 2-operand
// accumulate forms ("add xd, xs" means xd += xs) — genuine 3-operand
// forms aren't supported yet. Memory operands support only "reg" and
// "reg+disp"/"reg-disp" text (this package's own concrete grammar for
// the dialect-specific AsmOperand.Memory string).
package aarch64

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// LowerBlock lowers one inline-asm block. regTable is the module target's
// register table (vir.RegisterTableForArchitecture); dialect must be
// vir.DialectNative.
func LowerBlock(dialect vir.AsmDialect, block *vir.AsmBlock, regTable map[string]vir.RegisterInfo) ([]Inst, error) {
	if dialect != vir.DialectNative {
		return nil, fmt.Errorf("asm: aarch64 only supports the native dialect, got %q", dialect)
	}

	var out []Inst

	for _, b := range block.Bindings {
		if b.Kind != vir.BindingIn {
			continue
		}
		r, _, ok := resolveReg(b.Register, regTable)
		if !ok {
			return nil, fmt.Errorf("asm: unknown or unwired register %q", b.Register)
		}
		out = append(out, Inst{Op: "ldr", D: R(r), S: Slot(b.Ident), Sz: 8})
	}

	for _, line := range block.Code {
		if line.LabelDeclaration != "" {
			out = append(out, Inst{Op: "label", Lbl: asmLabel(line.LabelDeclaration)})
			continue
		}
		insts, err := lowerAsmLine(line, regTable)
		if err != nil {
			return nil, err
		}
		out = append(out, insts...)
	}

	for _, b := range block.Bindings {
		if b.Kind != vir.BindingOut {
			continue
		}
		r, _, ok := resolveReg(b.Register, regTable)
		if !ok {
			return nil, fmt.Errorf("asm: unknown or unwired register %q", b.Register)
		}
		out = append(out, Inst{Op: "str", D: Slot(b.Ident), S: R(r), Sz: 8})
	}
	return out, nil
}

// asmLabel namespaces an asm-local label declaration so it can't collide
// with isel's own compiler-generated labels (§4 label isolation).
func asmLabel(name string) string { return ".asm." + name }

func widthSz(bits int) int {
	if bits > 32 {
		return 8
	}
	return 4
}

func lowerAsmLine(line vir.AsmCodeLine, regTable map[string]vir.RegisterInfo) ([]Inst, error) {
	m := strings.ToLower(line.Mnemonic)

	reg := func(i int) (encoder.Reg, int, error) {
		if i >= len(line.Operands) || line.Operands[i].Kind != vir.AsmOperandKindRegister {
			return 0, 0, fmt.Errorf("asm: %s expects a register operand at position %d", m, i)
		}
		r, w, ok := resolveReg(line.Operands[i].Register, regTable)
		if !ok {
			return 0, 0, fmt.Errorf("asm: unknown or unwired register %q", line.Operands[i].Register)
		}
		return r, w, nil
	}
	mem := func(i int) (encoder.Reg, int32, error) {
		if i >= len(line.Operands) || line.Operands[i].Kind != vir.AsmOperandKindMemory {
			return 0, 0, fmt.Errorf("asm: %s expects a memory operand at position %d", m, i)
		}
		return parseAsmMemory(line.Operands[i].Memory, regTable)
	}
	imm := func(i int) (int64, error) {
		if i >= len(line.Operands) || line.Operands[i].Kind != vir.AsmOperandKindImmediate {
			return 0, fmt.Errorf("asm: %s expects an immediate operand at position %d", m, i)
		}
		o := line.Operands[i].Immediate
		if o.Kind != vir.OperandInt {
			return 0, fmt.Errorf("asm: %s immediate must be an integer literal", m)
		}
		return o.Int, nil
	}
	label := func(i int) (string, error) {
		if i >= len(line.Operands) || line.Operands[i].Kind != vir.AsmOperandKindLabel {
			return "", fmt.Errorf("asm: %s expects a label operand at position %d", m, i)
		}
		return asmLabel(line.Operands[i].Label), nil
	}

	switch m {
	case "nop":
		return nil, nil
	case "brk":
		return []Inst{{Op: "brk"}}, nil
	case "ret":
		return []Inst{{Op: "ret"}}, nil
	case "svc":
		v, err := imm(0)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: "svc", Imm: v}}, nil

	case "mov", "mvn", "neg":
		d, w, err := reg(0)
		if err != nil {
			return nil, err
		}
		s, _, err := reg(1)
		if err != nil {
			return nil, err
		}
		op := map[string]string{"mov": "mov_r", "mvn": "mvn", "neg": "neg"}[m]
		return []Inst{{Op: op, Sz: widthSz(w), D: R(d), S: R(s)}}, nil

	case "add", "sub", "and", "orr", "eor":
		d, w, err := reg(0)
		if err != nil {
			return nil, err
		}
		s, _, err := reg(1)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: m, Sz: widthSz(w), D: R(d), S: R(s)}}, nil

	case "mul", "udiv", "sdiv":
		d, w, err := reg(0)
		if err != nil {
			return nil, err
		}
		s, _, err := reg(1)
		if err != nil {
			return nil, err
		}
		t, _, err := reg(2)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: m, Sz: widthSz(w), D: R(d), S: R(s), T: R(t)}}, nil

	case "cmp":
		d, w, err := reg(0)
		if err != nil {
			return nil, err
		}
		s, _, err := reg(1)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: "cmp", Sz: widthSz(w), D: R(d), S: R(s)}}, nil

	case "ldr":
		d, w, err := reg(0)
		if err != nil {
			return nil, err
		}
		base, disp, err := mem(1)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: "ldr", Sz: widthSz(w), D: R(d), S: Mem(base, disp)}}, nil

	case "str":
		s, w, err := reg(0)
		if err != nil {
			return nil, err
		}
		base, disp, err := mem(1)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: "str", Sz: widthSz(w), D: Mem(base, disp), S: R(s)}}, nil

	case "b":
		l, err := label(0)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: "b", Lbl: l}}, nil

	case "cbz", "cbnz":
		s, w, err := reg(0)
		if err != nil {
			return nil, err
		}
		l, err := label(1)
		if err != nil {
			return nil, err
		}
		return []Inst{{Op: m, Sz: widthSz(w), S: R(s), Lbl: l}}, nil

	default:
		return nil, fmt.Errorf("asm: mnemonic %q not in the curated aarch64 native-dialect set", line.Mnemonic)
	}
}

func parseAsmMemory(text string, regTable map[string]vir.RegisterInfo) (encoder.Reg, int32, error) {
	text = strings.TrimSpace(text)
	base, disp := text, ""
	if i := strings.IndexAny(text, "+-"); i > 0 {
		base, disp = text[:i], text[i:]
	}
	r, _, ok := resolveReg(strings.TrimSpace(base), regTable)
	if !ok {
		return 0, 0, fmt.Errorf("asm: bad memory base register %q", base)
	}
	if disp == "" {
		return r, 0, nil
	}
	v, err := strconv.ParseInt(disp, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("asm: bad memory displacement %q", disp)
	}
	return r, int32(v), nil
}