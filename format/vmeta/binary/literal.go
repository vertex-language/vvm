// literal.go
package binary

import (
	"encoding/binary"
	"fmt"
	"math"

	vir "github.com/vertex-language/vvm/ir/vir"
)

// Literal tags. 0x01-0x04 are §F2.7's base set; 0x05/0x06 are this
// package's extension for bool/vector consts (package doc, limitation 4).
const (
	litInt    = 0x01
	litFloat  = 0x02
	litString = 0x03
	litNull   = 0x04
	litBool   = 0x05
	litVector = 0x06
)

// encodeLiteral encodes every literal kind except FLOAT, whose width
// isn't recoverable from a bare vir.Operand — use encodeLiteralTyped for
// consts, where the declared const type supplies it.
func encodeLiteral(strs *stringTable, o vir.Operand) ([]byte, error) {
	switch o.Kind {
	case vir.OperandInt:
		return append([]byte{litInt}, putSleb(nil, o.Int)...), nil
	case vir.OperandString:
		buf := []byte{litString}
		return putUleb(buf, uint64(strs.intern(o.Str))), nil
	case vir.OperandNull:
		return []byte{litNull}, nil
	case vir.OperandBool:
		b := byte(0)
		if o.Bool {
			b = 1
		}
		return []byte{litBool, b}, nil
	case vir.OperandVector:
		buf := []byte{litVector}
		buf = putUleb(buf, uint64(len(o.Vector)))
		for _, v := range o.Vector {
			buf = putSleb(buf, v)
		}
		return buf, nil
	default:
		return nil, fmt.Errorf("encodeLiteral: operand kind %d is not a literal (or needs encodeLiteralTyped)", o.Kind)
	}
}

// encodeLiteralTyped is encodeLiteral plus FLOAT support, needing t to
// know the IEEE width to store (§F2.7).
func encodeLiteralTyped(strs *stringTable, o vir.Operand, t vir.Type) ([]byte, error) {
	if o.Kind != vir.OperandFloat {
		return encodeLiteral(strs, o)
	}
	ft, ok := t.(vir.FloatType)
	if !ok {
		return nil, fmt.Errorf("float literal for non-float type %s", t)
	}
	buf := []byte{litFloat, byte(ft.Bits)}
	switch ft.Bits {
	case 64:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(o.Float))
		return append(buf, b[:]...), nil
	case 32:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(float32(o.Float)))
		return append(buf, b[:]...), nil
	case 16:
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], f16bits(o.Float))
		return append(buf, b[:]...), nil
	default:
		return nil, fmt.Errorf("float width f%d not one of 16/32/64", ft.Bits)
	}
}

// f16bits does a simplified truncating float64->float16 bit conversion —
// not correctly-rounded, matching format/vbyte/binary's own documented
// TODO (round-to-nearest-even) rather than inventing a different
// approximation here.
func f16bits(v float64) uint16 {
	bits := math.Float32bits(float32(v))
	sign := uint16((bits >> 16) & 0x8000)
	exp := int32((bits>>23)&0xff) - 127 + 15
	mant := bits & 0x7fffff
	if exp <= 0 {
		return sign
	}
	if exp >= 0x1f {
		return sign | 0x7c00
	}
	return sign | uint16(exp)<<10 | uint16(mant>>13)
}

func f16tofloat64(bits uint16) float64 {
	sign := uint32(bits&0x8000) << 16
	exp := (bits >> 10) & 0x1f
	mant := uint32(bits & 0x3ff)
	switch {
	case exp == 0 && mant == 0:
		return math.Float64frombits(uint64(sign) << 32)
	case exp == 0: // subnormal
		f := float32(mant) / 1024.0 / 16384.0
		if sign != 0 {
			f = -f
		}
		return float64(f)
	case exp == 0x1f && mant == 0:
		return float64(math.Float32frombits(sign | 0x7f800000))
	case exp == 0x1f:
		return float64(math.Float32frombits(sign | 0x7fc00000))
	default:
		f32exp := uint32(int32(exp) - 15 + 127)
		return float64(math.Float32frombits(sign | f32exp<<23 | mant<<13))
	}
}

// decodeLiteral is fully self-contained — FLOAT carries its own width
// byte inline, so no external type context is needed to decode (only to
// encode, where a bare float64 doesn't know its intended width).
func decodeLiteral(strs []string, data []byte) (vir.Operand, int, error) {
	if len(data) == 0 {
		return vir.Operand{}, 0, fmt.Errorf("truncated literal")
	}
	switch data[0] {
	case litInt:
		v, n, err := readSleb(data[1:])
		if err != nil {
			return vir.Operand{}, 0, err
		}
		return vir.IntLiteral(v), 1 + n, nil
	case litFloat:
		if len(data) < 2 {
			return vir.Operand{}, 0, fmt.Errorf("truncated float literal")
		}
		width := data[1]
		n := 2
		var f float64
		switch width {
		case 64:
			if len(data) < n+8 {
				return vir.Operand{}, 0, fmt.Errorf("truncated f64 literal")
			}
			f = math.Float64frombits(binary.LittleEndian.Uint64(data[n : n+8]))
			n += 8
		case 32:
			if len(data) < n+4 {
				return vir.Operand{}, 0, fmt.Errorf("truncated f32 literal")
			}
			f = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[n : n+4])))
			n += 4
		case 16:
			if len(data) < n+2 {
				return vir.Operand{}, 0, fmt.Errorf("truncated f16 literal")
			}
			f = f16tofloat64(binary.LittleEndian.Uint16(data[n : n+2]))
			n += 2
		default:
			return vir.Operand{}, 0, fmt.Errorf("float width f%d not one of 16/32/64", width)
		}
		return vir.FloatLiteral(f), n, nil
	case litString:
		idx, n, err := readUleb(data[1:])
		if err != nil {
			return vir.Operand{}, 0, err
		}
		s, err := strAt(strs, idx)
		if err != nil {
			return vir.Operand{}, 0, err
		}
		return vir.StringLiteral(s), 1 + n, nil
	case litNull:
		return vir.NullLiteral(), 1, nil
	case litBool:
		if len(data) < 2 {
			return vir.Operand{}, 0, fmt.Errorf("truncated bool literal")
		}
		return vir.BoolLiteral(data[1] != 0), 2, nil
	case litVector:
		count, n, err := readUleb(data[1:])
		if err != nil {
			return vir.Operand{}, 0, err
		}
		n++
		vals := make([]int64, count)
		for i := uint64(0); i < count; i++ {
			v, m, err := readSleb(data[n:])
			if err != nil {
				return vir.Operand{}, 0, err
			}
			vals[i] = v
			n += m
		}
		return vir.VectorLiteral(vals...), n, nil
	default:
		return vir.Operand{}, 0, fmt.Errorf("unrecognized literal tag 0x%02x", data[0])
	}
}