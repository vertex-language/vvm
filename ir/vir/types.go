// types.go
package vir

import "fmt"

// Type is the interface implemented by all Vertex IR types (§2).
type Type interface {
	String() string
	isType()
}

type IntType struct{ Bits int }   // i1, i8, ..., i128
type FloatType struct{ Bits int } // f16, f32, f64
type PtrType struct{}             // untyped pointer
type VoidType struct{}            // void
type VecType struct {             // vec[T, N]
	Elem Type
	Len  int
}
type StructType struct{ Name string } // struct <ident> (memory-only)
type ArrayType struct {               // array[T, N] (memory-only)
	Elem Type
	Len  int
}

func (IntType) isType()    {}
func (FloatType) isType()  {}
func (PtrType) isType()    {}
func (VoidType) isType()   {}
func (VecType) isType()    {}
func (StructType) isType() {}
func (ArrayType) isType()  {}

func (t IntType) String() string    { return fmt.Sprintf("i%d", t.Bits) }
func (t FloatType) String() string  { return fmt.Sprintf("f%d", t.Bits) }
func (PtrType) String() string      { return "ptr" }
func (VoidType) String() string     { return "void" }
func (t VecType) String() string    { return fmt.Sprintf("vec[%s, %d]", t.Elem, t.Len) }
func (t StructType) String() string { return "struct " + t.Name }
func (t ArrayType) String() string  { return fmt.Sprintf("array[%s, %d]", t.Elem, t.Len) }

// Canonical singletons for the common scalars.
var (
	I1   = IntType{1}
	I8   = IntType{8}
	I16  = IntType{16}
	I32  = IntType{32}
	I64  = IntType{64}
	I128 = IntType{128}
	F16  = FloatType{16}
	F32  = FloatType{32}
	F64  = FloatType{64}
	Ptr  = PtrType{}
	Void = VoidType{}
)

// Equal reports structural type equality.
func Equal(a, b Type) bool {
	switch x := a.(type) {
	case IntType:
		y, ok := b.(IntType)
		return ok && x.Bits == y.Bits
	case FloatType:
		y, ok := b.(FloatType)
		return ok && x.Bits == y.Bits
	case PtrType:
		_, ok := b.(PtrType)
		return ok
	case VoidType:
		_, ok := b.(VoidType)
		return ok
	case VecType:
		y, ok := b.(VecType)
		return ok && x.Len == y.Len && Equal(x.Elem, y.Elem)
	case StructType:
		y, ok := b.(StructType)
		return ok && x.Name == y.Name
	case ArrayType:
		y, ok := b.(ArrayType)
		return ok && x.Len == y.Len && Equal(x.Elem, y.Elem)
	}
	return false
}

func IsInt(t Type) bool   { _, ok := t.(IntType); return ok }
func IsFloat(t Type) bool { _, ok := t.(FloatType); return ok }
func IsPtr(t Type) bool   { _, ok := t.(PtrType); return ok }
func IsVoid(t Type) bool  { _, ok := t.(VoidType); return ok }
func IsVec(t Type) bool   { _, ok := t.(VecType); return ok }

// IsAggregate reports whether t is memory-only (§2): never a named value.
func IsAggregate(t Type) bool {
	switch t.(type) {
	case StructType, ArrayType:
		return true
	}
	return false
}

// IsValueType reports whether t may be the type of a named value.
func IsValueType(t Type) bool {
	return !IsAggregate(t) && !IsVoid(t)
}

// IsScalarType reports whether t is a bare register-class scalar (iN / fN /
// ptr) — used for syscall operand legality (§9.33).
func IsScalarType(t Type) bool {
	switch t.(type) {
	case IntType, FloatType, PtrType:
		return true
	}
	return false
}

// ElemOrSelf returns the element type for vectors, t otherwise. Handy for
// "iN or vec[iN, W]" opcode legality checks.
func ElemOrSelf(t Type) Type {
	if v, ok := t.(VecType); ok {
		return v.Elem
	}
	return t
}