// globals.go
package x86_64

import (
	"encoding/binary"
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	enc "github.com/vertex-language/vvm/isa/x86_64/encoder"
)

// lowerGlobal flattens a global initializer into raw bytes plus fixups. A
// relocated pointer (`addr ident`) becomes a full-width abs64 relocation —
// the only 64-bit-wide relocation a data section needs here.
func lowerGlobal(m *vir.Module, g *vir.Global) (Global, error) {
	l := newLayout(newIndex(m))
	size, err := l.Size(g.Type)
	if err != nil {
		return Global{}, err
	}
	align, err := l.Align(g.Type)
	if err != nil {
		return Global{}, err
	}
	if g.Align > 0 {
		align = int64(g.Align)
	}

	out := Global{
		Name:   g.Name,
		Size:   uint32(size),
		Align:  uint32(align),
		Export: g.Export,
		TLS:    g.TLS,
	}

	if _, isZero := g.Init.(vir.InitZero); isZero {
		out.Data = nil // BSS-style zero fill
		return out, nil
	}

	buf := make([]byte, size)
	fx, err := writeInit(l, buf, 0, g.Type, g.Init)
	if err != nil {
		return Global{}, err
	}
	out.Data = buf
	out.Fixups = fx
	return out, nil
}

func writeInit(l *Layout, buf []byte, off int64, t vir.Type, init vir.ConstInit) ([]enc.Fixup, error) {
	switch v := init.(type) {
	case vir.InitZero:
		return nil, nil // buf is already zero
	case vir.InitByteString:
		copy(buf[off:], v.Data)
		return nil, nil
	case vir.InitAddressOf:
		// A relocated pointer to another symbol: 8-byte abs64 hole.
		return []enc.Fixup{{
			Offset: uint32(off), Symbol: v.Name, Kind: enc.FixupAbs64, Addend: 0,
		}}, nil
	case vir.InitLiteral:
		return writeScalar(buf, off, t, v.Value)
	case vir.InitAggregate:
		return writeAggregate(l, buf, off, t, v.Elems)
	}
	return nil, fmt.Errorf("unsupported initializer %T", init)
}

func writeAggregate(l *Layout, buf []byte, off int64, t vir.Type, elems []vir.ConstInit) ([]enc.Fixup, error) {
	var fx []enc.Fixup
	switch x := t.(type) {
	case vir.ArrayType:
		es, err := l.Size(x.Elem)
		if err != nil {
			return nil, err
		}
		for i, e := range elems {
			sub, err := writeInit(l, buf, off+int64(i)*es, x.Elem, e)
			if err != nil {
				return nil, err
			}
			fx = append(fx, sub...)
		}
	case vir.StructType:
		s, ok := l.ix.structs[x.Name]
		if !ok {
			return nil, fmt.Errorf("unknown struct %s", x.Name)
		}
		for i, e := range elems {
			fo, err := l.FieldOffset(x.Name, s.Fields[i].Name)
			if err != nil {
				return nil, err
			}
			sub, err := writeInit(l, buf, off+fo, s.Fields[i].Type, e)
			if err != nil {
				return nil, err
			}
			fx = append(fx, sub...)
		}
	default:
		return nil, fmt.Errorf("aggregate initializer for non-aggregate %s", t)
	}
	return fx, nil
}

func writeScalar(buf []byte, off int64, t vir.Type, v vir.Operand) ([]enc.Fixup, error) {
	switch x := t.(type) {
	case vir.IntType:
		u := uint64(v.Int)
		putUint(buf[off:], u, (x.Bits+7)/8)
		return nil, nil
	case vir.PtrType:
		if v.Kind == vir.OperandNull {
			return nil, nil // zero
		}
		binary.LittleEndian.PutUint64(buf[off:], uint64(v.Int))
		return nil, nil
	case vir.FloatType:
		return nil, todo("float global initializer")
	}
	return nil, fmt.Errorf("cannot initialize scalar of type %s", t)
}

func putUint(b []byte, v uint64, nbytes int) {
	for i := 0; i < nbytes && i < len(b); i++ {
		b[i] = byte(v >> (8 * i))
	}
}