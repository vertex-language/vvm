package stablehlo

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// Literal is a constant tensor value, rendered as dense<...> : type.
type Literal struct {
	Repr string // the dense<...> part
	Ty   Type
}

func (l Literal) String() string { return l.Repr + " : " + l.Ty.String() }

// Splat builds dense<v> : t. v may be a Go integer, float, or bool.
func Splat(v any, t Type) Literal {
	return Literal{Repr: "dense<" + formatScalar(v) + ">", Ty: t}
}

// DenseF32 builds dense<[...]> : t from float32 data.
func DenseF32(vs []float32, t Type) Literal {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = formatFloat(float64(v))
	}
	return dense(parts, t)
}

// DenseF64 builds dense<[...]> : t from float64 data.
func DenseF64(vs []float64, t Type) Literal {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = formatFloat(v)
	}
	return dense(parts, t)
}

// DenseI64 builds dense<[...]> : t from int64 data.
func DenseI64(vs []int64, t Type) Literal {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return dense(parts, t)
}

// DenseBool builds dense<[...]> : t from bool data.
func DenseBool(vs []bool, t Type) Literal {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatBool(v)
	}
	return dense(parts, t)
}

// DenseHex builds dense<"0x..."> : t for bulk weights.
func DenseHex(data []byte, t Type) Literal {
	return Literal{Repr: `dense<"0x` + hex.EncodeToString(data) + `">`, Ty: t}
}

func dense(parts []string, t Type) Literal {
	return Literal{Repr: "dense<[" + strings.Join(parts, ", ") + "]>", Ty: t}
}

func formatScalar(v any) string {
	switch x := v.(type) {
	case float64:
		return formatFloat(x)
	case float32:
		return formatFloat(float64(x))
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	default:
		panic("stablehlo: unsupported scalar literal type")
	}
}