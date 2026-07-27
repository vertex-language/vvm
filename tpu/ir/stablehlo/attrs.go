package stablehlo

import (
	"fmt"
	"strconv"
	"strings"
)

// Attr is a static op attribute. String returns the MLIR attribute
// spelling. A nil Attr in a NamedAttr denotes a unit attribute.
type Attr interface {
	String() string
}

// NamedAttr is a name = value attribute pair.
type NamedAttr struct {
	Name  string
	Value Attr // nil => unit attribute
}

// RawAttr builds a NamedAttr from a verbatim MLIR attribute value. This is
// the escape hatch for unspecced attributes (mhlo.sharding, layouts,
// frontend attributes, ...).
func RawAttr(name, value string) NamedAttr {
	return NamedAttr{Name: name, Value: rawAttr(value)}
}

// UnitAttr builds a unit attribute (prints as the bare name).
func UnitAttr(name string) NamedAttr { return NamedAttr{Name: name} }

type rawAttr string

func (r rawAttr) String() string { return string(r) }

// RawAttrValue wraps a verbatim MLIR attribute value as an Attr.
func RawAttrValue(s string) Attr { return rawAttr(s) }

// Typed attribute constructors.

func I64Attr(v int64) Attr  { return rawAttr(strconv.FormatInt(v, 10) + " : i64") }
func I32Attr(v int32) Attr  { return rawAttr(strconv.FormatInt(int64(v), 10) + " : i32") }
func BoolAttr(v bool) Attr  { return rawAttr(strconv.FormatBool(v)) }
func F32Attr(v float32) Attr {
	return rawAttr(formatFloat(float64(v)) + " : f32")
}
func F64Attr(v float64) Attr { return rawAttr(formatFloat(v) + " : f64") }
func StrAttr(s string) Attr  { return rawAttr(strconv.Quote(s)) }

// I64Array returns array<i64: vs...>.
func I64Array(vs ...int64) Attr {
	if len(vs) == 0 {
		return rawAttr("array<i64>")
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return rawAttr("array<i64: " + strings.Join(parts, ", ") + ">")
}

// BoolArray returns array<i1: vs...>.
func BoolArray(vs ...bool) Attr {
	if len(vs) == 0 {
		return rawAttr("array<i1>")
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatBool(v)
	}
	return rawAttr("array<i1: " + strings.Join(parts, ", ") + ">")
}

// DenseI64Matrix returns dense<[[...], ...]> : tensor<NxMxi64>, as used by
// replica_groups, padding, and source_target_pairs.
func DenseI64Matrix(rows [][]int64) Attr {
	n := len(rows)
	m := 0
	if n > 0 {
		m = len(rows[0])
	}
	var b strings.Builder
	b.WriteString("dense<[")
	for i, row := range rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('[')
		for j, v := range row {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.FormatInt(v, 10))
		}
		b.WriteByte(']')
	}
	fmt.Fprintf(&b, "]> : tensor<%dx%dxi64>", n, m)
	return rawAttr(b.String())
}

// SymRef spells a symbol reference @name.
func SymRef(name string) Attr { return rawAttr("@" + name) }

// ConvDims wraps a convolution dimension-numbers spelling, e.g.
// ConvDims("[b,0,1,f]x[0,1,i,o]->[b,0,1,f]") => #stablehlo.conv<...>.
func ConvDims(s string) Attr { return rawAttr("#stablehlo.conv<" + s + ">") }

// DotDims is the dot_general dimension-numbers attribute.
type DotDims struct {
	LhsBatch    []int64
	RhsBatch    []int64
	LhsContract []int64
	RhsContract []int64
}

func (d DotDims) String() string {
	return "#stablehlo.dot<lhs_batching_dimensions = " + bracketI64(d.LhsBatch) +
		", rhs_batching_dimensions = " + bracketI64(d.RhsBatch) +
		", lhs_contracting_dimensions = " + bracketI64(d.LhsContract) +
		", rhs_contracting_dimensions = " + bracketI64(d.RhsContract) + ">"
}

// GatherDims is the gather dimension-numbers attribute. Batching
// dimensions require StableHLO >= 1.1 and are printed only when set.
type GatherDims struct {
	OffsetDims               []int64
	CollapsedSliceDims       []int64
	OperandBatchingDims      []int64
	StartIndicesBatchingDims []int64
	StartIndexMap            []int64
	IndexVectorDim           int64
	IndicesAreSorted         bool
}

func (g GatherDims) String() string {
	var b strings.Builder
	b.WriteString("#stablehlo.gather<offset_dims = " + bracketI64(g.OffsetDims))
	b.WriteString(", collapsed_slice_dims = " + bracketI64(g.CollapsedSliceDims))
	if len(g.OperandBatchingDims) > 0 || len(g.StartIndicesBatchingDims) > 0 {
		b.WriteString(", operand_batching_dims = " + bracketI64(g.OperandBatchingDims))
		b.WriteString(", start_indices_batching_dims = " + bracketI64(g.StartIndicesBatchingDims))
	}
	b.WriteString(", start_index_map = " + bracketI64(g.StartIndexMap))
	fmt.Fprintf(&b, ", index_vector_dim = %d>", g.IndexVectorDim)
	return b.String()
}

// ScatterDims is the scatter dimension-numbers attribute.
type ScatterDims struct {
	UpdateWindowDims           []int64
	InsertedWindowDims         []int64
	OperandBatchingDims        []int64
	ScatterIndicesBatchingDims []int64
	ScatterDimsToOperandDims   []int64
	IndexVectorDim             int64
}

func (s ScatterDims) String() string {
	var b strings.Builder
	b.WriteString("#stablehlo.scatter<update_window_dims = " + bracketI64(s.UpdateWindowDims))
	b.WriteString(", inserted_window_dims = " + bracketI64(s.InsertedWindowDims))
	if len(s.OperandBatchingDims) > 0 || len(s.ScatterIndicesBatchingDims) > 0 {
		b.WriteString(", input_batching_dims = " + bracketI64(s.OperandBatchingDims))
		b.WriteString(", scatter_indices_batching_dims = " + bracketI64(s.ScatterIndicesBatchingDims))
	}
	b.WriteString(", scatter_dims_to_operand_dims = " + bracketI64(s.ScatterDimsToOperandDims))
	fmt.Fprintf(&b, ", index_vector_dim = %d>", s.IndexVectorDim)
	return b.String()
}

// ChannelHandle is the #stablehlo.channel_handle attribute.
type ChannelHandle struct {
	Handle int64
	Type   int64
}

func (c ChannelHandle) String() string {
	return fmt.Sprintf("#stablehlo.channel_handle<handle = %d, type = %d>", c.Handle, c.Type)
}

// Enums (typed constants rendered as #stablehlo<kind VALUE>).

type CompareDirection string

const (
	CmpEQ CompareDirection = "EQ"
	CmpNE CompareDirection = "NE"
	CmpGE CompareDirection = "GE"
	CmpGT CompareDirection = "GT"
	CmpLE CompareDirection = "LE"
	CmpLT CompareDirection = "LT"
)

type CompareType string

const (
	CompareFloat      CompareType = "FLOAT"
	CompareTotalOrder CompareType = "TOTALORDER"
	CompareSigned     CompareType = "SIGNED"
	CompareUnsigned   CompareType = "UNSIGNED"
)

type RngDistribution string

const (
	RngUniform RngDistribution = "UNIFORM"
	RngNormal  RngDistribution = "NORMAL"
)

type RngAlgorithm string

const (
	RngDefault  RngAlgorithm = "DEFAULT"
	RngThreeFry RngAlgorithm = "THREE_FRY"
	RngPhilox   RngAlgorithm = "PHILOX"
)

type TransposeKind string

const (
	NoTranspose TransposeKind = "NO_TRANSPOSE"
	TransposeT  TransposeKind = "TRANSPOSE"
	Adjoint     TransposeKind = "ADJOINT"
)

type FftKind string

const (
	FftFFT   FftKind = "FFT"
	FftIFFT  FftKind = "IFFT"
	FftRFFT  FftKind = "RFFT"
	FftIRFFT FftKind = "IRFFT"
)

func enumAttr(kind, value string) Attr {
	return rawAttr("#stablehlo<" + kind + " " + value + ">")
}

// AnyAttr coerces plain Go values (ints, floats, bools, strings, int64
// slices, Attr) to an Attr. This is the convenience-coercion surface
// mentioned in the design notes; operand positions remain typed Values.
func AnyAttr(v any) Attr {
	switch x := v.(type) {
	case Attr:
		return x
	case int:
		return I64Attr(int64(x))
	case int64:
		return I64Attr(x)
	case int32:
		return I32Attr(x)
	case float64:
		return F64Attr(x)
	case float32:
		return F32Attr(x)
	case bool:
		return BoolAttr(x)
	case string:
		return StrAttr(x)
	case []int64:
		return I64Array(x...)
	default:
		return rawAttr(fmt.Sprint(v))
	}
}

// --- helpers ---

func bracketI64(vs []int64) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type listAttr []Attr

func (l listAttr) String() string {
	parts := make([]string, len(l))
	for i, a := range l {
		parts[i] = a.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type dictAttr []NamedAttr

func (d dictAttr) String() string {
	parts := make([]string, len(d))
	for i, a := range d {
		if a.Value == nil {
			parts[i] = a.Name
		} else {
			parts[i] = a.Name + " = " + a.Value.String()
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func nattr(name string, a Attr) NamedAttr { return NamedAttr{Name: name, Value: a} }