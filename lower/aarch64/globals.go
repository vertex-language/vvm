package aarch64

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// ---------------------------------------------------------------------------
// Globals. Scalars are serialized in the requested arch's byte order — the
// ONE place Arch.Big() matters in this package; layout offsets are
// identical either way.
// ---------------------------------------------------------------------------

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
	w := &dataw{lay: lw.lay, be: lw.arch.Big()}
	if err := w.emit(g.Init, g.Type); err != nil {
		return Global{}, err
	}
	for len(w.b) < sz {
		w.b = append(w.b, 0)
	}
	out.Data, out.Fixups = w.b, w.fx
	return out, nil
}

type dataw struct {
	lay *Layout
	be  bool
	b   []byte
	fx  []Fixup
}

func (w *dataw) pad(to int) {
	for len(w.b) < to {
		w.b = append(w.b, 0)
	}
}

func (w *dataw) scalar(v uint64, n int) {
	if w.be {
		for i := n - 1; i >= 0; i-- {
			w.b = append(w.b, byte(v>>(8*i)))
		}
		return
	}
	for i := 0; i < n; i++ {
		w.b = append(w.b, byte(v>>(8*i)))
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
		w.b = append(w.b, x.Data...)
		return nil
	case vir.InitAddressOf:
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: x.Name, Kind: FixupAbs64})
		w.scalar(0, 8)
		return nil
	case vir.InitLiteral:
		return w.lit(x.Value, t)
	case vir.InitAggregate:
		switch tt := t.(type) {
		case vir.StructType:
			base := len(w.b)
			sz, _, offs, err := w.lay.StructLayout(tt.Name)
			if err != nil {
				return err
			}
			s, ok := w.lay.Struct(tt.Name)
			if !ok {
				return fmt.Errorf("struct %q not declared", tt.Name)
			}
			for i, e := range x.Elems {
				w.pad(base + offs[s.Fields[i].Name])
				if err := w.emit(e, s.Fields[i].Type); err != nil {
					return err
				}
			}
			w.pad(base + sz)
			return nil
		case vir.ArrayType:
			base := len(w.b)
			es, err := w.lay.Size(tt.Elem)
			if err != nil {
				return err
			}
			for _, e := range x.Elems {
				if err := w.emit(e, tt.Elem); err != nil {
					return err
				}
			}
			w.pad(base + es*tt.Len)
			return nil
		}
		return fmt.Errorf("aggregate initializer for %s", t)
	}
	return fmt.Errorf("unknown initializer form")
}

func (w *dataw) lit(o vir.Operand, t vir.Type) error {
	switch o.Kind {
	case vir.OperandInt:
		sz, err := w.lay.Size(t)
		if err != nil {
			return err
		}
		w.scalar(uint64(o.Int), sz)
		return nil
	case vir.OperandBool:
		v := uint64(0)
		if o.Bool {
			v = 1
		}
		w.scalar(v, 1)
		return nil
	case vir.OperandNull:
		w.scalar(0, 8)
		return nil
	case vir.OperandFloat:
		switch t {
		case vir.F64:
			w.scalar(math.Float64bits(o.Float), 8)
			return nil
		case vir.F32:
			w.scalar(uint64(math.Float32bits(float32(o.Float))), 4)
			return nil
		}
		return fmt.Errorf("f16 initializers not yet emitted on aarch64 (TODO)")
	case vir.OperandVector:
		vt, ok := t.(vir.VecType)
		if !ok {
			return fmt.Errorf("vector literal for %s", t)
		}
		es, err := w.lay.Size(vt.Elem)
		if err != nil {
			return err
		}
		for _, v := range o.Vector {
			w.scalar(uint64(v), es)
		}
		return nil
	}
	return fmt.Errorf("literal kind %d in initializer", o.Kind)
}