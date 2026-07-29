package ptx

import (
	"fmt"

	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Type mapping and the zero-extension invariant's helpers.
//
// A value of type iN lives in a register of width max(16, N) holding the value
// zero-extended: PTX 8-bit registers are usable only by ld, st and cvt, so i8
// is promoted exactly as nvcc does. See README §3.

// regType is the PTX type of the register holding one lane of t.
func regType(t gvir.Type) (ptx.Type, error) {
	switch x := t.(type) {
	case gvir.IntType:
		switch x.Bits {
		case 1:
			return ptx.Pred, nil
		case 8, 16:
			return ptx.U16, nil
		case 32:
			return ptx.U32, nil
		case 64:
			return ptx.U64, nil
		}
	case gvir.FloatType:
		if x.Brain {
			if x.Bits == 16 {
				return ptx.BF16, nil
			}
			break
		}
		switch x.Bits {
		case 16:
			return ptx.F16, nil
		case 32:
			return ptx.F32, nil
		case 64:
			return ptx.F64, nil
		}
	case gvir.PtrType:
		if x.Space != "" {
			return ptx.U64, nil
		}
	case gvir.SubmaskType:
		return ptx.B32, nil
	case gvir.VecType:
		return regType(x.Elem)
	}
	return ptx.NoType, todof("no PTX register type for %s", t)
}

// memType is the PTX type a load or store of t uses. Unlike regType it does
// not promote: i8 moves as .u8 even though it lives in a 16-bit register.
func memType(t gvir.Type) (ptx.Type, error) {
	switch x := t.(type) {
	case gvir.IntType:
		switch x.Bits {
		case 8:
			return ptx.U8, nil
		case 16:
			return ptx.U16, nil
		case 32:
			return ptx.U32, nil
		case 64:
			return ptx.U64, nil
		}
	case gvir.VecType:
		return memType(x.Elem)
	}
	return regType(t)
}

// intType is the PTX integer type for arithmetic on t at the register's width,
// in the requested signedness.
func intType(t gvir.Type, signed bool) (ptx.Type, error) {
	switch regBits(t) {
	case 16:
		if signed {
			return ptx.S16, nil
		}
		return ptx.U16, nil
	case 32:
		if signed {
			return ptx.S32, nil
		}
		return ptx.U32, nil
	case 64:
		if signed {
			return ptx.S64, nil
		}
		return ptx.U64, nil
	}
	return ptx.NoType, todof("no PTX integer type for %s", t)
}

// bitType is the untyped-bits PTX type at the register width of t.
func bitType(t gvir.Type) (ptx.Type, error) {
	switch regBits(t) {
	case 16:
		return ptx.B16, nil
	case 32:
		return ptx.B32, nil
	case 64:
		return ptx.B64, nil
	}
	return ptx.NoType, todof("no PTX bits type for %s", t)
}

// valueBits is the declared width of one lane of t, in bits.
func valueBits(t gvir.Type) int {
	switch x := gvir.ElemOrSelf(t).(type) {
	case gvir.IntType:
		return x.Bits
	case gvir.FloatType:
		return x.Bits
	case gvir.PtrType:
		return 64
	case gvir.SubmaskType:
		return 32
	}
	return 0
}

// regBits is the width of the register one lane of t occupies: valueBits,
// promoted to 16 for i8.
func regBits(t gvir.Type) int {
	n := valueBits(t)
	if n < 16 {
		return 16
	}
	return n
}

// needsMask reports whether results of type t must be re-zero-extended after a
// wrapping operation. Only i8 does: every other width fills its register.
func needsMask(t gvir.Type) bool { return valueBits(t) == 8 && gvir.IsInt(gvir.ElemOrSelf(t)) }

// laneCount is 1 for scalars and N for vec[T,N].
func laneCount(t gvir.Type) int {
	if v, ok := t.(gvir.VecType); ok {
		return v.Len
	}
	return 1
}

// space maps a .gvir address space onto its PTX state space. There is no
// generic pointer in .gvir (§1, §5), so this is total and never lossy.
func space(a gvir.AddrSpace) (ptx.Space, error) {
	switch a {
	case gvir.SpaceGlobal:
		return ptx.Global, nil
	case gvir.SpaceGroup:
		return ptx.Shared, nil
	case gvir.SpacePrivate:
		return ptx.Local, nil
	case gvir.SpaceConstant:
		return ptx.Const, nil
	}
	return ptx.NoSpace, todof("unknown address space %q (§5)", a)
}

// spaceOfPtr extracts the state space from a pointer value's type.
func spaceOfPtr(t gvir.Type) (ptx.Space, error) {
	p, ok := t.(gvir.PtrType)
	if !ok || p.Space == "" {
		return ptx.NoSpace, todof("%s is not a space-qualified pointer (§5)", t)
	}
	return space(p.Space)
}

// isFloatElem reports whether the element type of a suffix is floating point.
func isFloatElem(t gvir.Type) bool { return gvir.IsFloat(gvir.ElemOrSelf(t)) }

// isPredElem reports whether the element type of a suffix is i1.
func isPredElem(t gvir.Type) bool { return gvir.IsBool(gvir.ElemOrSelf(t)) }

func mustRegType(t gvir.Type) ptx.Type {
	rt, err := regType(t)
	if err != nil {
		panic(fmt.Sprintf("gpu/lower/ptx: %v", err))
	}
	return rt
}