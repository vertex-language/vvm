// globals.go
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// lowerGlobal turns a global's initializer into raw bytes plus fixups.
//
// This is the only place endianness reaches: A64 instruction fetch is
// little-endian on aarch64_be too (BE-8), so only data words flip.
func lowerGlobal(ix *index, g *vir.Global) (*Global, error) {
	size, err := ix.layout.Size(g.Type)
	if err != nil {
		return nil, err
	}
	align, err := ix.layout.Align(g.Type)
	if err != nil {
		return nil, err
	}
	if g.Align != 0 {
		align = uint32(g.Align)
	}

	out := &Global{
		Name:   ix.symOf[g.Name],
		Size:   size,
		Align:  align,
		Export: g.Export,
		TLS:    g.TLS,
	}

	if _, zero := g.Init.(vir.InitZero); zero || g.Init == nil {
		// Zero storage carries no bytes: a BSS-style global is described by
		// its size alone.
		return out, nil
	}

	w := &dataWriter{ix: ix}
	if err := w.init(g.Type, g.Init); err != nil {
		return nil, err
	}
	for uint32(len(w.b)) < size {
		w.b = append(w.b, 0)
	}
	out.Data = w.b
	out.Fixups = w.fx
	return out, nil
}

type dataWriter struct {
	ix *index
	b  []byte
	fx []Fixup
}

func (w *dataWriter) pad(to uint32) {
	for uint32(len(w.b)) < to {
		w.b = append(w.b, 0)
	}
}

func (w *dataWriter) scalar(v uint64, n uint32) {
	buf := make([]byte, n)
	for i := uint32(0); i < n; i++ {
		buf[i] = byte(v >> (8 * i))
	}
	if w.ix.bigEndian {
		for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
			buf[i], buf[j] = buf[j], buf[i]
		}
	}
	w.b = append(w.b, buf...)
}

func (w *dataWriter) init(t vir.Type, in vir.ConstInit) error {
	switch x := in.(type) {
	case vir.InitZero:
		n, err := w.ix.layout.Size(t)
		if err != nil {
			return err
		}
		w.pad(uint32(len(w.b)) + n)
		return nil

	case vir.InitLiteral:
		n, err := w.ix.layout.Size(t)
		if err != nil {
			return err
		}
		switch x.Value.Kind {
		case vir.OperandInt:
			w.scalar(uint64(x.Value.Int), n)
		case vir.OperandBool:
			var v uint64
			if x.Value.Bool {
				v = 1
			}
			w.scalar(v, n)
		case vir.OperandNull:
			w.scalar(0, n)
		case vir.OperandFloat:
			return todo("float global initializer")
		default:
			return fmt.Errorf("literal %s is not a legal initializer", x.Value)
		}
		return nil

	case vir.InitAddressOf:
		if w.ix.globals[x.Name] != nil && w.ix.globals[x.Name].TLS {
			return fmt.Errorf("addr of a tls global is illegal (§6.2)")
		}
		sym, ok := w.ix.symOf[x.Name]
		if !ok {
			return fmt.Errorf("addr %s names nothing declared earlier", x.Name)
		}
		// A whole 64-bit data word — the relocation the encoder's
		// instruction-field vocabulary cannot name, and the reason this
		// package defines its own FixupKind.
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: sym, Kind: FixupAbs64})
		w.scalar(0, 8)
		return nil

	case vir.InitByteString:
		w.b = append(w.b, x.Data...)
		return nil

	case vir.InitAggregate:
		switch tt := t.(type) {
		case vir.ArrayType:
			esz, err := w.ix.layout.Size(tt.Elem)
			if err != nil {
				return err
			}
			eal, err := w.ix.layout.Align(tt.Elem)
			if err != nil {
				return err
			}
			stride := roundUp(esz, eal)
			base := uint32(len(w.b))
			for i, e := range x.Elems {
				w.pad(base + uint32(i)*stride)
				if err := w.init(tt.Elem, e); err != nil {
					return err
				}
			}
			w.pad(base + uint32(tt.Len)*stride)
			return nil

		case vir.StructType:
			sl, err := w.ix.layout.Struct(tt)
			if err != nil {
				return err
			}
			s := w.ix.layout.structs[tt.Name]
			base := uint32(len(w.b))
			for i, e := range x.Elems {
				if i >= len(s.Fields) {
					return fmt.Errorf("struct %s has %d fields, initializer has %d", tt.Name, len(s.Fields), len(x.Elems))
				}
				w.pad(base + sl.offsets[i])
				if err := w.init(s.Fields[i].Type, e); err != nil {
					return err
				}
			}
			w.pad(base + sl.size)
			return nil
		}
		return fmt.Errorf("aggregate initializer for non-aggregate type %s", t)
	}
	return fmt.Errorf("unhandled initializer %T", in)
}