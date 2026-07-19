package text

import (
	"strconv"

	"github.com/vertex-language/vvm/ir/vir"
)

type attSyntax struct{}

func (attSyntax) regIdent(reg string) string { return "%" + reg }

func (attSyntax) encodeOperand(op vir.AsmOperand) string {
	switch op.Kind {
	case vir.AsmOperandKindRegister:
		return "%" + op.Register
	case vir.AsmOperandKindImmediate:
		return "$" + op.Immediate.String()
	case vir.AsmOperandKindMemory:
		return op.Memory
	case vir.AsmOperandKindLabel:
		return op.Label
	}
	return "<bad asm operand>"
}

func (attSyntax) parseOperand(c *lc, arch string) (vir.AsmOperand, error) {
	tk, ok := c.peek()
	if !ok {
		return vir.AsmOperand{}, c.l.errf("expected asm operand")
	}
	switch {
	case tk.kind == tPunct && tk.text == "%":
		c.next()
		reg, err := c.expectIdent()
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmRegister(reg), nil
	case tk.kind == tPunct && tk.text == "$":
		c.next()
		imm, err := parseImmValue(c)
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmImmediate(imm), nil
	case tk.kind == tPunct && tk.text == "(":
		text, err := readAsmMemory(c, "")
		if err != nil {
			return vir.AsmOperand{}, err
		}
		return vir.AsmMemory(text), nil
	case tk.kind == tInt:
		if n, ok2 := c.peekN(1); ok2 && n.kind == tPunct && n.text == "(" {
			disp := tk.text
			c.next()
			text, err := readAsmMemory(c, disp)
			if err != nil {
				return vir.AsmOperand{}, err
			}
			return vir.AsmMemory(text), nil
		}
		c.next()
		v, err := strconv.ParseInt(tk.text, 10, 64)
		if err != nil {
			return vir.AsmOperand{}, c.l.errf("bad integer %q", tk.text)
		}
		return vir.AsmImmediate(vir.IntLiteral(v)), nil
	case tk.kind == tIdent:
		c.next()
		return vir.AsmLabelReference(tk.text), nil
	}
	return vir.AsmOperand{}, c.l.errf("unrecognized asm operand %q", tk.text)
}