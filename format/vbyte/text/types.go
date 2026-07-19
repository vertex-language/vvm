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
	case "struct":
		c.next()
		n, err := c.expectIdent()
		if err != nil {
			return nil, err
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