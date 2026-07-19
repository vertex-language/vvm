package text

import (
	"fmt"
	"strconv"

	"github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// Function bodies: signature, block/terminator shape, ordinary instructions.
// Asm blocks are delegated to asm.go.
// ---------------------------------------------------------------------------

var terminatorWords = map[string]bool{
	"br": true, "br_if": true, "switch": true, "return": true,
	"tailcall": true, "trap": true, "unreachable": true,
}

func (p *parser) parseFn(m *vir.Module) error {
	l := p.next()
	c := &lc{l: l}
	export := c.accept(tIdent, "export")
	if !c.accept(tIdent, "fn") {
		return l.errf("expected fn")
	}
	name, params, variadic, ret, attrs, err := parseFnHead(c)
	if err != nil {
		return err
	}
	if variadic {
		return l.errf("variadics are rejected in fn definitions (§1.2 rule 5)")
	}
	if err := c.expectPunct(":"); err != nil {
		return err
	}
	if !c.done() {
		return l.errf("trailing tokens on fn signature line")
	}

	arch := ""
	if m.Target != nil {
		arch = m.Target.Arch
	}

	fb := m.DeclareFunction(name, params, ret, export, attrs...)
	terminated := false
	for {
		l := p.next()
		if l == nil {
			return fmt.Errorf("fn %s: unterminated (missing end)", name)
		}
		f := first(l)
		switch {
		case f == "end":
			if !terminated {
				return l.errf("fn %s: block ended without a terminator (§1.3 rule 2)", name)
			}
			return nil
		case len(l.toks) == 2 && l.toks[0].kind == tIdent && l.toks[1].kind == tPunct && l.toks[1].text == ":":
			if !terminated {
				return l.errf("fn %s: block must end with a terminator before next label (§1.3 rule 2)", name)
			}
			fb.Label(l.toks[0].text)
			terminated = false
		case f == "asm":
			if terminated {
				return l.errf("fn %s: asm block after terminator (§1.3 rule 2)", name)
			}
			if m.AsmDialect == nil {
				return l.errf("fn %s: asm block requires a module-level asmdialect declaration (§1.2 rule 11)", name)
			}
			if err := p.parseAsm(l, fb, arch, *m.AsmDialect); err != nil {
				return err
			}
		case terminatorWords[f]:
			if terminated {
				return l.errf("fn %s: multiple terminators in one block (§1.3 rule 2)", name)
			}
			if err := applyTerminator(&lc{l: l}, fb); err != nil {
				return err
			}
			terminated = true
		default:
			if terminated {
				return l.errf("fn %s: instruction after terminator (§1.3 rule 2)", name)
			}
			inst, err := parseInst(&lc{l: l})
			if err != nil {
				return err
			}
			fb.EmitInstruction(inst)
		}
	}
}

func applyTerminator(c *lc, fb *vir.FunctionBuilder) error {
	kw, _ := c.expectIdent()
	switch kw {
	case "br":
		lbl, err := c.expectIdent()
		if err != nil {
			return err
		}
		if !c.done() {
			return c.l.errf("trailing tokens after br")
		}
		fb.Branch(lbl)
	case "br_if":
		cond, err := parseOperand(c)
		if err != nil {
			return err
		}
		if err := c.expectPunct(","); err != nil {
			return err
		}
		then, err := c.expectIdent()
		if err != nil {
			return err
		}
		if err := c.expectPunct(","); err != nil {
			return err
		}
		els, err := c.expectIdent()
		if err != nil {
			return err
		}
		if !c.done() {
			return c.l.errf("trailing tokens after br_if")
		}
		fb.BranchIf(cond, then, els)
	case "switch":
		v, err := parseOperand(c)
		if err != nil {
			return err
		}
		if err := c.expectPunct(","); err != nil {
			return err
		}
		def, err := c.expectIdent()
		if err != nil {
			return err
		}
		var cases []vir.SwitchCase
		for c.accept(tPunct, ",") {
			n, err := expectInt64(c)
			if err != nil {
				return err
			}
			lbl, err := c.expectIdent()
			if err != nil {
				return err
			}
			cases = append(cases, vir.SwitchCase{Value: n, Label: lbl})
		}
		if !c.done() {
			return c.l.errf("trailing tokens after switch")
		}
		fb.Switch(v, def, cases...)
	case "return":
		if c.done() {
			fb.Return()
			return nil
		}
		o, err := parseOperand(c)
		if err != nil {
			return err
		}
		if !c.done() {
			return c.l.errf("trailing tokens after return")
		}
		fb.Return(o)
	case "tailcall":
		if c.accept(tPunct, ".") {
			sig, err := c.expectIdent()
			if err != nil {
				return err
			}
			args, err := parseOperandList(c)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return c.l.errf("indirect tailcall requires a callee pointer operand")
			}
			fb.TailCallIndirect(sig, args[0], args[1:]...)
			return nil
		}
		callee, err := c.expectIdent()
		if err != nil {
			return err
		}
		var args []vir.Operand
		for c.accept(tPunct, ",") {
			o, err := parseOperand(c)
			if err != nil {
				return err
			}
			args = append(args, o)
		}
		if !c.done() {
			return c.l.errf("trailing tokens after tailcall")
		}
		fb.TailCall(callee, args...)
	case "trap":
		if !c.done() {
			return c.l.errf("trailing tokens after trap")
		}
		fb.Trap()
	case "unreachable":
		if !c.done() {
			return c.l.errf("trailing tokens after unreachable")
		}
		fb.Unreachable()
	default:
		return c.l.errf("unknown terminator %q", kw)
	}
	return nil
}

func parseInst(c *lc) (vir.Instruction, error) {
	var inst vir.Instruction

	// loc-line is its own grammar production: space-separated, no commas.
	if tk, ok := c.peek(); ok && tk.kind == tIdent && tk.text == "loc" {
		c.next()
		ftok, ok := c.next()
		if !ok || ftok.kind != tString {
			return inst, c.l.errf("loc: expected file string literal")
		}
		ltok, ok := c.next()
		if !ok || ltok.kind != tInt {
			return inst, c.l.errf("loc: expected line number")
		}
		lineNo, err := strconv.ParseInt(ltok.text, 10, 64)
		if err != nil {
			return inst, c.l.errf("loc: bad line number %q", ltok.text)
		}
		args := []vir.Operand{vir.StringLiteral(ftok.text), vir.IntLiteral(lineNo)}
		if !c.done() {
			ctok, ok := c.next()
			if !ok || ctok.kind != tInt {
				return inst, c.l.errf("loc: expected column number")
			}
			col, err := strconv.ParseInt(ctok.text, 10, 64)
			if err != nil {
				return inst, c.l.errf("loc: bad column number %q", ctok.text)
			}
			args = append(args, vir.IntLiteral(col))
		}
		if !c.done() {
			return inst, c.l.errf("loc: trailing tokens")
		}
		return vir.Instruction{Op: "loc", Args: args}, nil
	}

	// result name?
	if len(c.l.toks) > 1 && c.l.toks[0].kind == tIdent &&
		c.l.toks[1].kind == tPunct && c.l.toks[1].text == "=" {
		inst.Result = c.l.toks[0].text
		c.i = 2
	}
	op, err := c.expectIdent()
	if err != nil {
		return inst, err
	}
	inst.Op = op
	if c.accept(tPunct, ".") {
		// Suffix is a type or a fnsig name (§4).
		save := c.i
		if t, terr := parseType(c); terr == nil {
			inst.Suffix = t
		} else {
			c.i = save
			sig, err := c.expectIdent()
			if err != nil {
				return inst, err
			}
			inst.Sig = sig
		}
	}
	args, err := parseOperandList(c)
	if err != nil {
		return inst, err
	}
	// Trailing ", align N" (§1.1 align-clause) — spelled as ident+int operands.
	if n := len(args); n >= 2 && args[n-2].Kind == vir.OperandIdent && args[n-2].Ident == "align" && args[n-1].Kind == vir.OperandInt {
		inst.Align = int(args[n-1].Int)
		args = args[:n-2]
	}
	inst.Args = args
	return inst, nil
}

// ---------------------------------------------------------------------------
// Encoding
// ---------------------------------------------------------------------------

func encodeFunctionsSection(w func(string, ...any), m *vir.Module) {
	var dialect vir.AsmDialect
	if m.AsmDialect != nil {
		dialect = *m.AsmDialect
	}
	for _, f := range m.Functions {
		if f.Export {
			w("export ")
		}
		w("fn %s(%s) %s%s:\n", f.Name, encodeParams(f.Params, false), f.Ret.String(), encodeAttrs(f.Attrs))
		for _, blk := range f.AllBlocks() {
			if blk.Label != "" {
				w("%s:\n", blk.Label)
			}
			for _, ln := range blk.Lines {
				switch {
				case ln.Instruction != nil:
					w("    %s\n", encodeInst(ln.Instruction))
				case ln.Asm != nil:
					w("%s", indentLines(encodeAsmBlock(dialect, ln.Asm), "    "))
				}
			}
			w("    %s\n", encodeTerm(blk.Term))
		}
		w("end\n")
	}
}

// encodeInst formats one ordinary body-line instruction. `loc` is a
// distinct grammar production (space-separated, no operand-list commas)
// and is special-cased.
func encodeInst(i *vir.Instruction) string {
	if i.Op == "loc" {
		return encodeLoc(i)
	}
	var b []byte
	if i.Result != "" {
		b = append(b, i.Result...)
		b = append(b, " = "...)
	}
	b = append(b, i.Op...)
	if i.Suffix != nil {
		b = append(b, '.')
		b = append(b, i.Suffix.String()...)
	} else if i.Sig != "" {
		b = append(b, '.')
		b = append(b, i.Sig...)
	}
	for j, a := range i.Args {
		if j == 0 {
			b = append(b, ' ')
		} else {
			b = append(b, ", "...)
		}
		b = append(b, a.String()...)
	}
	if i.Align != 0 {
		b = append(b, fmt.Sprintf(", align %d", i.Align)...)
	}
	return string(b)
}

func encodeLoc(i *vir.Instruction) string {
	parts := make([]string, len(i.Args))
	for j, a := range i.Args {
		parts[j] = a.String()
	}
	return "loc " + joinStrings(parts, " ")
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func encodeTerm(t vir.Terminator) string {
	switch x := t.(type) {
	case vir.Branch:
		return "br " + x.Label
	case vir.BranchIf:
		return fmt.Sprintf("br_if %s, %s, %s", x.Cond.String(), x.Then, x.Else)
	case vir.Switch:
		s := fmt.Sprintf("switch %s, %s", x.Value.String(), x.Default)
		for _, c := range x.Cases {
			s += fmt.Sprintf(", %d %s", c.Value, c.Label)
		}
		return s
	case vir.Return:
		if x.Value == nil {
			return "return"
		}
		return "return " + x.Value.String()
	case vir.TailCall:
		if x.Callee != "" {
			s := "tailcall " + x.Callee
			for _, a := range x.Args {
				s += ", " + a.String()
			}
			return s
		}
		s := "tailcall." + x.Sig
		for j, a := range x.Args {
			if j == 0 {
				s += " " + a.String()
			} else {
				s += ", " + a.String()
			}
		}
		return s
	case vir.Trap:
		return "trap"
	case vir.Unreachable:
		return "unreachable"
	}
	return "<bad terminator>"
}