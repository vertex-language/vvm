// globals.go
package x86

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

func (lw *lowerer) lowerGlobal(g *vir.Global) (Global, error) {
	sz, err := lw.lay.Size(g.Type)
	if err != nil {
		return Global{}, err
	}
	al, err := lw.lay.AlignOf(g.Type)
	if err != nil {
		return Global{}, err
	}
	if g.Align > al {
		al = g.Align
	}
	out := Global{Name: g.Name, Size: uint32(sz), Align: uint32(al), Export: g.Export, TLS: g.TLS}
	if _, zero := g.Init.(vir.InitZero); zero {
		return out, nil
	}
	w := &dataw{lay: lw.lay}
	if err := w.emit(g.Init, g.Type); err != nil {
		return Global{}, err
	}
	// Over-long initializer data would run into whatever the object
	// writer places next, so it is an error rather than something to
	// truncate. Short data is just tail padding.
	if len(w.b) > sz {
		return Global{}, fmt.Errorf("initializer emitted %d bytes for a %d-byte object", len(w.b), sz)
	}
	w.pad(sz)
	out.Data, out.Fixups = w.b, w.fx
	return out, nil
}

type dataw struct {
	lay *Layout
	b   []byte
	fx  []Fixup
}

func (w *dataw) pad(to int) {
	for len(w.b) < to {
		w.b = append(w.b, 0)
	}
}

func (w *dataw) le(v uint64, n int) {
	for i := 0; i < n; i++ {
		w.b = append(w.b, byte(v>>(8*i)))
	}
}

// leInt writes a signed value in n little-endian bytes, sign-extending
// past 64 bits rather than zero-filling. Only i128 reaches the wide path,
// and a negative i128 initializer written with plain zero fill would come
// out as a huge positive number.
func (w *dataw) leInt(v int64, n int) {
	fill := byte(0)
	if v < 0 {
		fill = 0xFF
	}
	for i := 0; i < n; i++ {
		if i < 8 {
			w.b = append(w.b, byte(uint64(v)>>(8*i)))
			continue
		}
		w.b = append(w.b, fill)
	}
}

func (w *dataw) emit(init vir.ConstInit, t vir.Type) error {
	switch x := init.(type) {
	case vir.InitZero:
		sz, err := w.lay.Size(t)
		if err != nil {
			return err
		}
		w.pad(len(w.b) + sz)
		return nil

	case vir.InitByteString:
		sz, err := w.lay.Size(t)
		if err != nil {
			return err
		}
		if len(x.Data) > sz {
			return fmt.Errorf("byte string of %d bytes does not fit %s (%d bytes)", len(x.Data), t, sz)
		}
		w.b = append(w.b, x.Data...)
		return nil

	case vir.InitAddressOf:
		sz, err := w.lay.Size(t)
		if err != nil {
			return err
		}
		if sz != 4 {
			return fmt.Errorf("address-of initializer needs a 4-byte field, %s is %d", t, sz)
		}
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: x.Name, Kind: FixupAbs32})
		w.le(0, 4)
		return nil

	case vir.InitLiteral:
		return w.lit(x.Value, t)

	case vir.InitAggregate:
		return w.aggregate(x, t)
	}
	return fmt.Errorf("unknown initializer form")
}

func (w *dataw) aggregate(x vir.InitAggregate, t vir.Type) error {
	switch tt := t.(type) {
	case vir.StructType:
		s, ok := w.lay.Struct(tt.Name)
		if !ok {
			return fmt.Errorf("struct %q not declared", tt.Name)
		}
		// A trailing-field-omitting initializer is fine — the pad at the
		// end zero-fills it. More elements than fields is not, and used
		// to index straight off the end of s.Fields.
		if len(x.Elems) > len(s.Fields) {
			return fmt.Errorf("struct %s has %d fields but its initializer has %d elements",
				tt.Name, len(s.Fields), len(x.Elems))
		}
		sz, _, offs, err := w.lay.StructLayout(tt.Name)
		if err != nil {
			return err
		}
		base := len(w.b)
		for i, e := range x.Elems {
			w.pad(base + offs[s.Fields[i].Name])
			if err := w.emit(e, s.Fields[i].Type); err != nil {
				return fmt.Errorf("struct %s field %s: %w", tt.Name, s.Fields[i].Name, err)
			}
		}
		w.pad(base + sz)
		return nil

	case vir.ArrayType:
		if len(x.Elems) > tt.Len {
			return fmt.Errorf("array of %d has an initializer with %d elements", tt.Len, len(x.Elems))
		}
		es, err := w.lay.Size(tt.Elem)
		if err != nil {
			return err
		}
		base := len(w.b)
		for i, e := range x.Elems {
			// Pad to this element's own start rather than trusting the
			// previous emit to have written exactly es bytes: a nested
			// aggregate with omitted trailing fields writes fewer.
			w.pad(base + i*es)
			if err := w.emit(e, tt.Elem); err != nil {
				return err
			}
		}
		w.pad(base + es*tt.Len)
		return nil
	}
	return fmt.Errorf("aggregate initializer for %s", t)
}

func (w *dataw) lit(o vir.Operand, t vir.Type) error {
	sz, err := w.lay.Size(t)
	if err != nil {
		return err
	}
	switch o.Kind {
	case vir.OperandInt:
		w.leInt(o.Int, sz)
		return nil
	case vir.OperandBool:
		// Width comes from the declared type, not from the literal: a
		// bool literal initializing an i32 global still occupies four
		// bytes, and hardcoding one byte here left the object three
		// bytes short of its own declared size.
		v := int64(0)
		if o.Bool {
			v = 1
		}
		w.leInt(v, sz)
		return nil
	case vir.OperandNull:
		if sz != 4 {
			return fmt.Errorf("null initializer needs a 4-byte field, %s is %d", t, sz)
		}
		w.le(0, 4)
		return nil
	case vir.OperandFloat:
		ft, ok := t.(vir.FloatType)
		if !ok {
			return fmt.Errorf("float literal for %s", t)
		}
		switch ft.Bits {
		case 64:
			w.le(math.Float64bits(o.Float), 8)
			return nil
		case 32:
			w.le(uint64(math.Float32bits(float32(o.Float))), 4)
			return nil
		}
		return fmt.Errorf("f16 initializers not yet emitted on x86 (TODO)")
	case vir.OperandVector:
		vt, ok := t.(vir.VecType)
		if !ok {
			return fmt.Errorf("vector literal for %s", t)
		}
		if len(o.Vector) != vt.Len {
			return fmt.Errorf("%s initialized with %d elements", t, len(o.Vector))
		}
		es, err := w.lay.Size(vt.Elem)
		if err != nil {
			return err
		}
		for _, v := range o.Vector {
			w.leInt(v, es)
		}
		return nil
	}
	return fmt.Errorf("literal kind %d in initializer", o.Kind)
}