package ptx

import (
	"encoding/binary"
	"math"

	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Module consts (§2) become .const variables. Scalars are additionally recorded
// as immediates and inlined at every use site, which is both what PTX wants and
// what makes them uniform sources for free (§7.4).

func (l *lowerer) lowerConsts() error {
	for _, c := range l.gm.Constants {
		l.constType[c.Name] = c.Type

		if imm, ok := l.scalarConstImm(c); ok {
			l.constImm[c.Name] = imm
		}

		size, err := l.gm.SizeOf(c.Type)
		if err != nil {
			return todof("const %s: %w", c.Name, err)
		}
		align, err := l.gm.AlignOf(c.Type)
		if err != nil {
			return todof("const %s: %w", c.Name, err)
		}
		bytes, err := l.constBytes(c.Type, c.Init, size)
		if err != nil {
			return todof("const %s: %w", c.Name, err)
		}
		vals := make([]uint64, len(bytes))
		for i, b := range bytes {
			vals[i] = uint64(b)
		}
		l.constVar[c.Name] = l.pm.Var(ptx.Var{
			Space: ptx.Const,
			Align: align,
			Type:  ptx.B8,
			Name:  c.Name,
			Len:   size,
			Init:  ptx.InitU(vals...),
		})
	}
	return nil
}

func (l *lowerer) scalarConstImm(c *gvir.Const) (ptx.Operand, bool) {
	lit, ok := c.Init.(gvir.InitLiteral)
	if !ok {
		if _, isZero := c.Init.(gvir.InitZero); isZero && !gvir.IsAggregate(c.Type) {
			imm, err := immOperand(gvir.IntLiteral(0), c.Type)
			return imm, err == nil
		}
		return nil, false
	}
	if gvir.IsAggregate(c.Type) {
		return nil, false
	}
	imm, err := immOperand(lit.Value, c.Type)
	return imm, err == nil
}

// constBytes renders an initializer as the byte image of its type, using the
// §4.7 layout this module already computes for every other purpose.
func (l *lowerer) constBytes(t gvir.Type, init gvir.ConstInit, size int) ([]byte, error) {
	out := make([]byte, size)
	if err := l.writeConst(out, t, init); err != nil {
		return nil, err
	}
	return out, nil
}

func (l *lowerer) writeConst(dst []byte, t gvir.Type, init gvir.ConstInit) error {
	switch x := init.(type) {
	case gvir.InitZero:
		return nil // dst is already zeroed

	case gvir.InitLiteral:
		return l.writeScalar(dst, t, x.Value)

	case gvir.InitAggregate:
		switch ty := t.(type) {
		case gvir.ArrayType:
			esz, err := l.gm.SizeOf(ty.Elem)
			if err != nil {
				return err
			}
			for i, e := range x.Elems {
				if (i+1)*esz > len(dst) {
					return todof("array initializer has more elements than %s holds", ty)
				}
				if err := l.writeConst(dst[i*esz:(i+1)*esz], ty.Elem, e); err != nil {
					return err
				}
			}
			return nil
		case gvir.StructType:
			s := l.gm.StructByName(ty.Name)
			if s == nil {
				return todof("undeclared struct %s", ty.Name)
			}
			lay, err := l.gm.StructLayout(s)
			if err != nil {
				return err
			}
			for i, e := range x.Elems {
				if i >= len(lay.Fields) {
					return todof("struct initializer has more elements than %s has fields", ty)
				}
				fo := lay.Fields[i]
				if err := l.writeConst(dst[fo.Offset:fo.Offset+fo.Size], fo.Type, e); err != nil {
					return err
				}
			}
			return nil
		case gvir.VecType:
			esz, err := l.gm.SizeOf(ty.Elem)
			if err != nil {
				return err
			}
			for i, e := range x.Elems {
				if err := l.writeConst(dst[i*esz:(i+1)*esz], ty.Elem, e); err != nil {
					return err
				}
			}
			return nil
		}
		return todof("aggregate initializer for non-aggregate %s", t)
	}
	return todof("unknown const initializer for %s", t)
}

func (l *lowerer) writeScalar(dst []byte, t gvir.Type, o gvir.Operand) error {
	var bits uint64
	switch x := t.(type) {
	case gvir.IntType:
		bits = uint64(o.Int)
		if o.Kind == gvir.OperandBool && o.Bool {
			bits = 1
		}
	case gvir.PtrType:
		bits = uint64(o.Int) // only `null` is expressible; there is no addr-of
	case gvir.FloatType:
		switch {
		case x.Brain:
			bits = uint64(bf16bits(o.Float))
		case x.Bits == 16:
			bits = uint64(f16bits(o.Float))
		case x.Bits == 32:
			bits = uint64(math.Float32bits(float32(o.Float)))
		case x.Bits == 64:
			bits = math.Float64bits(o.Float)
		}
	default:
		return todof("no constant encoding for %s", t)
	}

	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], bits)
	copy(dst, buf[:min(len(dst), 8)])
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}