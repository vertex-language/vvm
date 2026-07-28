package ptx

// Type is a PTX type specifier. Types are scalars only; vector-ness is a
// property of an instruction (the .vN qualifier, derived from operand
// arity) or of a variable declaration (Var.Vec), never of the type itself.
type Type uint8

const (
	NoType Type = iota

	// Signed integer.
	S8
	S16
	S32
	S64

	// Unsigned integer.
	U8
	U16
	U32
	U64

	// Floating point.
	F16
	F32
	F64

	// Untyped bits.
	B8
	B16
	B32
	B64
	B128

	// Predicate.
	Pred

	// Packed floating point.
	F16x2
	BF16
	BF16x2

	// Alternate floating point (instruction types, never storage types).
	TF32
	E4M3
	E5M2
	E3M2
	E2M3
	E2M1
	E4M3x2
	E5M2x2
	E3M2x2
	E2M3x2
	E2M1x2
	UE8M0
	UE8M0x2
	UE4M3

	// Packed integer.
	U16x2
	S16x2
	U8x4
	S8x4

	// Sub-byte.
	U4
	S4
	B4

	// Tensor sub-byte packed forms.
	B4x16
	B4x16P64
	B6x16P32

	// Opaque.
	TexRef
	SamplerRef
	SurfRef
)

type typeInfo struct {
	name string
	bits int
	isFP bool
	// minISA is the ISA version that introduced the type, zero if original.
	minISA ISAVersion
}

var typeTable = map[Type]typeInfo{
	S8:  {"s8", 8, false, ISAVersion{}},
	S16: {"s16", 16, false, ISAVersion{}},
	S32: {"s32", 32, false, ISAVersion{}},
	S64: {"s64", 64, false, ISAVersion{}},

	U8:  {"u8", 8, false, ISAVersion{}},
	U16: {"u16", 16, false, ISAVersion{}},
	U32: {"u32", 32, false, ISAVersion{}},
	U64: {"u64", 64, false, ISAVersion{}},

	F16: {"f16", 16, true, ISAVersion{}},
	F32: {"f32", 32, true, ISAVersion{}},
	F64: {"f64", 64, true, ISAVersion{}},

	B8:   {"b8", 8, false, ISAVersion{}},
	B16:  {"b16", 16, false, ISAVersion{}},
	B32:  {"b32", 32, false, ISAVersion{}},
	B64:  {"b64", 64, false, ISAVersion{}},
	B128: {"b128", 128, false, ISA83},

	Pred: {"pred", 1, false, ISAVersion{}},

	F16x2:  {"f16x2", 32, true, ISAVersion{}},
	BF16:   {"bf16", 16, true, ISA70},
	BF16x2: {"bf16x2", 32, true, ISA70},

	TF32:    {"tf32", 32, true, ISA70},
	E4M3:    {"e4m3", 8, true, ISA81},
	E5M2:    {"e5m2", 8, true, ISA81},
	E3M2:    {"e3m2", 6, true, ISA86},
	E2M3:    {"e2m3", 6, true, ISA86},
	E2M1:    {"e2m1", 4, true, ISA86},
	E4M3x2:  {"e4m3x2", 16, true, ISA81},
	E5M2x2:  {"e5m2x2", 16, true, ISA81},
	E3M2x2:  {"e3m2x2", 16, true, ISA86},
	E2M3x2:  {"e2m3x2", 16, true, ISA86},
	E2M1x2:  {"e2m1x2", 8, true, ISA86},
	UE8M0:   {"ue8m0", 8, true, ISA86},
	UE8M0x2: {"ue8m0x2", 16, true, ISA86},
	UE4M3:   {"ue4m3", 8, true, ISA86},

	U16x2: {"u16x2", 32, false, ISA80},
	S16x2: {"s16x2", 32, false, ISA80},
	U8x4:  {"u8x4", 32, false, ISA92},
	S8x4:  {"s8x4", 32, false, ISA92},

	U4: {"u4", 4, false, ISAVersion{}},
	S4: {"s4", 4, false, ISAVersion{}},
	B4: {"b4", 4, false, ISAVersion{}},

	B4x16:    {"b4x16", 64, false, ISA88},
	B4x16P64: {"b4x16_p64", 128, false, ISA88},
	B6x16P32: {"b6x16_p32", 128, false, ISA88},

	TexRef:     {"texref", 0, false, ISAVersion{}},
	SamplerRef: {"samplerref", 0, false, ISAVersion{}},
	SurfRef:    {"surfref", 0, false, ISAVersion{}},
}

// String returns the dotted type specifier, e.g. ".f32".
func (t Type) String() string {
	if i, ok := typeTable[t]; ok {
		return "." + i.name
	}
	return ""
}

// Name returns the bare type name without the leading dot, e.g. "f32".
func (t Type) Name() string { return typeTable[t].name }

// Bits returns the width in bits; 0 for opaque types.
func (t Type) Bits() int { return typeTable[t].bits }

// IsFloat reports whether t is any floating-point format.
func (t Type) IsFloat() bool { return typeTable[t].isFP }

// IsValid reports whether t names a real PTX type.
func (t Type) IsValid() bool { _, ok := typeTable[t]; return ok }

// MinISA returns the ISA version that introduced t.
func (t Type) MinISA() ISAVersion { return typeTable[t].minISA }