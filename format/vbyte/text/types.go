// types.go
package text

import (
	"strconv"

	"github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

func parseType(c *lc) (vir.Type, error) {
	tk, ok := c.peek()
	if !ok || tk.kind != tIdent {
		return nil, c.l.errf("expected type")
	}
	switch tk.text {
	case "ptr":
		c.next()
		return vir.Ptr, nil
	case "void":
		c.next()
		return vir.Void, nil
	case "valist":
		// Opaque cursor type (§3, §4.5) — legal only as an alloca result or
		// a va_start/va_arg/va_end operand; that restriction is Verify's
		// job (vir/verify.go, vir/types.go IsValueType), not the parser's.
		c.next()
		return vir.Valist, nil
	case "struct":
		c.next()
		n, err := c.expectIdent()
		if err != nil {
			return nil, err
		}
		// Cross-module form "struct module.Name" (§7.3/§7.4) — StructType's
		// canonical spelling (types.go String()) qualifies with the import
		// path when Import != "", so the parser must accept it symmetrically.
		if c.accept(tPunct, ".") {
			n2, err := c.expectIdent()
			if err != nil {
				return nil, err
			}
			return vir.StructType{Name: n2, Import: n}, nil
		}
		return vir.StructType{Name: n}, nil
	case "vec", "array":
		kw := tk.text
		c.next()
		if err := c.expectPunct("["); err != nil {
			return nil, err
		}
		elem, err := parseType(c)
		if err != nil {
			return nil, err
		}
		if err := c.expectPunct(","); err != nil {
			return nil, err
		}
		n, err := expectInt(c)
		if err != nil {
			return nil, err
		}
		if err := c.expectPunct("]"); err != nil {
			return nil, err
		}
		if kw == "vec" {
			return vir.VecType{Elem: elem, Len: n}, nil
		}
		return vir.ArrayType{Elem: elem, Len: n}, nil
	}
	// iN / fN
	s := tk.text
	if len(s) >= 2 && (s[0] == 'i' || s[0] == 'f') && s[1] >= '1' && s[1] <= '9' {
		bits, err := strconv.Atoi(s[1:])
		if err == nil {
			c.next()
			if s[0] == 'i' {
				return vir.IntType{Bits: bits}, nil
			}
			switch bits {
			case 16, 32, 64:
				return vir.FloatType{Bits: bits}, nil
			}
			return nil, c.l.errf("illegal float width f%d", bits)
		}
	}
	return nil, c.l.errf("%q is not a type", s)
}