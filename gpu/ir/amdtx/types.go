package amdtx

import "fmt"

// Type is a scalar/vector element type as it appears in .amdtx text.
type Type int

const (
	Pred Type = iota // predicate / EXEC-mask lane bit
	B32
	B64
	U32
	S32
	U64
	S64
	F16
	F32
	F64
)

func (t Type) String() string {
	switch t {
	case Pred:
		return "pred"
	case B32:
		return "b32"
	case B64:
		return "b64"
	case U32:
		return "u32"
	case S32:
		return "s32"
	case U64:
		return "u64"
	case S64:
		return "s64"
	case F16:
		return "f16"
	case F32:
		return "f32"
	case F64:
		return "f64"
	}
	return "?"
}

// Bits returns the bit width of the type.
func (t Type) Bits() int {
	switch t {
	case B64, U64, S64, F64:
		return 64
	case F16:
		return 16
	case Pred:
		return 1
	default:
		return 32
	}
}

// AddrSpace is a memory / state space, mirroring PTX state spaces.
type AddrSpace int

const (
	Global   AddrSpace = iota // global memory
	Local                     // LDS (group)
	Private                   // scratch
	Constant                  // read-only
	Generic                   // flat
	Region                    // GDS
)

func (s AddrSpace) String() string {
	switch s {
	case Global:
		return "global"
	case Local:
		return "local"
	case Private:
		return "private"
	case Constant:
		return "constant"
	case Generic:
		return "generic"
	case Region:
		return "region"
	}
	return "?"
}

// Operand is anything that can appear as a source/dest of a virtual instruction:
// a register, an immediate, or a register+offset addressing form.
type Operand interface {
	isOperand()
	// textForm renders the operand in .amdtx text.
	textForm() string
}

// Imm is an immediate literal. When Inline is true the value is small enough to
// use an amdgcn inline constant (the lower pass decides the physical encoding).
type Imm struct {
	Value  uint64
	Inline bool
}

func (Imm) isOperand() {}
func (i Imm) textForm() string {
	if i.Value < 16 && i.Inline {
		return fmt.Sprintf("%d", i.Value)
	}
	return fmt.Sprintf("0x%x", i.Value)
}

// Inl builds an inline immediate (encoded directly in the instruction word).
func Inl(v uint64) Imm { return Imm{Value: v, Inline: true} }

// Lit builds a 32-bit literal immediate (encoded as a trailing literal dword).
func Lit(v uint64) Imm { return Imm{Value: v, Inline: false} }

// Addr is a base register plus a byte offset, used by scalar/flat loads.
type Addr struct {
	Base   Reg
	Offset int32
}

func (Addr) isOperand() {}
func (a Addr) textForm() string {
	return fmt.Sprintf("%s, 0x%x", a.Base.textForm(), uint32(a.Offset))
}

// Off builds a base+offset addressing operand.
func Off(base Reg, off int32) Addr { return Addr{Base: base, Offset: off} }