package ptx

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Operand is anything that can occupy an instruction operand position. The
// interface is closed: only this package can implement it.
type Operand interface {
	Text() string
	operand()
}

// Addressable is an operand that can be the base of a memory operand.
type Addressable interface {
	Operand
	addressable()
}

// Imm is a signed integer immediate.
type Imm int64

func (i Imm) Text() string { return strconv.FormatInt(int64(i), 10) }
func (Imm) operand()       {}

// UImm is an unsigned integer immediate. It prints with the U suffix so
// values above the .s64 range are not reinterpreted as negative.
type UImm uint64

func (u UImm) Text() string { return strconv.FormatUint(uint64(u), 10) + "U" }
func (UImm) operand()       {}

// F32Imm is a single-precision immediate. It always prints in the exact-bits
// 0fXXXXXXXX form, so no value, including NaN and the infinities, can be
// rendered as invalid or lossy PTX.
type F32Imm float32

func (f F32Imm) Text() string {
	return fmt.Sprintf("0f%08X", math.Float32bits(float32(f)))
}
func (F32Imm) operand() {}

// F64Imm is a double-precision immediate in the exact-bits 0dX... form.
type F64Imm float64

func (f F64Imm) Text() string {
	return fmt.Sprintf("0d%016X", math.Float64bits(float64(f)))
}
func (F64Imm) operand() {}

// Sym is a bare symbol reference. Prefer the typed handles returned by
// Module.Var, Callable.Param, Body.Label and Module.Add; Sym exists for
// symbols this package does not model.
type Sym string

func (s Sym) Text() string { return string(s) }
func (Sym) operand()       {}
func (Sym) addressable()   {}

// Mem is a memory operand: [base], [base+off], or [base-off].
type Mem struct {
	Base   Addressable
	Offset int64
}

func (m Mem) Text() string {
	if m.Base == nil {
		return "[<nil>]"
	}
	switch {
	case m.Offset > 0:
		return "[" + m.Base.Text() + "+" + strconv.FormatInt(m.Offset, 10) + "]"
	case m.Offset < 0:
		return "[" + m.Base.Text() + strconv.FormatInt(m.Offset, 10) + "]"
	}
	return "[" + m.Base.Text() + "]"
}
func (Mem) operand() {}

// At builds a memory operand with an optional byte offset:
// At(r) renders as [%rd1], At(r, 16) as [%rd1+16].
func At(base Addressable, off ...int64) Mem {
	m := Mem{Base: base}
	if len(off) > 0 {
		m.Offset = off[0]
	}
	return m
}

// VecOp is a braced operand list for vector loads and stores. Its length
// supplies the .vN qualifier, so the vector width is stated exactly once.
type VecOp []Operand

func (v VecOp) Text() string {
	parts := make([]string, len(v))
	for i, o := range v {
		parts[i] = o.Text()
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
func (VecOp) operand() {}

// Vec groups operands for a vector load or store.
func Vec(ops ...Operand) VecOp { return VecOp(ops) }

// group is a parenthesized operand list used by call.
type group []Operand

func (g group) Text() string {
	parts := make([]string, len(g))
	for i, o := range g {
		parts[i] = o.Text()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
func (group) operand() {}

// pair joins two operands with a vertical bar, as in shfl.sync's optional
// predicate destination: d|p.
type pair struct{ a, b Operand }

func (p pair) Text() string { return p.a.Text() + "|" + p.b.Text() }
func (pair) operand()       {}

// Or pairs a destination register with an optional predicate destination.
func Or(d, p Reg) Operand { return pair{d, p} }