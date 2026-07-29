// types.go
package msl

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// mapSpace maps a .gvir address space to an MSL one. There is no generic
// pointer on either side, so this is total and never loses information (§5).
func mapSpace(s gvir.AddrSpace) (msl.AddressSpace, error) {
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
	return msl.NoSpace, fmt.Errorf("pointer has no address space (§5)")
}

// bytePtr is the representation of every .gvir pointer value: an
// address-space-qualified byte address. §8.3 makes index.ptr byte arithmetic,
// so this is the type pointer arithmetic is already written against.
func bytePtr(s gvir.AddrSpace) (msl.Type, error) {
	space, err := mapSpace(s)
	if err != nil {
		return nil, err
	}
	return msl.Ptr(space, msl.UChar), nil
}

// mapType maps a .gvir type to the MSL spelling of a value of that type.
func (l *lowerer) mapType(t gvir.Type) (msl.Type, error) {
	switch x := t.(type) {
	case gvir.IntType:
		switch x.Bits {
		case 1:
			return msl.Bool, nil
		case 8:
			return msl.Char, nil
		case 16:
			return msl.Short, nil
		case 32:
			return msl.Int, nil
		case 64:
			return msl.Long, nil
		}
		return nil, fmt.Errorf("no MSL spelling for %s", x)

	case gvir.FloatType:
		switch {
		case x.Brain && x.Bits == 16:
			return msl.BFloat, nil // >= metal31, gated (§4.3)
		case x.Bits == 16:
			return msl.Half, nil
		case x.Bits == 32:
			return msl.Float, nil
		case x.Bits == 64:
			return nil, fmt.Errorf("f64 is unavailable on every msl artifact (§4.2) — gating should have excluded this kernel")
		}
		return nil, fmt.Errorf("no MSL spelling for %s", x)

	case gvir.PtrType:
		return bytePtr(x.Space)

	case gvir.VecType:
		elem, err := l.mapType(x.Elem)
		if err != nil {
			return nil, err
		}
		return msl.Vec(elem, x.Len), nil

	case gvir.ArrayType:
		elem, err := l.mapType(x.Elem)
		if err != nil {
			return nil, err
		}
		return msl.Array(elem, x.Len), nil

	case gvir.StructType:
		s, ok := l.structs[x.Name]
		if !ok {
			return nil, fmt.Errorf("undeclared struct %s", x.Name)
		}
		return s.Type(), nil

	case gvir.SubmaskType:
		// §4.6 keeps the mask opaque because its width is the runtime
		// subgroup width. simd_vote::vote_t is 64 bits on every Apple GPU,
		// which is the widest the type can be, so ulong holds it exactly.
		return msl.ULong, nil

	case gvir.VoidType:
		return nil, nil // msl spells void as a nil Ret
	}
	return nil, fmt.Errorf("no MSL spelling for %s", t)
}

// unsignedTwin returns the MSL type holding the same bits read as unsigned.
// .gvir puts signedness in the opcode, not the type (§11.1), so every u*
// opcode is the signed spelling with as_type on both sides.
func unsignedTwin(t msl.Type) (msl.Type, bool) {
	switch x := t.(type) {
	case msl.ScalarType:
		switch x {
		case msl.Char:
			return msl.UChar, true
		case msl.Short:
			return msl.UShort, true
		case msl.Int:
			return msl.UInt, true
		case msl.Long:
			return msl.ULong, true
		}
	case msl.VecType:
		if e, ok := unsignedTwin(x.Elem); ok {
			return msl.Vec(e, x.N), true
		}
	}
	return nil, false
}

// isVector reports whether the value is a vector, which decides whether the
// logical operators or the bitwise ones are the elementwise spelling.
func isVector(t msl.Type) bool { _, ok := t.(msl.VecType); return ok }

func intBits(t gvir.Type) int {
	if x, ok := gvir.ElemOrSelf(t).(gvir.IntType); ok {
		return x.Bits
	}
	return 0
}

func floatBits(t gvir.Type) int {
	if x, ok := gvir.ElemOrSelf(t).(gvir.FloatType); ok {
		return x.Bits
	}
	return 0
}

// mslSizeAlign is MSL's own layout of a .gvir type, computed independently of
// gvir's §4.7/§6.3 derivation so the two can be compared rather than assumed
// equal (§13 "Layout").
func (l *lowerer) mslSizeAlign(t gvir.Type) (int, int, error) {
	switch x := t.(type) {
	case gvir.IntType:
		if !gvir.IsSInt(x) {
			return 0, 0, fmt.Errorf("i1 has no layout (§4.1)")
		}
		n := x.Bits / 8
		return n, n, nil
	case gvir.FloatType:
		if x.Bits == 64 {
			return 0, 0, fmt.Errorf("f64 has no MSL layout (§4.2)")
		}
		n := x.Bits / 8
		return n, n, nil
	case gvir.PtrType:
		if x.Space == "" {
			return 0, 0, fmt.Errorf("the bare `ptr` word has no layout")
		}
		return 8, 8, nil // §4.1: pointers are 64-bit on every backend
	case gvir.VecType:
		es, _, err := l.mslSizeAlign(x.Elem)
		if err != nil {
			return 0, 0, err
		}
		// MSL rounds a 3-component vector to 4 elements, exactly as §4.4 does.
		n := nextPow2(x.Len) * es
		return n, n, nil
	case gvir.ArrayType:
		es, ea, err := l.mslSizeAlign(x.Elem)
		if err != nil {
			return 0, 0, err
		}
		return es * x.Len, ea, nil
	case gvir.StructType:
		s := l.src.StructByName(x.Name)
		if s == nil {
			return 0, 0, fmt.Errorf("undeclared struct %s", x.Name)
		}
		off, align := 0, 1
		for _, f := range s.Fields {
			fs, fa, err := l.mslSizeAlign(f.Type)
			if err != nil {
				return 0, 0, err
			}
			off = alignUp(off, fa) + fs
			if fa > align {
				align = fa
			}
		}
		return alignUp(off, align), align, nil
	}
	return 0, 0, fmt.Errorf("%s has no MSL layout", t)
}

// storageType picks an MSL type of exactly the requested size and alignment.
// `group` and `alloca` objects are never named directly in the IR — naming one
// yields its address (§8.2, §8.1) — so only the byte count and the alignment
// are observable, and a vector element type is how MSL spells over-alignment.
func storageType(size, align int) (msl.Type, error) {
	var elem msl.Type
	switch align {
	case 0, 1:
		align, elem = 1, msl.UChar
	case 2:
		elem = msl.UShort
	case 4:
		elem = msl.UInt
	case 8:
		elem = msl.Vec(msl.UInt, 2)
	case 16:
		elem = msl.Vec(msl.UInt, 4)
	default:
		return nil, fmt.Errorf("alignment %d exceeds the 16 bytes an MSL vector type can express", align)
	}
	n := alignUp(size, align) / align
	if n < 1 {
		n = 1
	}
	return msl.Array(elem, n), nil
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// ---------------------------------------------------------------------------
// Identifiers
// ---------------------------------------------------------------------------

// mslReserved is the set of MSL and C++ spellings a legal .gvir identifier
// (§2: [A-Za-z_][A-Za-z0-9_]*, no "__") may collide with.
var mslReserved = map[string]bool{
	"kernel": true, "vertex": true, "fragment": true, "device": true,
	"constant": true, "threadgroup": true, "thread": true, "ray_data": true,
	"object_data": true, "half": true, "float": true, "int": true, "uint": true,
	"bool": true, "char": true, "uchar": true, "short": true, "ushort": true,
	"long": true, "ulong": true, "void": true, "bfloat": true, "auto": true,
	"sampler": true, "texture": true, "template": true, "class": true,
	"struct": true, "union": true, "namespace": true, "using": true,
	"return": true, "if": true, "else": true, "for": true, "while": true,
	"do": true, "switch": true, "case": true, "default": true, "break": true,
	"continue": true, "const": true, "static": true, "inline": true,
	"operator": true, "this": true, "true": true, "false": true, "nullptr": true,
	"select": true, "sample": true, "min": true, "max": true, "abs": true,
	"main": true, "metal": true, "atomic_int": true, "atomic_uint": true,
}

type nameTable struct{ taken map[string]bool }

func newNameTable() *nameTable { return &nameTable{taken: map[string]bool{}} }

// ident maps a .gvir identifier to an MSL one. .gvir's namespace is flat with
// no shadowing (§2), so the mapping is injective by construction; only
// reserved words and this backend's own vv_ prefix need escaping.
func (n *nameTable) ident(s string) string {
	out := s
	for mslReserved[out] || strings.HasPrefix(out, "vv_") {
		out += "_"
	}
	n.taken[out] = true
	return out
}

func (n *nameTable) fresh(prefix string) string {
	for i := 0; ; i++ {
		c := fmt.Sprintf("vv_%s%d", prefix, i)
		if !n.taken[c] {
			n.taken[c] = true
			return c
		}
	}
}