// globals.go
package arm

import (
	"encoding/binary"
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

func (x *index) lowerGlobal(g *vir.Global) (Global, error) {
	size, err := x.layout.Size(g.Type)
	if err != nil {
		return Global{}, err
	}
	align := uint32(g.Align)
	if align == 0 {
		if align, err = x.layout.Align(g.Type); err != nil {
			return Global{}, err
		}
	}
	sym, _ := x.symbol(g.Name)
	out := Global{Name: sym, Size: size, Align: align, Export: g.Export, TLS: g.TLS}

	if _, zero := g.Init.(vir.InitZero); zero || g.Init == nil {
		return out, nil // BSS-style: Size with no Data
	}
	data := make([]byte, size)
	var fixups []Fixup
	if err := x.initBytes(g.Init, g.Type, data, 0, &fixups); err != nil {
		return Global{}, err
	}
	out.Data, out.Fixups = data, fixups
	return out, nil
}

// initBytes writes one const-init form into buf at off. Byte order follows
// the target: armeb lays data words out big-endian even though the
// instruction stream stays little-endian on BE-8.
func (x *index) initBytes(init vir.ConstInit, t vir.Type, buf []byte, off uint32, fx *[]Fixup) error {
	size, err := x.layout.Size(t)
	if err != nil {
		return err
	}
	switch v := init.(type) {
	case vir.InitZero:
		return nil // buf is already zeroed

	case vir.InitByteString:
		if uint32(len(v.Data)) > size {
			return fmt.Errorf("byte string of %d bytes does not fit %s", len(v.Data), t)
		}
		copy(buf[off:], v.Data)
		return nil

	case vir.InitAddressOf:
		sym, ok := x.symbol(v.Name)
		if !ok {
			return fmt.Errorf("addr %s names nothing in this module", v.Name)
		}
		if size != 4 {
			return fmt.Errorf("addr initializer needs a 4-byte target type, not %s", t)
		}
		*fx = append(*fx, Fixup{Offset: off, Symbol: sym, Kind: FixupAbs32})
		return nil

	case vir.InitLiteral:
		return x.literalBytes(v.Value, t, buf, off)

	case vir.InitAggregate:
		switch a := t.(type) {
		case vir.ArrayType:
			es, err := x.layout.Size(a.Elem)
			if err != nil {
				return err
			}
			for i, e := range v.Elems {
				if err := x.initBytes(e, a.Elem, buf, off+uint32(i)*es, fx); err != nil {
					return err
				}
			}
			return nil
		case vir.StructType:
			s, ok := x.layout.structs[a.Name]
			if !ok {
				return fmt.Errorf("no struct %q", a.Name)
			}
			sl, err := x.layout.structOf(a)
			if err != nil {
				return err
			}
			if len(v.Elems) != len(s.Fields) {
				return fmt.Errorf("struct %s takes %d initializers, got %d",
					a.Name, len(s.Fields), len(v.Elems))
			}
			for i, e := range v.Elems {
				if err := x.initBytes(e, s.Fields[i].Type, buf, off+sl.Offsets[i], fx); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("aggregate initializer for non-aggregate %s", t)
	}
	return fmt.Errorf("unknown initializer %T", init)
}

func (x *index) literalBytes(o vir.Operand, t vir.Type, buf []byte, off uint32) error {
	size, err := x.layout.Size(t)
	if err != nil {
		return err
	}
	var v uint64
	switch o.Kind {
	case vir.OperandInt:
		v = uint64(o.Int)
	case vir.OperandBool:
		if o.Bool {
			v = 1
		}
	case vir.OperandNull:
		v = 0
	case vir.OperandFloat:
		return todo("float global initializers")
	default:
		return fmt.Errorf("%s is not a literal initializer", o)
	}
	if size > 8 {
		return todo("%d-byte scalar initializers", size)
	}
	var tmp [8]byte
	if x.be {
		binary.BigEndian.PutUint64(tmp[:], v<<(8*(8-size)))
	} else {
		binary.LittleEndian.PutUint64(tmp[:], v)
	}
	copy(buf[off:off+size], tmp[:size])
	return nil
}