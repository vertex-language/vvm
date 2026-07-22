// types.go
package vir

import "fmt"

// Type is the interface implemented by all Vertex IR types (§2, §3).
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

// StructType names a struct (memory-only). Import is "" for a struct
// declared in this module; otherwise it's the import path (§7.3) the
// struct's shape came from, and lookups go through that ModuleShape
// instead of the local Module.Structs table (see vmeta.go).
type StructType struct {
	Name   string
	Import string
}

type ArrayType struct { // array[T, N] (memory-only)
	Elem Type
	Len  int
}

// ValistType is the opaque, target-defined-layout in-progress variadic
// argument cursor (§3, §4.5). Deliberately has no fields: its shape is
// unspecified on purpose so nothing can be written against its layout.
// Legal only as an alloca result and a va_start/va_arg/va_end operand —
// never a struct field, array element, global/const type, or ordinary
// function parameter type (checked in verify.go, not here).
type ValistType struct{}

func (IntType) isType()    {}
func (FloatType) isType()  {}
func (PtrType) isType()    {}
func (VoidType) isType()   {}
func (VecType) isType()    {}
func (StructType) isType() {}
func (ArrayType) isType()  {}
func (ValistType) isType() {}

func (t IntType) String() string   { return fmt.Sprintf("i%d", t.Bits) }
func (t FloatType) String() string { return fmt.Sprintf("f%d", t.Bits) }
func (PtrType) String() string     { return "ptr" }
func (VoidType) String() string    { return "void" }
func (t VecType) String() string   { return fmt.Sprintf("vec[%s, %d]", t.Elem, t.Len) }
func (t StructType) String() string {
	if t.Import != "" {
		return fmt.Sprintf("struct %s.%s", t.Import, t.Name)
	}
	return "struct " + t.Name
}
func (t ArrayType) String() string { return fmt.Sprintf("array[%s, %d]", t.Elem, t.Len) }
func (ValistType) String() string  { return "valist" }

// Canonical singletons for the common scalars.
var (
	I1     = IntType{1}
	I8     = IntType{8}
	I16    = IntType{16}
	I32    = IntType{32}
	I64    = IntType{64}
	I128   = IntType{128}
	F16    = FloatType{16}
	F32    = FloatType{32}
	F64    = FloatType{64}
	Ptr    = PtrType{}
	Void   = VoidType{}
	Valist = ValistType{}
)

// Equal reports structural type equality. Two StructTypes with different
// Import strings are never Equal, even with the same Name — cross-module
// struct identity is nominal per-origin, not merely by spelling (§7.4
// treats byval/sret comparison specially precisely because plain structural
// name equality isn't safe across modules).
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
		return ok && x.Name == y.Name && x.Import == y.Import
	case ArrayType:
		y, ok := b.(ArrayType)
		return ok && x.Len == y.Len && Equal(x.Elem, y.Elem)
	case ValistType:
		_, ok := b.(ValistType)
		return ok
	}
	return false
}

func IsInt(t Type) bool    { _, ok := t.(IntType); return ok }
func IsFloat(t Type) bool  { _, ok := t.(FloatType); return ok }
func IsPtr(t Type) bool    { _, ok := t.(PtrType); return ok }
func IsVoid(t Type) bool   { _, ok := t.(VoidType); return ok }
func IsVec(t Type) bool    { _, ok := t.(VecType); return ok }
func IsValist(t Type) bool { _, ok := t.(ValistType); return ok }

// IsAggregate reports whether t is memory-only (§2): never a named value.
func IsAggregate(t Type) bool {
	switch t.(type) {
	case StructType, ArrayType:
		return true
	}
	return false
}

// IsValueType reports whether t may be the type of a named value (a
// parameter, an instruction result, an ordinary local). valist is
// excluded deliberately (§4.5): it only ever appears as an alloca result
// or a va_* operand, both checked structurally at those call sites, never
// as a general-purpose named value or parameter type.
func IsValueType(t Type) bool {
	return !IsAggregate(t) && !IsVoid(t) && !IsValist(t)
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

// IsVaArgType reports whether t is legal as a va_arg.<T> destination type
// (§4.5): scalar, ptr, or vec — struct/array must go by pointer instead.
func IsVaArgType(t Type) bool {
	return IsScalarType(t) || IsVec(t)
}

// ElemOrSelf returns the element type for vectors, t otherwise. Handy for
// "iN or vec[iN, W]" opcode legality checks.
func ElemOrSelf(t Type) Type {
	if v, ok := t.(VecType); ok {
		return v.Elem
	}
	return t
}