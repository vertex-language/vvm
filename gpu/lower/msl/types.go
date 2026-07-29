// types.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// mslSpace maps a .gvir address space to its MSL qualifier. There is no
// generic pointer in either language, so this is total and never defaults.
func mslSpace(s gvir.AddrSpace) (msl.AddressSpace, error) {
	switch s {
	case gvir.SpaceGlobal:
		return msl.Device, nil
	case gvir.SpaceConstant:
		return msl.Constant, nil
	case gvir.SpaceGroup:
		return msl.Threadgroup, nil
	case gvir.SpacePrivate:
		return msl.Thread, nil
	}
	return "", fmt.Errorf("pointer has no address space (§5)")
}

// typeOf maps a .gvir type to its MSL spelling.
//
// Integers take the *unsigned* spelling: the representation invariant is that
// an iN value is held zero-extended in the unsigned MSL type of that width, and
// signed consumers cast (see signedTypeOf).
func (l *lowerer) typeOf(t gvir.Type) (msl.Type, error) {
	switch x := t.(type) {
	case gvir.IntType:
		return intType(x.Bits, false)

	case gvir.FloatType:
		switch {
		case x.Brain && x.Bits == 16:
			return msl.BFloat, nil // >= metal31, gated in §4.3
		case x.Bits == 16:
			return msl.Half, nil
		case x.Bits == 32:
			return msl.Float, nil
		case x.Bits == 64:
			return nil, fmt.Errorf("f64 is unavailable on every msl artifact (§4.2)")
		}
		return nil, fmt.Errorf("%s is not a float type", x)

	case gvir.PtrType:
		space, err := mslSpace(x.Space)
		if err != nil {
			return nil, err
		}
		// .gvir pointers carry no pointee type; every access reinterpret_casts.
		return msl.Ptr(space, msl.UChar), nil

	case gvir.VecType:
		elem, err := l.typeOf(x.Elem)
		if err != nil {
			return nil, err
		}
		return msl.Vec(elem, x.Len), nil

	case gvir.StructType:
		return msl.Named(x.Name), nil

	case gvir.ArrayType:
		elem, err := l.typeOf(x.Elem)
		if err != nil {
			return nil, err
		}
		return msl.Array(elem, x.Len), nil

	case gvir.SubmaskType:
		return msl.Named(submaskName), nil

	case gvir.VoidType:
		return msl.Void, nil
	}
	return nil, fmt.Errorf("type %s has no MSL spelling", t)
}

// signedTypeOf is typeOf with the signed integer spelling, for the operations
// that read the same bits as a signed value.
func (l *lowerer) signedTypeOf(t gvir.Type) (msl.Type, error) {
	switch x := t.(type) {
	case gvir.IntType:
		return intType(x.Bits, true)
	case gvir.VecType:
		elem, err := l.signedTypeOf(x.Elem)
		if err != nil {
			return nil, err
		}
		return msl.Vec(elem, x.Len), nil
	}
	return l.typeOf(t)
}

func intType(bits int, signed bool) (msl.Type, error) {
	switch bits {
	case 1:
		return msl.Bool, nil // value-only (§4.1); never storable
	case 8:
		if signed {
			return msl.Char, nil
		}
		return msl.UChar, nil
	case 16:
		if signed {
			return msl.Short, nil
		}
		return msl.UShort, nil
	case 32:
		if signed {
			return msl.Int, nil
		}
		return msl.UInt, nil
	case 64:
		if signed {
			return msl.Long, nil
		}
		return msl.ULong, nil
	}
	return nil, fmt.Errorf("i%d is not a .gvir integer type (§4.1)", bits)
}

// intBits reports the width of an integer (or integer-vector element) type.
func intBits(t gvir.Type) (int, bool) {
	x, ok := gvir.ElemOrSelf(t).(gvir.IntType)
	return x.Bits, ok
}