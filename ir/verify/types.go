// types.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// typeCtx tracks, in declaration order, which local struct names are
// visible for use so far (§2.2 declare-before-use). Imported struct types
// (Import != "") are never checked here — importer verifies those shapes
// against the real target module.
type typeCtx struct {
	structs map[string]bool
}

func structTypeCtx(m *vir.Module) *typeCtx {
	tc := &typeCtx{structs: make(map[string]bool, len(m.Structs))}
	for _, s := range m.Structs {
		tc.structs[s.Name] = true
	}
	return tc
}

// checkType validates t is well-formed given what's been declared so far.
// It does not check vector width against a feature tier (§7.1) — that
// needs the module's Target and is left to the caller where relevant.
func (c *typeCtx) checkType(t vir.Type) error {
	if t == nil {
		return fmt.Errorf("missing type")
	}
	switch x := t.(type) {
	case vir.IntType:
		if x.Bits < 1 {
			return fmt.Errorf("integer type i%d: bit width must be >= 1 (§3)", x.Bits)
		}
	case vir.FloatType:
		if x.Bits != 16 && x.Bits != 32 && x.Bits != 64 {
			return fmt.Errorf("float type f%d: only f16/f32/f64 are legal (§3)", x.Bits)
		}
	case vir.PtrType, vir.VoidType:
		// always fine
	case vir.VecType:
		if x.Len <= 0 {
			return fmt.Errorf("vec[%s, %d]: length must be positive (§3)", x.Elem, x.Len)
		}
		if vir.IsAggregate(x.Elem) || vir.IsVoid(x.Elem) || vir.IsValist(x.Elem) {
			return fmt.Errorf("vec[%s, %d]: element type must be a scalar/ptr (§3)", x.Elem, x.Len)
		}
		return c.checkType(x.Elem)
	case vir.StructType:
		if x.Import == "" && !c.structs[x.Name] {
			return fmt.Errorf("struct %q used before its declaration (§2.2)", x.Name)
		}
	case vir.ArrayType:
		if x.Len <= 0 {
			return fmt.Errorf("array[%s, %d]: length must be positive (§3)", x.Elem, x.Len)
		}
		return c.checkType(x.Elem)
	case vir.ValistType:
		// Legality of *where* valist may appear (params, globals, struct
		// fields, vectors) is checked at each such call site, not here —
		// as a bare type it's always structurally well-formed (§3, §4.5).
	default:
		return fmt.Errorf("unrecognized type %T", t)
	}
	return nil
}

func isArrayOfI8(t vir.Type) bool {
	arr, ok := t.(vir.ArrayType)
	if !ok {
		return false
	}
	it, ok := arr.Elem.(vir.IntType)
	return ok && it.Bits == 8
}