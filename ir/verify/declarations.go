// declarations.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// checkStructs validates §2.1 struct-decls and registers each name in the
// flat namespace (§2.2). Field types may only reference structs declared
// strictly earlier — no forward references (§2.2).
func checkStructs(m *vir.Module, names *nameTable) error {
	tc := &typeCtx{structs: make(map[string]bool, len(m.Structs))}
	for _, s := range m.Structs {
		if len(s.Fields) == 0 {
			return fmt.Errorf("struct %q: at least one field required", s.Name)
		}
		seen := make(map[string]bool, len(s.Fields))
		for _, f := range s.Fields {
			if seen[f.Name] {
				return fmt.Errorf("struct %q: field %q declared twice", s.Name, f.Name)
			}
			seen[f.Name] = true
			if vir.IsValist(f.Type) {
				return fmt.Errorf("struct %q field %q: valist is never a struct field (§3)", s.Name, f.Name)
			}
			if vir.IsVoid(f.Type) {
				return fmt.Errorf("struct %q field %q: void is not a storable type", s.Name, f.Name)
			}
			if err := tc.checkType(f.Type); err != nil {
				return fmt.Errorf("struct %q field %q: %w", s.Name, f.Name, err)
			}
		}
		if err := names.declare("struct", s.Name); err != nil {
			return err
		}
		tc.structs[s.Name] = true
	}
	return nil
}

// checkFunctionSignatures validates §2.1 fnsig-decls (used to type-check
// indirect calls/tailcalls, §4.2). All structs are visible by now (structs
// come first in section order), so it uses the full struct set.
func checkFunctionSignatures(m *vir.Module, names *nameTable) error {
	tc := structTypeCtx(m)
	for _, sig := range m.FunctionSignatures {
		for i, p := range sig.Params {
			if vir.IsValist(p) {
				return fmt.Errorf("fnsig %q param %d: valist is never a fnsig param type (§3, §4.5)", sig.Name, i)
			}
			if !vir.IsValueType(p) {
				return fmt.Errorf("fnsig %q param %d: %s is not a value type", sig.Name, i, p)
			}
			if err := tc.checkType(p); err != nil {
				return fmt.Errorf("fnsig %q param %d: %w", sig.Name, i, err)
			}
		}
		if sig.Ret == nil {
			return fmt.Errorf("fnsig %q: return type is required", sig.Name)
		}
		if err := tc.checkType(sig.Ret); err != nil {
			return fmt.Errorf("fnsig %q return type: %w", sig.Name, err)
		}
		if vir.IsAggregate(sig.Ret) {
			return fmt.Errorf("fnsig %q: return type must not be an aggregate (%s) — pass via sret instead", sig.Name, sig.Ret)
		}
		if err := names.declare("fnsig", sig.Name); err != nil {
			return err
		}
	}
	return nil
}

// checkConstants validates §2.1 const-decls (§6.2: compile-time scalars only).
func checkConstants(m *vir.Module, names *nameTable) error {
	tc := structTypeCtx(m)
	for _, c := range m.Constants {
		if err := tc.checkType(c.Type); err != nil {
			return fmt.Errorf("const %q: %w", c.Name, err)
		}
		if !vir.IsScalarType(c.Type) && !vir.IsVec(c.Type) {
			return fmt.Errorf("const %q: type %s is not a compile-time scalar (§6.2)", c.Name, c.Type)
		}
		if err := checkLiteralOperand(c.Value, c.Type); err != nil {
			return fmt.Errorf("const %q: %w", c.Name, err)
		}
		if err := names.declare("const", c.Name); err != nil {
			return err
		}
	}
	return nil
}

// checkLiteralOperand checks a const/global literal operand is a legal
// compile-time value for t. (ConstInit/Operand's closed set of Go types
// already enforces §6.2's "no arithmetic, no const references, no offsets"
// restriction structurally — there's no way to construct anything else.)
func checkLiteralOperand(op vir.Operand, t vir.Type) error {
	switch op.Kind {
	case vir.OperandInt:
		if !vir.IsInt(vir.ElemOrSelf(t)) && !vir.IsPtr(t) {
			return fmt.Errorf("integer literal not legal for type %s", t)
		}
	case vir.OperandFloat:
		if !vir.IsFloat(vir.ElemOrSelf(t)) {
			return fmt.Errorf("float literal not legal for type %s", t)
		}
	case vir.OperandBool:
		if !vir.IsInt(t) {
			return fmt.Errorf("bool literal not legal for type %s", t)
		}
	case vir.OperandNull:
		if !vir.IsPtr(t) {
			return fmt.Errorf("null literal only legal for ptr, got %s", t)
		}
	case vir.OperandVector:
		if !vir.IsVec(t) {
			return fmt.Errorf("vector literal only legal for vec types, got %s", t)
		}
	default:
		return fmt.Errorf("operand kind not legal as a compile-time literal")
	}
	return nil
}

// checkGlobals validates §2.1 global-decls / §6.2.
func checkGlobals(m *vir.Module, names *nameTable) error {
	tc := structTypeCtx(m)
	seenGlobals := make(map[string]bool, len(m.Globals))
	for _, g := range m.Globals {
		if vir.IsValist(g.Type) {
			return fmt.Errorf("global %q: valist is never a legal global type (§4.5, §6.2)", g.Name)
		}
		if err := tc.checkType(g.Type); err != nil {
			return fmt.Errorf("global %q: %w", g.Name, err)
		}
		if g.Align < 0 {
			return fmt.Errorf("global %q: align must be >= 0", g.Name)
		}
		if err := checkConstInit(g.Init, g.Type, g.TLS, seenGlobals); err != nil {
			return fmt.Errorf("global %q: %w", g.Name, err)
		}
		if err := names.declare("global", g.Name); err != nil {
			return err
		}
		seenGlobals[g.Name] = true
	}
	return nil
}

// checkConstInit validates a const-init tree against its declared type
// (§6.2). addr may only name a global declared strictly earlier in this
// same Globals list — fn/extern groups always sit later in the fixed
// section order (§2.1), so they can never be "earlier" no matter how the
// comment in README §6.2 reads generically.
func checkConstInit(init vir.ConstInit, t vir.Type, tls bool, seenGlobals map[string]bool) error {
	switch x := init.(type) {
	case vir.InitZero:
		return nil
	case vir.InitLiteral:
		return checkLiteralOperand(x.Value, t)
	case vir.InitAddressOf:
		if tls {
			return fmt.Errorf("addr initializer illegal on a tls global (§6.2)")
		}
		if !vir.IsPtr(t) {
			return fmt.Errorf("addr initializer only legal for ptr-typed globals, got %s", t)
		}
		if !seenGlobals[x.Name] {
			return fmt.Errorf("addr %q: must name an earlier global (§2.2 declare-before-use)", x.Name)
		}
		return nil
	case vir.InitAggregate:
		switch agg := t.(type) {
		case vir.ArrayType:
			if len(x.Elems) != agg.Len {
				return fmt.Errorf("aggregate initializer has %d elements, array type wants %d", len(x.Elems), agg.Len)
			}
			for i, e := range x.Elems {
				if err := checkConstInit(e, agg.Elem, tls, seenGlobals); err != nil {
					return fmt.Errorf("element %d: %w", i, err)
				}
			}
			return nil
		case vir.StructType:
			// Field-by-field shape checking needs the Struct's field list,
			// which isn't threaded through here; arity/order agreement
			// against the declared struct is left to a later pass.
			if len(x.Elems) == 0 {
				return fmt.Errorf("aggregate initializer must not be empty")
			}
			return nil
		default:
			return fmt.Errorf("aggregate initializer only legal for array/struct types, got %s", t)
		}
	case vir.InitByteString:
		if !isArrayOfI8(t) {
			return fmt.Errorf("byte-string initializer only legal for array[i8, N], got %s", t)
		}
		if arr, ok := t.(vir.ArrayType); ok && arr.Len != len(x.Data) {
			return fmt.Errorf("byte-string initializer has %d bytes, array type wants %d", len(x.Data), arr.Len)
		}
		return nil
	default:
		return fmt.Errorf("unrecognized const-init form %T", init)
	}
}