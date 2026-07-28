// types.go
package gvir

import "fmt"

// Type is the interface implemented by every GPU IR type (§4).
//
// Deliberate divergences from vir's type system (gvir_arch.md §9):
// pointers are address-space qualified and never generic; floats are keyed
// by kind rather than bit width, because f16 and bf16 are both 16 bits with
// different semantics and different gating (§4.2, §4.3); there is no i128
// and no valist; submask (§4.6) is new.
type Type interface {
	String() string
	isType()
}

// IntType is i1 (boolean), i8, i16, i32, i64. No i128 (§4.1).
type IntType struct{ Bits int }

// FloatKind names a float type. Width alone is not a key here (§4.2).
type FloatKind uint8

const (
	KindF16 FloatKind = iota
	KindBF16
	KindF32
	KindF64
)

type FloatType struct{ Kind FloatKind }

// Bits is the storage width. f16 and bf16 share it; Kind is what
// distinguishes them.
func (t FloatType) Bits() int {
	switch t.Kind {
	case KindF16, KindBF16:
		return 16
	case KindF32:
		return 32
	}
	return 64
}

// AddrSpace is the address space baked into every pointer type (§5). It is
// part of the type, so it is part of a name's permanent binding under the
// Join Convention (§7.3 rule 2).
type AddrSpace string

const (
	SpaceGlobal   AddrSpace = "global"
	SpaceGroup    AddrSpace = "group"
	SpacePrivate  AddrSpace = "private"
	SpaceConstant AddrSpace = "constant"

	// SpaceNone is the unqualified pointer carried by the bare `.ptr`
	// *ident* suffix — `eq.ptr`, `index.ptr`, `field.ptr` (§8.3, §11.4).
	// The grammar admits an ident suffix alongside a type suffix
	// (§2, `op := ident ("." (ident | type))?`), which is what those
	// spellings are: there is no space-less pointer *type* in v1, and a
	// PtrType carrying SpaceNone is never a legal value type.
	SpaceNone AddrSpace = ""
)

type PtrType struct{ Space AddrSpace }

type VecType struct { // vec[T, N], N in {2,3,4} (§4.4)
	Elem Type
	Len  int
}

// StructType names a struct (§4.7). Memory-only: never held in a named
// value. There is no cross-module import qualifier — a .gvir module is a
// single translation unit with a flat namespace (§2).
type StructType struct{ Name string }

type ArrayType struct { // array[T, N] (§4.7), memory-only
	Elem Type
	Len  int
}

type VoidType struct{}

// SubmaskType is the opaque per-subgroup lane mask (§4.6). Produced only by
// ballot, consumed only by mask_* ops. Like ValistType in vir it has no
// fields on purpose: nothing may be written against its layout, and unlike
// vir's valist it is a real value type — it may be a func parameter,
// a return type, and a select arm (§11.7).
type SubmaskType struct{}

func (IntType) isType()     {}
func (FloatType) isType()   {}
func (PtrType) isType()     {}
func (VecType) isType()     {}
func (StructType) isType()  {}
func (ArrayType) isType()   {}
func (VoidType) isType()    {}
func (SubmaskType) isType() {}

func (t IntType) String() string { return fmt.Sprintf("i%d", t.Bits) }
func (t FloatType) String() string {
	switch t.Kind {
	case KindF16:
		return "f16"
	case KindBF16:
		return "bf16"
	case KindF32:
		return "f32"
	case KindF64:
		return "f64"
	}
	return "f?"
}
func (t PtrType) String() string {
	if t.Space == SpaceNone {
		return "ptr"
	}
	return fmt.Sprintf("ptr[%s]", t.Space)
}
func (t VecType) String() string    { return fmt.Sprintf("vec[%s, %d]", t.Elem, t.Len) }
func (t StructType) String() string { return "struct " + t.Name }
func (t ArrayType) String() string  { return fmt.Sprintf("array[%s, %d]", t.Elem, t.Len) }
func (VoidType) String() string     { return "void" }
func (SubmaskType) String() string  { return "submask" }

// Canonical singletons.
var (
	I1   = IntType{1}
	I8   = IntType{8}
	I16  = IntType{16}
	I32  = IntType{32}
	I64  = IntType{64}
	F16  = FloatType{KindF16}
	BF16 = FloatType{KindBF16}
	F32  = FloatType{KindF32}
	F64  = FloatType{KindF64}

	PtrGlobal   = PtrType{SpaceGlobal}
	PtrGroup    = PtrType{SpaceGroup}
	PtrPrivate  = PtrType{SpacePrivate}
	PtrConstant = PtrType{SpaceConstant}
	// AnyPtr is the bare `.ptr` opcode suffix, not a value type.
	AnyPtr = PtrType{SpaceNone}

	Void    = VoidType{}
	Submask = SubmaskType{}
)

func Vec(elem Type, n int) VecType { return VecType{Elem: elem, Len: n} }

// Equal reports structural type equality. Address space participates: a
// ptr[group] is never Equal to a ptr[global] (§5, §7.3 rule 2).
func Equal(a, b Type) bool {
	switch x := a.(type) {
	case IntType:
		y, ok := b.(IntType)
		return ok && x.Bits == y.Bits
	case FloatType:
		y, ok := b.(FloatType)
		return ok && x.Kind == y.Kind
	case PtrType:
		y, ok := b.(PtrType)
		return ok && x.Space == y.Space
	case VecType:
		y, ok := b.(VecType)
		return ok && x.Len == y.Len && Equal(x.Elem, y.Elem)
	case StructType:
		y, ok := b.(StructType)
		return ok && x.Name == y.Name
	case ArrayType:
		y, ok := b.(ArrayType)
		return ok && x.Len == y.Len && Equal(x.Elem, y.Elem)
	case VoidType:
		_, ok := b.(VoidType)
		return ok
	case SubmaskType:
		_, ok := b.(SubmaskType)
		return ok
	}
	return false
}

func IsInt(t Type) bool     { _, ok := t.(IntType); return ok }
func IsFloat(t Type) bool   { _, ok := t.(FloatType); return ok }
func IsPtr(t Type) bool     { _, ok := t.(PtrType); return ok }
func IsVec(t Type) bool     { _, ok := t.(VecType); return ok }
func IsStruct(t Type) bool  { _, ok := t.(StructType); return ok }
func IsArray(t Type) bool   { _, ok := t.(ArrayType); return ok }
func IsVoid(t Type) bool    { _, ok := t.(VoidType); return ok }
func IsSubmask(t Type) bool { _, ok := t.(SubmaskType); return ok }

// SpaceOf returns a pointer's address space, ok false for non-pointers.
func SpaceOf(t Type) (AddrSpace, bool) {
	p, ok := t.(PtrType)
	if !ok {
		return SpaceNone, false
	}
	return p.Space, true
}

// IsPredVec reports whether t is vec[i1,N] (§4.5) — producible only by a
// vector comparison, value-only, unreachable from the `type` production.
func IsPredVec(t Type) bool {
	v, ok := t.(VecType)
	return ok && IsInt(v.Elem) && v.Elem.(IntType).Bits == 1
}

// IsVecElem reports whether t may be a vec[T,N] element (§4.4): a non-i1
// scalar. i1 elements arise only via comparison results (§4.5).
func IsVecElem(t Type) bool {
	switch x := t.(type) {
	case IntType:
		return x.Bits != 1
	case FloatType:
		return true
	}
	return false
}

// IsAggregate reports whether t is memory-only (§4.7): never a named value.
func IsAggregate(t Type) bool {
	switch t.(type) {
	case StructType, ArrayType:
		return true
	}
	return false
}

// IsValueType reports whether t may be the type of a named value. Predicate
// vectors and submask qualify — they are value-*only* (§4.5, §4.6), which
// is the opposite restriction from aggregates.
func IsValueType(t Type) bool { return !IsAggregate(t) && !IsVoid(t) }

// IsStorable reports whether t may be loaded, stored, memset through, or
// bitcast — i.e. has a defined representation in memory (§4.5, §4.6, §4.7).
func IsStorable(t Type) bool {
	return IsValueType(t) && !IsPredVec(t) && !IsSubmask(t)
}

// IsTransient reports whether a binding of type t may not cross a barrier
// (§7.3 rule 5).
func IsTransient(t Type) bool { return IsPredVec(t) || IsSubmask(t) }

// IsKernelParamType reports the §6.2 permitted set: ptr[global],
// ptr[constant], integer and float scalars, vectors, structs by value.
func IsKernelParamType(t Type) bool {
	switch x := t.(type) {
	case IntType, FloatType, StructType:
		return true
	case VecType:
		return !IsPredVec(x)
	case PtrType:
		return x.Space == SpaceGlobal || x.Space == SpaceConstant
	}
	return false
}

// IsFuncRetType reports the §6.4 return set: scalars, vec, vec[i1,N],
// ptr[*], submask, void. Never struct or array.
func IsFuncRetType(t Type) bool {
	if IsAggregate(t) {
		return false
	}
	if p, ok := t.(PtrType); ok {
		return p.Space != SpaceNone
	}
	return true
}

// IsMemoryDeclType reports whether t may be an alloca (§8.1), group
// declaration (§8.2), const (§2), or pointee type: anything statically
// sized with a memory representation.
func IsMemoryDeclType(t Type) bool { return IsStorable(t) || IsAggregate(t) }

// IsPaddingLane reports whether k names the padding element of a width-3
// vector, which extract/insert reject (§4.4).
func IsPaddingLane(t VecType, k int) bool { return t.Len == 3 && k == 3 }

// ElemOrSelf returns the element type for vectors, t otherwise — for
// "iN or vec[iN,W]" opcode legality checks.
func ElemOrSelf(t Type) Type {
	if v, ok := t.(VecType); ok {
		return v.Elem
	}
	return t
}