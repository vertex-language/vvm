// consts.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// lowerConsts emits module-scope consts as `constant` variables. §2 orders
// consts after structs and before funcs, which is also MSL's declare-before-use
// order, so the ordered Module.Decls list needs no sorting.
func (l *lowerer) lowerConsts() error {
	for _, c := range l.src.Constants {
		ok, err := l.available(c.Type)
		if err != nil {
			return err
		}
		if !ok {
			l.out.Add(&msl.CommentDecl{Text: fmt.Sprintf(
				"const %s omitted: %s is unavailable on %s (§4.3)", c.Name, c.Type, l.arch)})
			continue
		}
		t, err := l.typeOf(c.Type)
		if err != nil {
			return fmt.Errorf("const %s: %w", c.Name, err)
		}
		init, err := l.constInit(c.Init, c.Type)
		if err != nil {
			return fmt.Errorf("const %s: %w", c.Name, err)
		}
		l.out.Constant(t, c.Name, init)
	}
	return nil
}

func (l *lowerer) constInit(i gvir.ConstInit, t gvir.Type) (msl.Expr, error) {
	switch x := i.(type) {
	case gvir.InitZero:
		return msl.Init(), nil // `= {}` zero-initializes scalars and aggregates alike

	case gvir.InitLiteral:
		return l.constLiteral(x.Value, t)

	case gvir.InitAggregate:
		elems := make([]msl.Expr, 0, len(x.Elems))
		for k, e := range x.Elems {
			et, err := l.elemType(t, k)
			if err != nil {
				return msl.Expr{}, err
			}
			ex, err := l.constInit(e, et)
			if err != nil {
				return msl.Expr{}, err
			}
			elems = append(elems, ex)
		}
		return msl.Init(elems...), nil
	}
	return msl.Expr{}, fmt.Errorf("unknown const-init form %T", i)
}

// elemType is the type of an aggregate initializer's k'th element.
func (l *lowerer) elemType(t gvir.Type, k int) (gvir.Type, error) {
	switch x := t.(type) {
	case gvir.ArrayType:
		return x.Elem, nil
	case gvir.VecType:
		return x.Elem, nil
	case gvir.StructType:
		s := l.src.StructByName(x.Name)
		if s == nil {
			return nil, fmt.Errorf("undeclared struct %s", x.Name)
		}
		if k >= len(s.Fields) {
			return nil, fmt.Errorf("struct %s has %d fields, initializer names %d", x.Name, len(s.Fields), k+1)
		}
		return s.Fields[k].Type, nil
	}
	return nil, fmt.Errorf("%s is not an aggregate and takes no aggregate initializer", t)
}

func (l *lowerer) constLiteral(o gvir.Operand, t gvir.Type) (msl.Expr, error) {
	elem := gvir.ElemOrSelf(t)
	if ft, ok := elem.(gvir.FloatType); ok {
		if o.Kind == gvir.OperandInt {
			return msl.Raw(gvir.FormatFloatBits(float64(o.Int), ft.Bits)), nil
		}
		return floatLiteral(o, ft.Bits), nil
	}
	switch o.Kind {
	case gvir.OperandInt:
		if o.Int < 0 {
			return msl.I(o.Int), nil
		}
		return msl.U(uint64(o.Int)), nil
	case gvir.OperandBool:
		return msl.B(o.Bool), nil
	case gvir.OperandNull:
		return msl.Name("nullptr"), nil
	}
	return msl.Expr{}, fmt.Errorf("%s is not a legal const initializer", o)
}