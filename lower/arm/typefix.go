// typefix.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// typeFunc computes each named value's fixed type in one forward pass (per
// §4.3 — the first assignment, parameters included, fixes a name's type
// permanently) and returns the definition order used to assign slots.
func (x *index) typeFunc(f *vir.Function) (map[string]vir.Type, []string, error) {
	types := map[string]vir.Type{}
	var order []string
	define := func(name string, t vir.Type) error {
		if name == "" {
			return nil
		}
		if _, seen := types[name]; seen {
			return nil // type already fixed; verify checked consistency
		}
		if t == nil {
			return fmt.Errorf("%s: instruction produces a value of no type", name)
		}
		types[name] = t
		order = append(order, name)
		return nil
	}

	for _, p := range f.Params {
		t := p.Type
		if p.ByVal != "" {
			t = vir.Ptr // the callee sees the copy's address
		}
		if err := define(p.Name, t); err != nil {
			return nil, nil, err
		}
	}
	for _, b := range f.AllBlocks() {
		for _, in := range b.Lines {
			if in.Result == "" {
				continue
			}
			t, err := x.resultType(in)
			if err != nil {
				return nil, nil, fmt.Errorf("%s = %s: %w", in.Result, in.Op, err)
			}
			if err := define(in.Result, t); err != nil {
				return nil, nil, err
			}
		}
	}
	return types, order, nil
}

// resultType derives an instruction's result type. ir/verify owns the
// authoritative rules; this is the subset a backend needs to size a slot,
// and it deliberately does not reach into vir's internal opTable.
func (x *index) resultType(in *vir.Instruction) (vir.Type, error) {
	switch in.Op {
	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge,
		vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe,
		vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO,
		vir.OpUMulO, vir.OpSMulO:
		if vir.IsVec(in.Suffix) {
			return nil, todo("vector comparisons")
		}
		return vir.I1, nil

	case vir.OpField, vir.OpIndex:
		return vir.Ptr, nil

	case vir.OpAlloca:
		return in.Suffix, nil // ptr, or valist for alloca.valist

	case vir.OpCall:
		return x.calleeReturn(in)

	case vir.OpExtract:
		return vir.ElemOrSelf(in.Suffix), nil

	case vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax,
		vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		return vir.ElemOrSelf(in.Suffix), nil
	}

	if in.Suffix == nil {
		return nil, fmt.Errorf("no type suffix to derive a result from")
	}
	return in.Suffix, nil
}

// calleeReturn resolves a call's result type: an fnsig's for an indirect
// call, the declared return type for a direct one.
func (x *index) calleeReturn(in *vir.Instruction) (vir.Type, error) {
	if in.Sig != "" {
		s, ok := x.sigs[in.Sig]
		if !ok {
			return nil, fmt.Errorf("no fnsig %q", in.Sig)
		}
		return s.Ret, nil
	}
	if len(in.Args) == 0 || in.Args[0].Kind != vir.OperandIdent {
		return nil, fmt.Errorf("call has no callee identifier")
	}
	name := in.Args[0].Ident
	if in.Args[0].Qualifier != "" {
		return nil, fmt.Errorf("unrewritten cross-module call %s.%s (importer.Rewrite must run first)",
			in.Args[0].Qualifier, name)
	}
	if f, ok := x.funcs[name]; ok {
		return f.Ret, nil
	}
	if e, ok := x.externs[name]; ok {
		return e.Ret, nil
	}
	return nil, fmt.Errorf("no function %q", name)
}

// calleeParams returns a callee's declared parameters and whether it is
// variadic, for argument layout.
func (x *index) calleeParams(in *vir.Instruction) ([]vir.Param, bool, error) {
	if in.Sig != "" {
		s, ok := x.sigs[in.Sig]
		if !ok {
			return nil, false, fmt.Errorf("no fnsig %q", in.Sig)
		}
		ps := make([]vir.Param, len(s.Params))
		for i, t := range s.Params {
			ps[i] = vir.Param{Type: t}
		}
		return ps, s.Variadic, nil
	}
	name := in.Args[0].Ident
	if f, ok := x.funcs[name]; ok {
		return f.Params, f.Variadic, nil
	}
	if e, ok := x.externs[name]; ok {
		return e.Params, e.Variadic, nil
	}
	return nil, false, fmt.Errorf("no function %q", name)
}