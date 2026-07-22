// File: format/vbyte/binary/literal.go
package binary

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Literal tags (§7). 0x01-0x08 are legal both as instruction/const operands
// and inside initializers; 0x0F-0x12 are init-only (see constinit.go).
const (
	litTagInt    = 0x01
	litTagFloat  = 0x02
	litTagString = 0x03
	litTagBool   = 0x04
	litTagNull   = 0x05
	litTagNaN    = 0x06
	litTagInf    = 0x07
	litTagNegInf = 0x08
)

var canonicalNaNBits = math.Float64bits(math.NaN())

// encodeLiteralOperand writes op as a §7 literal (tags 0x01-0x08). NaN
// payloads are passed through bit-exact via the raw f64 tag (0x02) unless
// the value is exactly Go's canonical NaN, in which case the dedicated
// tag-only "NaN" form (0x06) is used, matching the text grammar's separate
// "NaN" literal keyword (§2.3) as distinct from an arbitrary NaN bit
// pattern arriving via a raw float constant.
func encodeLiteralOperand(w *writer, op vir.Operand, ec *encodeContext) error {
	switch op.Kind {
	case vir.OperandInt:
		w.u8(litTagInt)
		w.sleb(op.Int)
	case vir.OperandFloat:
		switch {
		case math.IsNaN(op.Float) && math.Float64bits(op.Float) == canonicalNaNBits:
			w.u8(litTagNaN)
		case math.IsNaN(op.Float):
			w.u8(litTagFloat)
			w.f64(op.Float)
		case math.IsInf(op.Float, 1):
			w.u8(litTagInf)
		case math.IsInf(op.Float, -1):
			w.u8(litTagNegInf)
		default:
			w.u8(litTagFloat)
			w.f64(op.Float)
		}
	case vir.OperandString:
		w.u8(litTagString)
		id, err := ec.strings.id(op.Str)
		if err != nil {
			return err
		}
		w.idx(id)
	case vir.OperandBool:
		w.u8(litTagBool)
		w.boolean(op.Bool)
	case vir.OperandNull:
		w.u8(litTagNull)
	default:
		return fmt.Errorf("vbyte: operand kind %v is not a legal literal (§7)", op.Kind)
	}
	return nil
}

func decodeLiteralOperand(r *reader, dc *decodeContext) (vir.Operand, error) {
	tag, err := r.u8()
	if err != nil {
		return vir.Operand{}, err
	}
	return decodeLiteralPayload(r, dc, tag)
}

// decodeLiteralPayload decodes the payload for an already-read literal tag
// (tags 0x01-0x08 only). Shared between operand-position literals and
// const_init literals.
func decodeLiteralPayload(r *reader, dc *decodeContext, tag byte) (vir.Operand, error) {
	switch tag {
	case litTagInt:
		v, err := r.sleb()
		if err != nil {
			return vir.Operand{}, err
		}
		return vir.IntLiteral(v), nil
	case litTagFloat:
		v, err := r.f64()
		if err != nil {
			return vir.Operand{}, err
		}
		return vir.FloatLiteral(v), nil
	case litTagString:
		id, err := r.idx()
		if err != nil {
			return vir.Operand{}, err
		}
		s, err := stringAt(dc.strings, id)
		if err != nil {
			return vir.Operand{}, err
		}
		return vir.StringLiteral(s), nil
	case litTagBool:
		b, err := r.boolean()
		if err != nil {
			return vir.Operand{}, err
		}
		return vir.BoolLiteral(b), nil
	case litTagNull:
		return vir.NullLiteral(), nil
	case litTagNaN:
		return vir.FloatLiteral(math.NaN()), nil
	case litTagInf:
		return vir.FloatLiteral(math.Inf(1)), nil
	case litTagNegInf:
		return vir.FloatLiteral(math.Inf(-1)), nil
	default:
		return vir.Operand{}, fmt.Errorf("vbyte: invalid literal tag 0x%02x, expected 0x01-0x08 (§7)", tag)
	}
}