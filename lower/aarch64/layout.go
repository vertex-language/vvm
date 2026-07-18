package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// layout implements §7.1 for AAPCS64: fields at increasing offsets, each at
// its natural alignment, trailing padding to the largest field alignment.
// usize is i64; ptr is 8 bytes; i64/f64 align to 8, i128 to 16. The maximum
// fundamental alignment is 16 (quad-words and 128-bit vectors).
type layout struct {
	m       *vir.Module
	structs map[string]*vir.Struct
}

func newLayout(m *vir.Module) *layout {
	l := &layout{m: m, structs: map[string]*vir.Struct{}}
	for _, s := range m.Structs {
		l.structs[s.Name] = s
	}
	return l
}

func roundUp(n, a int) int { return (n + a - 1) &^ (a - 1) }

func (l *layout) size(t vir.Type) (int, error) {
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
		return 8, nil // usize is i64 on aarch64 (§10.1)
	case vir.VecType:
		es, err := l.size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * x.Len, nil
	case vir.ArrayType:
		es, err := l.size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * x.Len, nil
	case vir.StructType:
		sz, _, _, err := l.structLayout(x.Name)
		return sz, err
	}
	return 0, fmt.Errorf("layout: %s has no size", t)
}

func (l *layout) alignOf(t vir.Type) (int, error) {
	switch x := t.(type) {
	case vir.IntType, vir.FloatType, vir.PtrType:
		sz, err := l.size(t)
		if err != nil {
			return 0, err
		}
		if sz > 16 {
			return 16, nil // AAPCS64: max fundamental alignment is 16
		}
		return sz, nil
	case vir.VecType:
		sz, err := l.size(t)
		if err != nil {
			return 0, err
		}
		if sz > 16 {
			return 16, nil // 128-bit Q registers align to 16 (AAPCS64)
		}
		return sz, nil
	case vir.ArrayType:
		return l.alignOf(x.Elem)
	case vir.StructType:
		_, al, _, err := l.structLayout(x.Name)
		return al, err
	}
	return 0, fmt.Errorf("layout: %s has no alignment", t)
}

// structLayout returns (size, align, field offsets) per §7.1.
func (l *layout) structLayout(name string) (int, int, map[string]int, error) {
	s, ok := l.structs[name]
	if !ok {
		return 0, 0, nil, fmt.Errorf("layout: struct %q not declared", name)
	}
	off, align := 0, 1
	offs := map[string]int{}
	for _, f := range s.Fields {
		fa, err := l.alignOf(f.Type)
		if err != nil {
			return 0, 0, nil, err
		}
		fs, err := l.size(f.Type)
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

func (l *layout) fieldOffset(structName, field string) (int, error) {
	_, _, offs, err := l.structLayout(structName)
	if err != nil {
		return 0, err
	}
	o, ok := offs[field]
	if !ok {
		return 0, fmt.Errorf("layout: struct %s has no field %q", structName, field)
	}
	return o, nil
}