package msl

import "fmt"

// Type is the interface implemented by all MSL type nodes.
type Type interface{ isType() }

// AddressSpace qualifies pointer and reference types. Address spaces are
// part of the type system, not of individual operations.
type AddressSpace string

// Address spaces.
const (
	Device                AddressSpace = "device"
	Constant              AddressSpace = "constant"
	Threadgroup           AddressSpace = "threadgroup"
	ThreadgroupImageblock AddressSpace = "threadgroup_imageblock"
	Thread                AddressSpace = "thread"
	RayData               AddressSpace = "ray_data"
	ObjectData            AddressSpace = "object_data"
)

// ScalarType is a fundamental MSL type, spelled exactly as it prints.
type ScalarType string

func (ScalarType) isType() {}

// Fundamental types mirroring the spec.
const (
	Bool    ScalarType = "bool"
	Char    ScalarType = "char"
	UChar   ScalarType = "uchar"
	Short   ScalarType = "short"
	UShort  ScalarType = "ushort"
	Int     ScalarType = "int"
	UInt    ScalarType = "uint"
	Long    ScalarType = "long"
	ULong   ScalarType = "ulong"
	Half    ScalarType = "half"
	Float   ScalarType = "float"
	BFloat  ScalarType = "bfloat" // requires >= Metal31
	Size    ScalarType = "size_t"
	PtrDiff ScalarType = "ptrdiff_t"
	Void    ScalarType = "void"
)

// VecType is a vector type such as float4.
type VecType struct {
	Elem Type
	N    int
}

func (VecType) isType() {}

// Vec builds a vector type: Vec(Float, 4) is float4.
func Vec(elem Type, n int) VecType { return VecType{Elem: elem, N: n} }

// MatType is a matrix type such as float4x4 (columns x rows).
type MatType struct {
	Elem       Type
	Cols, Rows int
}

func (MatType) isType() {}

// Mat builds a matrix type: Mat(Float, 4, 4) is float4x4.
func Mat(elem Type, cols, rows int) MatType { return MatType{Elem: elem, Cols: cols, Rows: rows} }

// PackedType is a packed vector type such as packed_float3.
type PackedType struct {
	Elem Type
	N    int
}

func (PackedType) isType() {}

// Packed builds a packed vector type: Packed(Float, 3) is packed_float3.
func Packed(elem Type, n int) PackedType { return PackedType{Elem: elem, N: n} }

// AtomicType is an atomic scalar type such as atomic_uint.
type AtomicType struct{ Elem Type }

func (AtomicType) isType() {}

// Atomic builds an atomic type: Atomic(UInt) is atomic_uint.
func Atomic(elem Type) AtomicType { return AtomicType{Elem: elem} }

// ArrayType is a fixed-size array. The length prints at the declarator
// site (float tile[256]), which the printer handles.
type ArrayType struct {
	Elem Type
	Len  int
}

func (ArrayType) isType() {}

// Array builds a fixed-size array type.
func Array(elem Type, n int) ArrayType { return ArrayType{Elem: elem, Len: n} }

// PtrType is an address-space-qualified pointer. There is no unqualified
// pointer constructor: the Metal compiler rejects one, so the API makes
// it unrepresentable. A zero-valued Space is a structural error caught
// by the printer.
type PtrType struct {
	Space AddressSpace
	Elem  Type
}

func (PtrType) isType() {}

// Ptr builds an address-space-qualified pointer type.
func Ptr(space AddressSpace, elem Type) PtrType { return PtrType{Space: space, Elem: elem} }

// RefType is an address-space-qualified reference.
type RefType struct {
	Space AddressSpace
	Elem  Type
}

func (RefType) isType() {}

// Ref builds an address-space-qualified reference type.
func Ref(space AddressSpace, elem Type) RefType { return RefType{Space: space, Elem: elem} }

// NamedType references a struct (or any type) by name.
type NamedType string

func (NamedType) isType() {}

// Named references a declared struct by name.
func Named(name string) NamedType { return NamedType(name) }

// Access is a texture access qualifier (access::sample etc.).
type Access string

// Texture access qualifiers.
const (
	AccessSample    Access = "sample"
	AccessRead      Access = "read"
	AccessWrite     Access = "write"
	AccessReadWrite Access = "read_write"
)

// TextureType is a texture or depth-texture type.
type TextureType struct {
	Kind   string // "texture2d", "texture3d", "texturecube", "texture2d_array", "depth2d", ...
	Elem   Type
	Access Access
}

func (TextureType) isType() {}

// Texture and depth-texture constructors.
func Texture2D(elem Type, acc Access) TextureType {
	return TextureType{Kind: "texture2d", Elem: elem, Access: acc}
}
func Texture3D(elem Type, acc Access) TextureType {
	return TextureType{Kind: "texture3d", Elem: elem, Access: acc}
}
func TextureCube(elem Type, acc Access) TextureType {
	return TextureType{Kind: "texturecube", Elem: elem, Access: acc}
}
func Texture2DArray(elem Type, acc Access) TextureType {
	return TextureType{Kind: "texture2d_array", Elem: elem, Access: acc}
}
func Depth2D(elem Type, acc Access) TextureType {
	return TextureType{Kind: "depth2d", Elem: elem, Access: acc}
}

// SamplerType is the sampler type.
type SamplerType struct{}

func (SamplerType) isType() {}

// Sampler is the sampler type.
var Sampler = SamplerType{}

// ImageBlockType is imageblock<Layout>.
type ImageBlockType struct{ Layout string }

func (ImageBlockType) isType() {}

// ImageBlock builds an imageblock type over a named layout struct.
func ImageBlock(layout string) ImageBlockType { return ImageBlockType{Layout: layout} }

// TensorKind discriminates the Metal 4 tensor flavors.
type TensorKind string

// Tensor kinds.
const (
	TensorKindHandle      TensorKind = "tensor_handle" // host-bound, may carry explicit strides
	TensorKindInline      TensorKind = "tensor_inline" // GPU-side view into a buffer
	TensorKindCooperative TensorKind = "cooperative"   // thread-distributed transient tensor
)

// TensorType is a Metal 4 tensor type. Requires Metal40 and
// #include <metal_tensor>. Rank is the number of dynamic extents.
type TensorType struct {
	Kind  TensorKind
	Space AddressSpace
	Elem  Type
	Rank  int
}

func (TensorType) isType() {}

// TensorHandle builds a host-bound tensor type:
// tensor<device half, dextents<int, 2>, tensor_handle>.
func TensorHandle(elem Type, rank int) TensorType {
	return TensorType{Kind: TensorKindHandle, Space: Device, Elem: elem, Rank: rank}
}

// TensorInline builds a GPU-side tensor view into a tensor or buffer.
func TensorInline(elem Type, rank int) TensorType {
	return TensorType{Kind: TensorKindInline, Space: Device, Elem: elem, Rank: rank}
}

// CooperativeTensor builds a thread-distributed transient tensor type.
func CooperativeTensor(elem Type, rank int) TensorType {
	return TensorType{Kind: TensorKindCooperative, Elem: elem, Rank: rank}
}

// TypeString returns the canonical MSL spelling of a type. Array types
// return their element spelling plus a bracket suffix combined; callers
// that need declarator-site placement should special-case ArrayType.
func TypeString(t Type) string {
	switch v := t.(type) {
	case nil:
		return "void"
	case ScalarType:
		return string(v)
	case NamedType:
		return string(v)
	case VecType:
		return fmt.Sprintf("%s%d", TypeString(v.Elem), v.N)
	case MatType:
		return fmt.Sprintf("%s%dx%d", TypeString(v.Elem), v.Cols, v.Rows)
	case PackedType:
		return fmt.Sprintf("packed_%s%d", TypeString(v.Elem), v.N)
	case AtomicType:
		return "atomic_" + TypeString(v.Elem)
	case ArrayType:
		return fmt.Sprintf("%s[%d]", TypeString(v.Elem), v.Len)
	case PtrType:
		return fmt.Sprintf("%s %s*", v.Space, TypeString(v.Elem))
	case RefType:
		return fmt.Sprintf("%s %s&", v.Space, TypeString(v.Elem))
	case TextureType:
		return fmt.Sprintf("%s<%s, access::%s>", v.Kind, TypeString(v.Elem), v.Access)
	case SamplerType:
		return "sampler"
	case ImageBlockType:
		return fmt.Sprintf("imageblock<%s>", v.Layout)
	case TensorType:
		switch v.Kind {
		case TensorKindCooperative:
			return fmt.Sprintf("cooperative_tensor<%s, dextents<int, %d>>", TypeString(v.Elem), v.Rank)
		default:
			return fmt.Sprintf("tensor<%s %s, dextents<int, %d>, %s>",
				v.Space, TypeString(v.Elem), v.Rank, v.Kind)
		}
	default:
		return fmt.Sprintf("/*unknown type %T*/", t)
	}
}