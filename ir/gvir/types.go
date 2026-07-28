// types.go
package gvir

import "fmt"

// Type is the interface implemented by all Vertex GPU IR types (§4).
type Type interface {
	String() string
	isType()
}

// AddrSpace is part of every pointer type — there is no generic pointer
// (§1, §5).
type AddrSpace string

const (
	SpaceGlobal   AddrSpace = "global"
	SpaceGroup    AddrSpace = "group"
	SpacePrivate  AddrSpace = "private"
	SpaceConstant AddrSpace = "constant"
)

var CanonicalSpaces = map[AddrSpace]bool{
	SpaceGlobal: true, SpaceGroup: true, SpacePrivate: true, SpaceConstant: true,
}

// IntType covers i1, i8, i16, i32, i64. There is no i128 (§4.1). i1 is
// modelled as an IntType for arithmetic-table uniformity, but it is
// value-only: IsStorable rejects it, and IsSInt is the predicate that means
// "one of the §2 sint-types".
type IntType struct{ Bits int }

// FloatType covers f16, bf16, f32, f64 (§4.1). Bits alone does not identify
// a float type — f16 and bf16 are both 16 bits with different exponent and
// mantissa splits — so Brain is part of identity and Equal compares it.
type FloatType struct {
	Bits  int
	Brain bool // bf16
}

// PtrType is always space-qualified as a value type. The zero value, spelled
// PtrWord, is NOT a value type: it is the bare `ptr` suffix word used by
// index.ptr / field.ptr (§8.3) and the pointer comparisons (§11.4), where
// the space comes from the operand rather than the suffix. IsValueType and
// IsStorable both reject it, so it cannot leak into a binding by accident.
type PtrType struct{ Space AddrSpace }

// VecType is vec[T,N], N in {2,3,4} (§4.4). An Elem of I1 makes it a
// predicate vector (§4.5), producible only by a vector comparison and
// value-only.
type VecType struct {
	Elem Type
	Len  int
}

// StructType names a memory-only aggregate (§4.7). There is no import
// qualifier: the module namespace is flat and there is no cross-module
// linkage in .gvir.
type StructType struct{ Name string }

// ArrayType is array[T,N], memory-only, with no inter-element padding (§4.7).
type ArrayType struct {
	Elem Type
	Len  int
}

// SubmaskType is the opaque per-subgroup lane mask (§4.6). Deliberately
// fieldless: its width is the runtime subgroup width (§9.2), so nothing may
// be written against its layout.
type SubmaskType struct{}

type VoidType struct{}

func (IntType) isType()     {}
func (FloatType) isType()   {}
func (PtrType) isType()     {}
func (VecType) isType()     {}
func (StructType) isType()  {}
func (ArrayType) isType()   {}
func (SubmaskType) isType() {}
func (VoidType) isType()    {}

func (t IntType) String() string { return fmt.Sprintf("i%d", t.Bits) }
func (t FloatType) String() string {
	if t.Brain {
		return fmt.Sprintf("bf%d", t.Bits)
	}
	return fmt.Sprintf("f%d", t.Bits)
}
func (t PtrType) String() string {
	if t.Space == "" {
		return "ptr"
	}
	return fmt.Sprintf("ptr[%s]", t.Space)
}
func (t VecType) String() string    { return fmt.Sprintf("vec[%s,%d]", t.Elem, t.Len) }
func (t StructType) String() string { return "struct " + t.Name }
func (t ArrayType) String() string  { return fmt.Sprintf("array[%s,%d]", t.Elem, t.Len) }
func (SubmaskType) String() string  { return "submask" }
func (VoidType) String() string     { return "void" }

// Canonical singletons.
var (
	I1   = IntType{1}
	I8   = IntType{8}
	I16  = IntType{16}
	I32  = IntType{32}
	I64  = IntType{64}
	F16  = FloatType{Bits: 16}
	BF16 = FloatType{Bits: 16, Brain: true}
	F32  = FloatType{Bits: 32}
	F64  = FloatType{Bits: 64}

	PtrGlobal   = PtrType{SpaceGlobal}
	PtrGroup    = PtrType{SpaceGroup}
	PtrPrivate  = PtrType{SpacePrivate}
	PtrConstant = PtrType{SpaceConstant}
	// PtrWord is the bare `ptr` suffix word (§8.3, §11.4), never a value type.
	PtrWord = PtrType{}

	Submask = SubmaskType{}
	Void    = VoidType{}
)

// Equal reports structural type equality. Address space is part of a
// pointer's identity (§7.3), so ptr[group] and ptr[global] are never Equal
// and neither is ever Equal to the bare suffix word.
func Equal(a, b Type) bool {
	switch x := a.(type) {
	case IntType:
		y, ok := b.(IntType)
		return ok && x.Bits == y.Bits
	case FloatType:
		y, ok := b.(FloatType)
		return ok && x.Bits == y.Bits && x.Brain == y.Brain
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
	case SubmaskType:
		_, ok := b.(SubmaskType)
		return ok
	case VoidType:
		_, ok := b.(VoidType)
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
func IsSubmask(t Type) bool { _, ok := t.(SubmaskType); return ok }
func IsVoid(t Type) bool    { _, ok := t.(VoidType); return ok }

// IsBool reports whether t is i1 (§4.1): comparison results, select
// conditions, br_if operands. Value-only.
func IsBool(t Type) bool { x, ok := t.(IntType); return ok && x.Bits == 1 }

// IsSInt reports whether t is one of the §2 sint-types: i8, i16, i32, i64.
// i1 is excluded on purpose.
func IsSInt(t Type) bool {
	x, ok := t.(IntType)
	return ok && (x.Bits == 8 || x.Bits == 16 || x.Bits == 32 || x.Bits == 64)
}

// IsSpacedPtr reports whether t is a real, space-qualified pointer value type.
func IsSpacedPtr(t Type) bool { x, ok := t.(PtrType); return ok && x.Space != "" }

// IsPtrWord reports whether t is the bare `ptr` suffix word.
func IsPtrWord(t Type) bool { x, ok := t.(PtrType); return ok && x.Space == "" }

// IsPredicateVector reports whether t is vec[i1,N] (§4.5).
func IsPredicateVector(t Type) bool { x, ok := t.(VecType); return ok && IsBool(x.Elem) }

// IsVecElemType reports whether t may be a vector element: a non-i1 scalar
// (§4.4). Pointers are excluded by the §2 vec-elem-type grammar.
func IsVecElemType(t Type) bool { return IsSInt(t) || IsFloat(t) }

// IsAggregate reports whether t is memory-only (§4.7): never held in a
// named value.
func IsAggregate(t Type) bool {
	switch t.(type) {
	case StructType, ArrayType:
		return true
	}
	return false
}

// IsStorable reports whether t is one of the §2 storable types — legal as a
// const type, a struct field, an array element, an alloca/group type, and a
// load/store subject. i1, vec[i1,N] and submask are value-only and excluded.
func IsStorable(t Type) bool {
	switch x := t.(type) {
	case IntType:
		return IsSInt(x)
	case FloatType:
		return true
	case PtrType:
		return x.Space != ""
	case VecType:
		return IsVecElemType(x.Elem)
	case ArrayType:
		return IsStorable(x.Elem)
	case StructType:
		return true
	}
	return false
}

// IsValueType reports whether t may be the type of a named value: a
// parameter, an instruction result, a loop-carried binding. This is the §2
// value-type production — storable scalars and vectors plus i1, vec[i1,N]
// and submask. Aggregates, void and the bare `ptr` word are excluded.
func IsValueType(t Type) bool {
	switch x := t.(type) {
	case IntType:
		return true
	case FloatType:
		return true
	case PtrType:
		return x.Space != ""
	case VecType:
		return IsVecElemType(x.Elem) || IsBool(x.Elem)
	case SubmaskType:
		return true
	}
	return false
}

// IsKernelParamType enforces the §6.2 permitted set: ptr[global],
// ptr[constant], non-i1 integer and float scalars, vectors, structs by
// value. ptr[group], ptr[private], array, submask, i1, vec[i1,N] and void
// are all rejected.
func IsKernelParamType(t Type) bool {
	switch x := t.(type) {
	case IntType:
		return IsSInt(x)
	case FloatType:
		return true
	case PtrType:
		return x.Space == SpaceGlobal || x.Space == SpaceConstant
	case VecType:
		return IsVecElemType(x.Elem)
	case StructType:
		return true
	}
	return false
}

// IsFuncParamType reports whether t is legal as a `func` parameter (§2:
// param takes a value-type). Pointers in any space are permitted; the space
// is part of the signature (§5).
func IsFuncParamType(t Type) bool { return IsValueType(t) }

// IsFuncReturnType reports whether t is legal as a `func` return type
// (§6.4): any value type or void, never struct or array.
func IsFuncReturnType(t Type) bool { return IsValueType(t) || IsVoid(t) }

// IsBitcastType reports whether t may be a bitcast source or destination
// (§11.5). i1, submask and vec[i1,N] are illegal. The equal-bit-width rule
// and the differing-address-space rule are two-operand checks and live in
// ir/verify.
func IsBitcastType(t Type) bool {
	switch x := t.(type) {
	case IntType:
		return IsSInt(x)
	case FloatType:
		return true
	case PtrType:
		return x.Space != ""
	case VecType:
		return IsVecElemType(x.Elem)
	}
	return false
}

// IsAtomicType reports the types every atomic accepts (§10.2): i32, i64 and
// pointers. Narrower integers are not atomic types on any backend, which is
// why the atomic opcodes carry no plain operandConstraint (opcode.go).
func IsAtomicType(t Type) bool {
	switch x := t.(type) {
	case IntType:
		return x.Bits == 32 || x.Bits == 64
	case PtrType:
		return x.Space != ""
	}
	return false
}

// IsAtomicAddType extends IsAtomicType with the float forms atomic_add
// alone accepts (§10.2): f32 always, f64 where §4.3 makes it available.
func IsAtomicAddType(t Type) bool {
	if IsAtomicType(t) {
		return true
	}
	x, ok := t.(FloatType)
	return ok && !x.Brain && (x.Bits == 32 || x.Bits == 64)
}

// ElemOrSelf returns the element type for vectors and t otherwise — the
// shape every "iN or vec[iN,N]" opcode legality check wants.
func ElemOrSelf(t Type) Type {
	if v, ok := t.(VecType); ok {
		return v.Elem
	}
	return t
}