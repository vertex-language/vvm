// typefix.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// typeFunc computes each named value's fixed type in one forward pass (§4.3:
// the first assignment, parameters included, fixes a name's type). It also
// returns the value-definition order, used to assign local slots.
func typeFunc(l *Layout, f *vir.Function) (map[string]vir.Type, []string, error) {
	types := map[string]vir.Type{}
	var order []string

	fix := func(name string, t vir.Type) error {
		if name == "" {
			return nil
		}
		if _, ok := types[name]; ok {
			return nil // already fixed at first assignment
		}
		if err := checkValueType(t); err != nil {
			return err
		}
		types[name] = t
		order = append(order, name)
		return nil
	}

	for _, p := range f.Params {
		pt := p.Type
		if p.ByVal != "" || p.SRet != "" {
			pt = vir.Ptr // byval/sret params arrive as pointers
		}
		if err := fix(p.Name, pt); err != nil {
			return nil, nil, err
		}
	}

	for _, b := range f.AllBlocks() {
		for _, in := range b.Lines {
			if in.Result == "" {
				continue
			}
			t, err := resultType(l, f, in)
			if err != nil {
				return nil, nil, err
			}
			if err := fix(in.Result, t); err != nil {
				return nil, nil, err
			}
		}
	}
	return types, order, nil
}

// resultType derives an instruction's result type. For the memory-model
// backend the fine points (element-vs-vector, special opcodes) are handled
// where they matter; the common rule is "result == Suffix", with a few
// opcodes fixed to ptr / i1.
func resultType(l *Layout, f *vir.Function, in *vir.Instruction) (vir.Type, error) {
	switch in.Op {
	case vir.OpField, vir.OpIndex, vir.OpAlloca:
		if _, ok := in.Suffix.(vir.ValistType); ok {
			return vir.Valist, nil
		}
		return vir.Ptr, nil
	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge,
		vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO, vir.OpUMulO, vir.OpSMulO:
		return vir.I1, nil
	case vir.OpCall, vir.OpSyscall:
		if in.Suffix != nil {
			return in.Suffix, nil
		}
		return vir.I64, nil // rax-width default for an untyped call result
	}
	if in.Suffix != nil {
		return in.Suffix, nil
	}
	return vir.I64, nil
}