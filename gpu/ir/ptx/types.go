package ptx

import "fmt"

// Type is a PTX type specifier. Fundamental types are the predefined
// package variables; vectors are built with V2/V4.
type Type struct {
	name string // without leading dot, e.g. "f32"
	bits int
	vec  int   // 0 = scalar, 2, 4
	elem *Type // element type for vectors
}

func (t Type) String() string {
	if t.vec != 0 {
		return fmt.Sprintf(".v%d %s", t.vec, t.elem.String())
	}
	return "." + t.name
}

// Name returns the bare type name ("f32").
func (t Type) Name() string { return t.name }

// Bits returns the scalar bit width.
func (t Type) Bits() int { return t.bits }

// IsVec reports whether t is a vector type.
func (t Type) IsVec() bool { return t.vec != 0 }

func scalar(name string, bits int) Type { return Type{name: name, bits: bits} }

// Fundamental (storage) types.
var (
	S8  = scalar("s8", 8)
	S16 = scalar("s16", 16)
	S32 = scalar("s32", 32)
	S64 = scalar("s64", 64)

	U8  = scalar("u8", 8)
	U16 = scalar("u16", 16)
	U32 = scalar("u32", 32)
	U64 = scalar("u64", 64)

	F16   = scalar("f16", 16)
	F16x2 = scalar("f16x2", 32)
	F32   = scalar("f32", 32)
	F64   = scalar("f64", 64)

	B8   = scalar("b8", 8)
	B16  = scalar("b16", 16)
	B32  = scalar("b32", 32)
	B64  = scalar("b64", 64)
	B128 = scalar("b128", 128)

	Pred = scalar("pred", 1)
)

// Alternate FP formats: instruction types only, never storage types.
var (
	BF16   = scalar("bf16", 16)
	BF16x2 = scalar("bf16x2", 32)
	TF32   = scalar("tf32", 32)
	E4M3   = scalar("e4m3", 8)
	E5M2   = scalar("e5m2", 8)
	E3M2   = scalar("e3m2", 8)
	E2M3   = scalar("e2m3", 8)
	E2M1   = scalar("e2m1", 4)
	E4M3x2 = scalar("e4m3x2", 16)
	E5M2x2 = scalar("e5m2x2", 16)
)

// Opaque types.
var (
	TexRef     = scalar("texref", 0)
	SamplerRef = scalar("samplerref", 0)
	SurfRef    = scalar("surfref", 0)
)

// V2 wraps a scalar into a two-element vector: V2(F32) -> ".v2 .f32".
func V2(t Type) Type { e := t; return Type{vec: 2, elem: &e, bits: t.bits * 2, name: t.name} }

// V4 wraps a scalar into a four-element vector: V4(U32) -> ".v4 .u32".
func V4(t Type) Type { e := t; return Type{vec: 4, elem: &e, bits: t.bits * 4, name: t.name} }