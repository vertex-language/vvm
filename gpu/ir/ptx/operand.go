package ptx

import (
	"fmt"
	"math"
	"strconv"
)

// Operand is anything that can appear in an instruction operand position.
type Operand interface{ Text() string }

// Imm is an integer immediate.
type Imm int64

func (i Imm) Text() string { return strconv.FormatInt(int64(i), 10) }

// FImm is a floating-point immediate, printed in the canonical PTX hex
// form (0f... / 0d...) only via FHex; FImm prints decimal.
type FImm float64

func (f FImm) Text() string { return formatFloat(float64(f)) }

// FHex32 prints a float32 immediate as the exact-bits 0fXXXXXXXX form.
type FHex32 float32

func (f FHex32) Text() string { return fmt.Sprintf("0f%08X", math.Float32bits(float32(f))) }

// FHex64 prints a float64 immediate as the exact-bits 0dXXXXXXXXXXXXXXXX form.
type FHex64 float64

func (f FHex64) Text() string { return fmt.Sprintf("0d%016X", math.Float64bits(float64(f))) }

// Sym is a bare symbol reference (label or variable name).
type Sym string

func (s Sym) Text() string { return string(s) }

// SpecialReg is a predefined %-register.
type SpecialReg string

func (s SpecialReg) Text() string { return string(s) }

// AddrRef is a memory operand: [base], [base+off], or [symbol].
type AddrRef struct {
	Base   Operand
	Offset int64
}

func (a AddrRef) Text() string {
	if a.Offset != 0 {
		if a.Offset < 0 {
			return fmt.Sprintf("[%s%d]", a.Base.Text(), a.Offset)
		}
		return fmt.Sprintf("[%s+%d]", a.Base.Text(), a.Offset)
	}
	return "[" + a.Base.Text() + "]"
}

// Addr builds a memory operand from a register or symbol, with an
// optional byte offset: Addr(a) -> [%rd1], Addr(a, 16) -> [%rd1+16].
func Addr(base Operand, off ...int64) AddrRef {
	a := AddrRef{Base: base}
	if len(off) > 0 {
		a.Offset = off[0]
	}
	return a
}

// SymAddr builds a memory operand addressing a named symbol: [name].
func SymAddr(name string, off ...int64) AddrRef {
	return Addr(Sym(name), off...)
}

// vecOperand groups registers for vector ld/st: {%f1, %f2, %f3, %f4}.
type vecOperand []Operand

func (v vecOperand) Text() string {
	s := "{"
	for i, o := range v {
		if i > 0 {
			s += ", "
		}
		s += o.Text()
	}
	return s + "}"
}

// Vec groups operands for vector loads/stores.
func Vec(ops ...Operand) Operand { return vecOperand(ops) }

// group is a parenthesized operand list used by call.
type group []Operand

func (g group) Text() string {
	s := "("
	for i, o := range g {
		if i > 0 {
			s += ", "
		}
		s += o.Text()
	}
	return s + ")"
}

// toOperand coerces convenient Go values into operands, so emit methods
// accept plain ints and floats (cb.MulWideU32(off, i, 4)).
func toOperand(v any) Operand {
	switch x := v.(type) {
	case Operand:
		return x
	case int:
		return Imm(x)
	case int32:
		return Imm(x)
	case int64:
		return Imm(x)
	case uint:
		return Imm(x)
	case uint32:
		return Imm(x)
	case uint64:
		return Imm(x)
	case float32:
		return FImm(x)
	case float64:
		return FImm(x)
	case string:
		return Sym(x)
	default:
		panic(fmt.Sprintf("ptx: cannot use %T as operand", v))
	}
}

func toOperands(vs []any) []Operand {
	out := make([]Operand, len(vs))
	for i, v := range vs {
		out[i] = toOperand(v)
	}
	return out
}

func formatFloat(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}