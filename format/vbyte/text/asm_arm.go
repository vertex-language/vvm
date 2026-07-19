package text

import "github.com/vertex-language/vvm/ir/vir"

// armSyntax serves a32, t32, and native — all three share the same
// operand grammar (§1.1 arm-mem, imm-operand with '#').
type armSyntax struct{}

func (armSyntax) regIdent(reg string) string { return reg }

func (armSyntax) encodeOperand(op vir.AsmOperand) string {
	switch op.Kind {
	case vir.AsmOperandKindRegister:
		return op.Register
	case vir.AsmOperandKindImmediate:
		return "#" + op.Immediate.String()
	case vir.AsmOperandKindMemory:
		return op.Memory
	case vir.AsmOperandKindLabel:
		return op.Label
	}
	return "<bad asm operand>"
}

func (armSyntax) parseOperand(c *lc, arch string) (vir.AsmOperand, error) {
	tk, ok := c.peek()
	if !ok {
		return vir.AsmOperand{}, c.l.errf("expected asm operand")
	}
	switch {
	case tk.kind == tPunct && tk.text == "[":
		text, err := readAsmMemory(c, "")
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmMemory(text), nil
	case tk.kind == tPunct && tk.text == "#":
		c.next()
		imm, err := parseImmValue(c)
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmImmediate(imm), nil
	case tk.kind == tInt || tk.kind == tFloat:
		c.next()
		imm, err := literalOperand(tk)
		if err != nil {
			return vir.AsmOperand{}, c.l.errf("%v", err)
		}
		return vir.AsmImmediate(imm), nil
	case tk.kind == tIdent:
		c.next()
		if regTableHas(arch, tk.text) {
			return vir.AsmRegister(tk.text), nil
		}
		return vir.AsmLabelReference(tk.text), nil
	}
	return vir.AsmOperand{}, c.l.errf("unrecognized asm operand %q", tk.text)
}