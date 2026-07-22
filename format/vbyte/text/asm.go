package text

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// Inline assembly (§4.4). Structural only, per README/verify.go: mnemonic and
// operand-shape legality (§9.38) is explicitly not required at this layer.
// Dialect is module-wide (§2.1 step 4) — passed in from the caller, not
// read off the asm block itself.
// ---------------------------------------------------------------------------

func (p *parser) parseAsm(header *line, fb *vir.FunctionBuilder, arch string, dialect vir.AsmDialect) error {
	c := &lc{l: header}
	c.accept(tIdent, "asm")
	if err := c.expectPunct(":"); err != nil {
		return err
	}
	if !c.done() {
		return header.errf("trailing tokens after asm header")
	}

	ab := fb.BeginAsm()

bindings:
	for {
		bl := p.next()
		if bl == nil {
			return fmt.Errorf("unterminated asm block (missing code:/end)")
		}
		bc := &lc{l: bl}
		switch first(bl) {
		case "in":
			bc.next()
			reg, err := parseRegIdent(bc)
			if err != nil {
				return err
			}
			if err := bc.expectPunct("="); err != nil {
				return err
			}
			ident, err := bc.expectIdent()
			if err != nil {
				return err
			}
			if !bc.done() {
				return bl.errf("trailing tokens after in-binding")
			}
			ab.In(reg, ident)
		case "out":
			bc.next()
			reg, err := parseRegIdent(bc)
			if err != nil {
				return err
			}
			if err := bc.expectPunct("="); err != nil {
				return err
			}
			ident, err := bc.expectIdent()
			if err != nil {
				return err
			}
			if !bc.done() {
				return bl.errf("trailing tokens after out-binding")
			}
			ab.Out(reg, ident)
		case "clobber":
			bc.next()
			var regs []string
			for {
				r, err := parseRegIdent(bc)
				if err != nil {
					return err
				}
				regs = append(regs, r)
				if bc.done() {
					break
				}
				if err := bc.expectPunct(","); err != nil {
					return err
				}
			}
			ab.Clobber(regs...)
		case "code":
			bc.next()
			if err := bc.expectPunct(":"); err != nil {
				return err
			}
			if !bc.done() {
				return bl.errf("trailing tokens after code:")
			}
			break bindings
		default:
			return bl.errf("expected asm binding (in/out/clobber) or code:, got %q", first(bl))
		}
	}

	for {
		cl := p.next()
		if cl == nil {
			return fmt.Errorf("unterminated asm code section (missing end)")
		}
		if first(cl) == "end" {
			ab.End()
			return nil
		}
		codeLine, err := parseAsmCodeLine(cl, arch, dialect)
		if err != nil {
			return err
		}
		ab.Code(codeLine)
	}
}

// parseRegIdent parses "%"? ident, stripping the optional AT&T '%' prefix;
// the canonical register name (without '%') is what AsmBinding.Register
// stores, regardless of dialect.
func parseRegIdent(c *lc) (string, error) {
	c.accept(tPunct, "%")
	return c.expectIdent()
}

func parseAsmCodeLine(l *line, arch string, dialect vir.AsmDialect) (vir.AsmCodeLine, error) {
	if len(l.toks) == 2 && l.toks[0].kind == tIdent && l.toks[1].kind == tPunct && l.toks[1].text == ":" {
		return vir.AsmLabelDeclaration(l.toks[0].text), nil
	}
	c := &lc{l: l}
	mnem, err := c.expectIdent()
	if err != nil {
		return vir.AsmCodeLine{}, l.errf("expected mnemonic or label declaration")
	}
	syntax, ok := dialects[dialect]
	if !ok {
		return vir.AsmCodeLine{}, l.errf("no parser registered for asm dialect %q", dialect)
	}
	var ops []vir.AsmOperand
	for !c.done() {
		op, err := syntax.parseOperand(c, arch)
		if err != nil {
			return vir.AsmCodeLine{}, err
		}
		ops = append(ops, op)
		if c.done() {
			break
		}
		if err := c.expectPunct(","); err != nil {
			return vir.AsmCodeLine{}, err
		}
	}
	return vir.AsmInstructionLine(mnem, ops...), nil
}

// ---------------------------------------------------------------------------
// Encoding
// ---------------------------------------------------------------------------

func encodeAsmBlock(dialect vir.AsmDialect, a *vir.AsmBlock) string {
	syntax, ok := dialects[dialect]
	if !ok {
		return fmt.Sprintf("<bad asm dialect %q>\n", dialect)
	}
	var b strings.Builder
	b.WriteString("asm :\n")
	for _, bind := range a.Bindings {
		switch bind.Kind {
		case vir.BindingIn:
			fmt.Fprintf(&b, "  in %s = %s\n", syntax.regIdent(bind.Register), bind.Ident)
		case vir.BindingOut:
			fmt.Fprintf(&b, "  out %s = %s\n", syntax.regIdent(bind.Register), bind.Ident)
		case vir.BindingClobber:
			regs := make([]string, len(bind.Registers))
			for i, r := range bind.Registers {
				regs[i] = syntax.regIdent(r)
			}
			fmt.Fprintf(&b, "  clobber %s\n", strings.Join(regs, ", "))
		}
	}
	b.WriteString("code:\n")
	for _, line := range a.Code {
		if line.LabelDeclaration != "" {
			fmt.Fprintf(&b, "  %s:\n", line.LabelDeclaration)
			continue
		}
		parts := make([]string, len(line.Operands))
		for i, op := range line.Operands {
			parts[i] = syntax.encodeOperand(op)
		}
		if len(parts) == 0 {
			fmt.Fprintf(&b, "  %s\n", line.Mnemonic)
		} else {
			fmt.Fprintf(&b, "  %s %s\n", line.Mnemonic, strings.Join(parts, ", "))
		}
	}
	b.WriteString("end\n")
	return b.String()
}

func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}