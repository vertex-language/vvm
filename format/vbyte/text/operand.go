// operand.go
package text

import (
	"math"
	"strconv"

	"github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// Operands (module-grammar `operand`, §1.1)
// ---------------------------------------------------------------------------

var orderings = map[string]bool{
	"relaxed": true, "acquire": true, "release": true, "acqrel": true, "seqcst": true,
}

func parseOperand(c *lc) (vir.Operand, error) {
	tk, ok := c.peek()
	if !ok {
		return vir.Operand{}, c.l.errf("expected operand")
	}
	switch tk.kind {
	case tInt:
		c.next()
		v, err := strconv.ParseInt(tk.text, 10, 64)
		if err != nil {
			return vir.Operand{}, c.l.errf("bad integer %q: %v", tk.text, err)
		}
		return vir.IntLiteral(v), nil
	case tFloat:
		c.next()
		v, err := strconv.ParseFloat(tk.text, 64)
		if err != nil {
			return vir.Operand{}, c.l.errf("bad float %q: %v", tk.text, err)
		}
		return vir.FloatLiteral(v), nil
	case tString:
		c.next()
		return vir.StringLiteral(tk.text), nil
	case tPunct:
		if tk.text == "(" { // vector literal
			c.next()
			var lanes []int64
			for {
				n, err := expectInt64(c)
				if err != nil {
					return vir.Operand{}, err
				}
				lanes = append(lanes, n)
				if c.accept(tPunct, ")") {
					break
				}
				if err := c.expectPunct(","); err != nil {
					return vir.Operand{}, err
				}
			}
			return vir.VectorLiteral(lanes...), nil
		}
	case tIdent:
		switch tk.text {
		case "true":
			c.next()
			return vir.BoolLiteral(true), nil
		case "false":
			c.next()
			return vir.BoolLiteral(false), nil
		case "null":
			c.next()
			return vir.NullLiteral(), nil
		case "NaN":
			c.next()
			return vir.FloatLiteral(math.NaN()), nil
		case "Inf":
			c.next()
			return vir.FloatLiteral(math.Inf(1)), nil
		case "-Inf":
			c.next()
			return vir.FloatLiteral(math.Inf(-1)), nil
		}
		if orderings[tk.text] {
			c.next()
			return vir.OrderingOperand(tk.text), nil
		}
		// Type in operand position (index.ptr)?
		save := c.i
		if t, err := parseType(c); err == nil {
			return vir.TypeOperand(t), nil
		}
		c.i = save
		name, _ := c.next()
		// qualified-ident := ident "." ident (§7.3) — cross-module operand
		// reference, e.g. `call module.foo` or `field.ptr p, module.S, f`.
		// vir.Operand.Qualifier / QualifiedIdent exist specifically for
		// this; an ordinary local ident leaves Qualifier "".
		if dot, ok := c.peek(); ok && dot.kind == tPunct && dot.text == "." {
			if nn, ok2 := c.peekN(1); ok2 && nn.kind == tIdent {
				c.next() // consume "."
				c.next() // consume second ident
				return vir.QualifiedIdent(name.text, nn.text), nil
			}
		}
		return vir.Ident(name.text), nil
	}
	return vir.Operand{}, c.l.errf("unexpected token %q in operand position", tk.text)
}

func parseOperandList(c *lc) ([]vir.Operand, error) {
	var out []vir.Operand
	if c.done() {
		return out, nil
	}
	for {
		o, err := parseOperand(c)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
		if !c.accept(tPunct, ",") {
			break
		}
	}
	if !c.done() {
		return nil, c.l.errf("trailing tokens")
	}
	return out, nil
}