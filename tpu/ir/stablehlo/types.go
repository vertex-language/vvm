package stablehlo

import (
	"fmt"
	"strconv"
	"strings"
)

// Type is a StableHLO value type (tensor, token, tuple).
// String returns the canonical MLIR spelling.
type Type interface {
	String() string
}

// TypeString renders any Type to its canonical MLIR spelling. Exported for
// tooling built on top of the IR.
func TypeString(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

// Element is a tensor element type. Element types are not first-class in
// StableHLO: scalar values are idiomatically 0-dimensional tensors.
type Element interface {
	elemString() string
}

// ElemType is a named element type. Note that StableHLO's MLIR
// implementation spells signed integers as signless (i8, i64, ...), so the
// SI* constants render without the leading 's' to keep output verifiable
// with stablehlo-opt.
type ElemType string

func (e ElemType) elemString() string { return string(e) }

const (
	I1 ElemType = "i1" // boolean

	SI2  ElemType = "i2"
	SI4  ElemType = "i4"
	SI8  ElemType = "i8"
	SI16 ElemType = "i16"
	SI32 ElemType = "i32"
	SI64 ElemType = "i64"

	UI2  ElemType = "ui2"
	UI4  ElemType = "ui4"
	UI8  ElemType = "ui8"
	UI16 ElemType = "ui16"
	UI32 ElemType = "ui32"
	UI64 ElemType = "ui64"

	F16  ElemType = "f16"
	F32  ElemType = "f32"
	F64  ElemType = "f64"
	BF16 ElemType = "bf16"
	TF32 ElemType = "tf32"

	F8E4M3        ElemType = "f8E4M3"
	F8E4M3FN      ElemType = "f8E4M3FN"
	F8E5M2        ElemType = "f8E5M2"
	F8E3M4        ElemType = "f8E3M4"
	F8E4M3FNUZ    ElemType = "f8E4M3FNUZ"
	F8E5M2FNUZ    ElemType = "f8E5M2FNUZ"
	F8E4M3B11FNUZ ElemType = "f8E4M3B11FNUZ"

	// MX microscaling formats.
	F4E2M1FN  ElemType = "f4E2M1FN"
	F6E2M3FN  ElemType = "f6E2M3FN"
	F6E3M2FN  ElemType = "f6E3M2FN"
	F8E8M0FNU ElemType = "f8E8M0FNU"
)

type complexElem struct{ elem ElemType }

func (c complexElem) elemString() string { return "complex<" + string(c.elem) + ">" }

// Complex returns the complex element type complex<e>. e must be F32 or F64.
func Complex(e ElemType) Element { return complexElem{e} }

// Dyn is the dynamic dimension size sentinel; it prints as '?'.
const Dyn int64 = -1

type tensorType struct {
	elem Element
	dims []int64
}

func (t tensorType) String() string {
	var b strings.Builder
	b.WriteString("tensor<")
	for _, d := range t.dims {
		if d == Dyn {
			b.WriteString("?x")
		} else {
			b.WriteString(strconv.FormatInt(d, 10))
			b.WriteByte('x')
		}
	}
	b.WriteString(t.elem.elemString())
	b.WriteByte('>')
	return b.String()
}

// Tensor returns tensor<dims x e>. With no dims it is the 0-d scalar
// tensor tensor<e>.
func Tensor(e Element, dims ...int64) Type { return tensorType{e, dims} }

// TensorDyn is Tensor with dynamic sizes allowed and is provided for
// readability at call sites; stablehlo.Dyn may be used with either.
func TensorDyn(e Element, dims ...int64) Type { return tensorType{e, dims} }

type tokenType struct{}

func (tokenType) String() string { return "!stablehlo.token" }

// Token is the !stablehlo.token type.
var Token Type = tokenType{}

type tupleType struct{ elems []Type }

func (t tupleType) String() string {
	parts := make([]string, len(t.elems))
	for i, e := range t.elems {
		parts[i] = e.String()
	}
	return "tuple<" + strings.Join(parts, ", ") + ">"
}

// Tuple returns tuple<ts...> (HLO-ABI legacy).
func Tuple(ts ...Type) Type { return tupleType{ts} }

// QuantizedElement describes a uniform-quantized element type. Setting
// Dimension (via stablehlo.Axis) selects per-axis quantization, with one
// scale/zero-point pair per slice.
type QuantizedElement struct {
	Storage    ElemType
	Expressed  ElemType
	Scales     []float64
	ZeroPoints []int64
	Dimension  *int64 // non-nil => per-axis
	StorageMin *int64 // optional storage range
	StorageMax *int64
}

// Axis is a convenience for setting QuantizedElement.Dimension.
func Axis(d int64) *int64 { return &d }

func (q QuantizedElement) elemString() string {
	var b strings.Builder
	b.WriteString("!quant.uniform<")
	b.WriteString(string(q.Storage))
	if q.StorageMin != nil && q.StorageMax != nil {
		fmt.Fprintf(&b, "<%d:%d>", *q.StorageMin, *q.StorageMax)
	}
	b.WriteByte(':')
	b.WriteString(string(q.Expressed))
	if q.Dimension != nil {
		fmt.Fprintf(&b, ":%d", *q.Dimension)
	}
	b.WriteString(", ")
	pair := func(i int) string {
		s := formatFloat(q.Scales[i])
		if i < len(q.ZeroPoints) && q.ZeroPoints[i] != 0 {
			return s + ":" + strconv.FormatInt(q.ZeroPoints[i], 10)
		}
		return s
	}
	if q.Dimension != nil {
		b.WriteByte('{')
		for i := range q.Scales {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(pair(i))
		}
		b.WriteByte('}')
	} else if len(q.Scales) > 0 {
		b.WriteString(pair(0))
	}
	b.WriteByte('>')
	return b.String()
}

// Quant returns a tensor of the given quantized element type.
func Quant(q QuantizedElement, dims ...int64) Type { return tensorType{q, dims} }

// --- internal shape helpers (bookkeeping only, never verification) ---

// scalarOf returns the 0-d tensor of t's element, or t itself when t is
// not a tensor type.
func scalarOf(t Type) Type {
	if tt, ok := t.(tensorType); ok {
		return tensorType{elem: tt.elem}
	}
	return t
}

// withElem returns t with its element type replaced, or t when t is not a
// tensor type.
func withElem(t Type, e Element) Type {
	if tt, ok := t.(tensorType); ok {
		return tensorType{elem: e, dims: tt.dims}
	}
	return t
}

func tensorDims(t Type) ([]int64, bool) {
	tt, ok := t.(tensorType)
	if !ok {
		return nil, false
	}
	return tt.dims, true
}

func tupleElems(t Type) ([]Type, bool) {
	tt, ok := t.(tupleType)
	if !ok {
		return nil, false
	}
	return tt.elems, true
}

func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") && !strings.Contains(s, "inf") && !strings.Contains(s, "NaN") {
		s += ".0"
	}
	return s
}