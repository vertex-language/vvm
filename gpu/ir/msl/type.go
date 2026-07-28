package msl

// Type is implemented by every MSL type node.
type Type interface{ isType() }

// AddressSpace qualifies pointer, reference, and variable declarations.
// Address spaces are part of the type system, not of operations.
type AddressSpace string

// Address spaces.
const (
	NoSpace               AddressSpace = ""
	Device                AddressSpace = "device"
	Constant              AddressSpace = "constant"
	Threadgroup           AddressSpace = "threadgroup"
	ThreadgroupImageblock AddressSpace = "threadgroup_imageblock"
	Thread                AddressSpace = "thread"
	RayData               AddressSpace = "ray_data"
	ObjectData            AddressSpace = "object_data"
)

// ScalarType is a fundamental type, spelled exactly as it prints. Revisions
// add scalars faster than this package can track them; ScalarType is a string
// type so unlisted spellings are reachable directly, e.g.
// ScalarType("float8_e4m3"), and Verify will not gate what it does not know.
type ScalarType string

func (ScalarType) isType() {}

// Fundamental types.
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
	BFloat  ScalarType = "bfloat"    // >= Metal31
	Size    ScalarType = "size_t"
	PtrDiff ScalarType = "ptrdiff_t"
	Void    ScalarType = "void"
	Auto    ScalarType = "auto"      // >= Metal32
)

// NamedType references a struct, alias, or any other declared name.
type NamedType string

func (NamedType) isType() {}

// Named references a declared type by name.
func Named(name string) NamedType { return NamedType(name) }

// ConstType is a const-qualified type: const device float*.
type ConstType struct{ Elem Type }

func (ConstType) isType() {}

// Const const-qualifies a type.
func Const(t Type) ConstType { return ConstType{Elem: t} }

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
func Mat(elem Type, cols, rows int) MatType {
	return MatType{Elem: elem, Cols: cols, Rows: rows}
}

// PackedType is a packed vector such as packed_float3.
type PackedType struct {
	Elem Type
	N    int
}

func (PackedType) isType() {}

// Packed builds a packed vector type.
func Packed(elem Type, n int) PackedType { return PackedType{Elem: elem, N: n} }

// AtomicType is an atomic scalar such as atomic_uint.
type AtomicType struct{ Elem Type }

func (AtomicType) isType() {}

// Atomic builds an atomic type.
func Atomic(elem Type) AtomicType { return AtomicType{Elem: elem} }

// ArrayType is a fixed-size array. The length belongs at the declarator site
// (float tile[256]); the printer places it there.
type ArrayType struct {
	Elem Type
	Len  int
}

func (ArrayType) isType() {}

// Array builds a fixed-size array type.
func Array(elem Type, n int) ArrayType { return ArrayType{Elem: elem, Len: n} }

// PtrType is an address-space-qualified pointer. There is no unqualified
// constructor: the metal compiler rejects one, so it is unrepresentable.
type PtrType struct {
	Space AddressSpace
	Elem  Type
}

func (PtrType) isType() {}

// Ptr builds an address-space-qualified pointer type.
func Ptr(space AddressSpace, elem Type) PtrType {
	return PtrType{Space: space, Elem: elem}
}

// RefType is an address-space-qualified reference.
type RefType struct {
	Space AddressSpace
	Elem  Type
}

func (RefType) isType() {}

// Ref builds an address-space-qualified reference type.
func Ref(space AddressSpace, elem Type) RefType {
	return RefType{Space: space, Elem: elem}
}

// Access is a texture access qualifier.
type Access string

// Texture access qualifiers.
const (
	AccessSample    Access = "sample"
	AccessRead      Access = "read"
	AccessWrite     Access = "write"
	AccessReadWrite Access = "read_write"
)

// TextureType is a texture or depth-texture type. Kind is the base spelling
// ("texture2d", "texturecube_array", "depth2d_ms", ...).
type TextureType struct {
	Kind   string
	Elem   Type
	Access Access
}

func (TextureType) isType() {}

// Texture builds a texture type of the given kind.
func Texture(kind string, elem Type, acc Access) TextureType {
	return TextureType{Kind: kind, Elem: elem, Access: acc}
}

// Common texture constructors.
func Texture2D(elem Type, acc Access) TextureType { return Texture("texture2d", elem, acc) }
func Texture3D(elem Type, acc Access) TextureType { return Texture("texture3d", elem, acc) }
func TextureCube(elem Type, acc Access) TextureType {
	return Texture("texturecube", elem, acc)
}
func Texture2DArray(elem Type, acc Access) TextureType {
	return Texture("texture2d_array", elem, acc)
}
func Depth2D(elem Type, acc Access) TextureType { return Texture("depth2d", elem, acc) }

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

// TypeArg is a C++ template argument: either a type or a constant expression.
// MSL 4's tensor operator surface is template-shaped, so template arguments
// are grammar here rather than raw text.
type TypeArg struct {
	Type Type // nil when Val is set
	Val  Expr // zero when Type is set
}

// TArg builds a type template argument.
func TArg(t Type) TypeArg { return TypeArg{Type: t} }

// VArg builds a non-type (constant expression) template argument.
func VArg(e Expr) TypeArg { return TypeArg{Val: e} }

// TemplateType is any templated type spelling: Name<args...>.
type TemplateType struct {
	Name string
	Args []TypeArg
}

func (TemplateType) isType() {}

// Template builds a templated type: Template("matmul2d_descriptor",
// VArg(I(16)), VArg(I(16)), VArg(DynamicExtent)).
func Template(name string, args ...TypeArg) TemplateType {
	return TemplateType{Name: name, Args: args}
}

// TensorKind discriminates the two device-backed tensor flavors.
type TensorKind string

// Tensor kinds.
const (
	TensorHandleKind TensorKind = "tensor_handle" // host-bound, may carry explicit strides
	TensorInlineKind TensorKind = "tensor_inline" // shader-created view over a buffer
)

// TensorType is a device-backed Metal 4 tensor. Rank is the number of dynamic
// extents. Offset adds the tensor_offset tag, which permits GPU-side slicing
// without a new descriptor. Requires Metal40 and <metal_tensor>.
type TensorType struct {
	Kind   TensorKind
	Space  AddressSpace
	Elem   Type
	Rank   int
	Offset bool
}

func (TensorType) isType() {}

// TensorHandle builds a host-bound tensor type.
func TensorHandle(space AddressSpace, elem Type, rank int) TensorType {
	return TensorType{Kind: TensorHandleKind, Space: space, Elem: elem, Rank: rank}
}

// TensorInline builds a shader-created view over a buffer or tensor.
func TensorInline(space AddressSpace, elem Type, rank int) TensorType {
	return TensorType{Kind: TensorInlineKind, Space: space, Elem: elem, Rank: rank}
}

// CoopTensorType is a cooperative tensor: a register fragment held collectively
// by the threads of an execution scope. It never reaches device memory and its
// internal layout is deliberately opaque, so it carries no address space.
type CoopTensorType struct {
	Elem Type
	Rank int
}

func (CoopTensorType) isType() {}

// CoopTensor builds a cooperative tensor type.
func CoopTensor(elem Type, rank int) CoopTensorType {
	return CoopTensorType{Elem: elem, Rank: rank}
}