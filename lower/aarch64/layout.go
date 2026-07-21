package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Layout implements AAPCS64 §7.1: fields at increasing offsets, each at
// its natural alignment, trailing padding to the largest field alignment.
type Layout struct {
	m       *vir.Module
	structs map[string]*vir.Struct
}

func NewLayout(m *vir.Module) *Layout {
	l := &Layout{m: m, structs: map[string]*vir.Struct{}}
	for _, s := range m.Structs {
		l.structs[s.Name] = s
	}
	return l
}

func roundUp(n, a int) int { return (n + a - 1) &^ (a - 1) }

func (l *Layout) Size(t vir.Type) (int, error) {
	switch x := t.(type) {
	case vir.IntType:
		switch x.Bits {
		case 1, 8:
			return 1, nil
		case 16:
			return 2, nil
		case 32:
			return 4, nil
		case 64:
			return 8, nil
		case 128:
			return 16, nil
		}
		return 0, fmt.Errorf("layout: unsupported integer width i%d", x.Bits)
	case vir.FloatType:
		return x.Bits / 8, nil
	case vir.PtrType:
		return 8, nil
	case vir.VecType:
		es, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * x.Len, nil
	case vir.ArrayType:
		es, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * x.Len, nil
	case vir.StructType:
		sz, _, _, err := l.StructLayout(x.Name)
		return sz, err
	}
	return 0, fmt.Errorf("layout: %s has no size", t)
}

func (l *Layout) AlignOf(t vir.Type) (int, error) {
	switch x := t.(type) {
	case vir.IntType, vir.FloatType, vir.PtrType:
		sz, err := l.Size(t)
		if err != nil {
			return 0, err
		}
		if sz > 16 {
			return 16, nil
		}
		return sz, nil
	case vir.VecType:
		sz, err := l.Size(t)
		if err != nil {
			return 0, err
		}
		if sz > 16 {
			return 16, nil
		}
		return sz, nil
	case vir.ArrayType:
		return l.AlignOf(x.Elem)
	case vir.StructType:
		_, al, _, err := l.StructLayout(x.Name)
		return al, err
	}
	return 0, fmt.Errorf("layout: %s has no alignment", t)
}

// StructLayout returns (size, align, field offsets).
func (l *Layout) StructLayout(name string) (int, int, map[string]int, error) {
	s, ok := l.structs[name]
	if !ok {
		return 0, 0, nil, fmt.Errorf("layout: struct %q not declared", name)
	}
	off, align := 0, 1
	offs := map[string]int{}
	for _, f := range s.Fields {
		fa, err := l.AlignOf(f.Type)
		if err != nil {
			return 0, 0, nil, err
		}
		fs, err := l.Size(f.Type)
		if err != nil {
			return 0, 0, nil, err
		}
		off = roundUp(off, fa)
		offs[f.Name] = off
		off += fs
		if fa > align {
			align = fa
		}
	}
	return roundUp(off, align), align, offs, nil
}

func (l *Layout) FieldOffset(structName, field string) (int, error) {
	_, _, offs, err := l.StructLayout(structName)
	if err != nil {
		return 0, err
	}
	o, ok := offs[field]
	if !ok {
		return 0, fmt.Errorf("layout: struct %s has no field %q", structName, field)
	}
	return o, nil
}

// Struct returns the declaration backing a struct name, for global
// initializer emission.
func (l *Layout) Struct(name string) (*vir.Struct, bool) {
	s, ok := l.structs[name]
	return s, ok
}