// typefix.go
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// typeFunc computes each named value's fixed type in one forward pass (§4.3:
// the first assignment, parameters included, fixes a name's type
// permanently) and returns the definition order used to assign local slots.
//
// The module is verified, so this pass records rather than checks: a name
// reassigned at a different type is upstream's bug, and disagreement here is
// reported as one.
func typeFunc(ix *index, f *vir.Function) (map[string]vir.Type, []string, error) {
	types := map[string]vir.Type{}
	var order []string

	fix := func(name string, t vir.Type) error {
		if name == "" {
			return nil
		}
		if prev, ok := types[name]; ok {
			if !vir.Equal(prev, t) {
				return fmt.Errorf("value %s is fixed at %s but reassigned at %s (§4.3)", name, prev, t)
			}
			return nil
		}
		types[name] = t
		order = append(order, name)
		return nil
	}

	for _, p := range f.Params {
		if err := fix(p.Name, p.Type); err != nil {
			return nil, nil, err
		}
	}
	for _, b := range f.AllBlocks() {
		for _, in := range b.Lines {
			if in.Op == vir.OpLoc || in.Result == "" {
				continue
			}
			t, err := resultType(ix, types, in)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", in.Op, err)
			}
			if t == nil || vir.IsVoid(t) {
				continue
			}
			if err := fix(in.Result, t); err != nil {
				return nil, nil, err
			}
		}
	}
	return types, order, nil
}

// resultType derives one instruction's result type. Most opcodes take it
// straight from the suffix; the rest are enumerated.
func resultType(ix *index, types map[string]vir.Type, in *vir.Instruction) (vir.Type, error) {
	switch in.Op {
	// Comparisons and overflow predicates yield i1 (vec[i1,N] for a vector
	// suffix, which does not lower here).
	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge,
		vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe,
		vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO, vir.OpUMulO, vir.OpSMulO:
		if vir.IsVec(in.Suffix) {
			return nil, todo("vector comparison")
		}
		return vir.I1, nil

	case vir.OpAlloca:
		if vir.IsValist(in.Suffix) {
			return vir.Valist, nil
		}
		return vir.Ptr, nil

	case vir.OpField, vir.OpIndex:
		return vir.Ptr, nil

	case vir.OpCall:
		if in.Sig != "" {
			sig, ok := ix.sigs[in.Sig]
			if !ok {
				return nil, fmt.Errorf("undeclared fnsig %s", in.Sig)
			}
			return sig.Ret, nil
		}
		if len(in.Args) == 0 || in.Args[0].Kind != vir.OperandIdent {
			return nil, fmt.Errorf("call has no callee operand")
		}
		if in.Args[0].IsQualified() {
			return nil, fmt.Errorf("qualified callee %s: importer.Rewrite has not run", in.Args[0])
		}
		c, ok := ix.funcs[in.Args[0].Ident]
		if !ok {
			return nil, fmt.Errorf("undeclared callee %s", in.Args[0].Ident)
		}
		return c.ret, nil

	case vir.OpSyscall, vir.OpVaArg:
		return in.Suffix, nil

	case vir.OpStore, vir.OpStoreVol, vir.OpMemcopy, vir.OpMemmove, vir.OpMemset,
		vir.OpAtomicStore, vir.OpFence, vir.OpPrefetch, vir.OpMaskedStore, vir.OpScatter,
		vir.OpVaStart, vir.OpVaEnd, vir.OpLoc:
		return vir.Void, nil

	case vir.OpMin, vir.OpMax:
		if !vir.IsFloat(vir.ElemOrSelf(in.Suffix)) {
			return nil, fmt.Errorf("min/max are illegal on integers; use smin/smax/umin/umax (§9.17)")
		}
		return in.Suffix, nil

	case vir.OpExtract:
		v, ok := in.Suffix.(vir.VecType)
		if !ok {
			return nil, fmt.Errorf("extract needs a vector suffix")
		}
		return v.Elem, nil

	case vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax,
		vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		v, ok := in.Suffix.(vir.VecType)
		if !ok {
			return nil, fmt.Errorf("reduction needs a vector suffix")
		}
		return v.Elem, nil
	}

	if in.Suffix == nil {
		return nil, fmt.Errorf("instruction has no type suffix")
	}
	return in.Suffix, nil
}

// typeOfOperand reports the fixed type of an operand in a context expecting
// hint. Literals take the hint; idents take their fixed type, or the type of
// the global/const they name.
func (s *sel) typeOfOperand(o vir.Operand, hint vir.Type) vir.Type {
	if o.Kind != vir.OperandIdent {
		return hint
	}
	if t, ok := s.types[o.Ident]; ok {
		return t
	}
	if c, ok := s.ix.consts[o.Ident]; ok {
		return c.Type
	}
	if _, ok := s.ix.globals[o.Ident]; ok {
		return vir.Ptr
	}
	if _, ok := s.ix.funcs[o.Ident]; ok {
		return vir.Ptr
	}
	return hint
}