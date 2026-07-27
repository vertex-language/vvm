// layout.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Layout sizes and aligns types per AAPCS32 (the ARM Procedure Call
// Standard's fundamental data types).
//
// The rule most likely to surprise someone arriving from lower/x86:
// **there is no 4-byte alignment cap.** IA-32 aligns an i64 to 4 bytes;
// AAPCS aligns it to 8, and a struct's alignment is its largest member's.
// Computing it the IA-32 way would not be binary-compatible with anything
// else on the platform. Pointers are 4 bytes, as on IA-32.
type Layout struct {
	structs map[string]*vir.Struct
	cache   map[string]*structLayout
	busy    map[string]bool // doubles as the by-value recursion guard
}

type structLayout struct {
	Size    uint32
	Align   uint32
	Offsets []uint32
}

func newLayout(structs map[string]*vir.Struct) *Layout {
	return &Layout{
		structs: structs,
		cache:   map[string]*structLayout{},
		busy:    map[string]bool{},
	}
}

func (l *Layout) Size(t vir.Type) (uint32, error) {
	switch x := t.(type) {
	case vir.IntType:
		return uint32((x.Bits + 7) / 8), nil
	case vir.FloatType:
		return uint32(x.Bits / 8), nil
	case vir.PtrType:
		return 4, nil
	case vir.VoidType:
		return 0, nil
	case vir.ValistType:
		// This backend's va_list is a single pointer (see isel_va.go).
		return 4, nil
	case vir.VecType:
		es, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * uint32(x.Len), nil
	case vir.ArrayType:
		es, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * uint32(x.Len), nil
	case vir.StructType:
		sl, err := l.structOf(x)
		if err != nil {
			return 0, err
		}
		return sl.Size, nil
	}
	return 0, fmt.Errorf("cannot size %s", t)
}

func (l *Layout) Align(t vir.Type) (uint32, error) {
	switch x := t.(type) {
	case vir.IntType:
		switch {
		case x.Bits <= 8:
			return 1, nil
		case x.Bits <= 16:
			return 2, nil
		case x.Bits <= 32:
			return 4, nil
		default:
			return 8, nil // i64/i128: 8-byte aligned, no IA-32 style cap
		}
	case vir.FloatType:
		if x.Bits >= 64 {
			return 8, nil
		}
		return uint32(x.Bits / 8), nil
	case vir.PtrType, vir.ValistType:
		return 4, nil
	case vir.VoidType:
		return 1, nil
	case vir.VecType:
		sz, err := l.Size(x)
		if err != nil {
			return 0, err
		}
		if sz >= 8 {
			return 8, nil
		}
		return sz, nil
	case vir.ArrayType:
		return l.Align(x.Elem)
	case vir.StructType:
		sl, err := l.structOf(x)
		if err != nil {
			return 0, err
		}
		return sl.Align, nil
	}
	return 0, fmt.Errorf("cannot align %s", t)
}

// FieldOffset resolves a struct field's byte offset and type.
func (l *Layout) FieldOffset(structName, field string) (uint32, vir.Type, error) {
	s, ok := l.structs[structName]
	if !ok {
		return 0, nil, fmt.Errorf("no struct %q in this module", structName)
	}
	sl, err := l.structOf(vir.StructType{Name: structName})
	if err != nil {
		return 0, nil, err
	}
	for i, f := range s.Fields {
		if f.Name == field {
			return sl.Offsets[i], f.Type, nil
		}
	}
	return 0, nil, fmt.Errorf("struct %s has no field %q", structName, field)
}

func (l *Layout) structOf(t vir.StructType) (*structLayout, error) {
	if t.Import != "" {
		// importer.Rewrite is required to have erased cross-module shapes
		// before Lower runs; a surviving one is an upstream bug.
		return nil, fmt.Errorf("unresolved imported struct %s.%s", t.Import, t.Name)
	}
	if sl, ok := l.cache[t.Name]; ok {
		return sl, nil
	}
	if l.busy[t.Name] {
		return nil, fmt.Errorf("struct %s contains itself by value", t.Name)
	}
	s, ok := l.structs[t.Name]
	if !ok {
		return nil, fmt.Errorf("no struct %q in this module", t.Name)
	}
	l.busy[t.Name] = true
	defer delete(l.busy, t.Name)

	sl := &structLayout{Align: 1, Offsets: make([]uint32, len(s.Fields))}
	var off uint32
	for i, f := range s.Fields {
		fa, err := l.Align(f.Type)
		if err != nil {
			return nil, err
		}
		fs, err := l.Size(f.Type)
		if err != nil {
			return nil, err
		}
		off = roundUp(off, fa)
		sl.Offsets[i] = off
		off += fs
		if fa > sl.Align {
			sl.Align = fa
		}
	}
	sl.Size = roundUp(off, sl.Align)
	l.cache[t.Name] = sl
	return sl, nil
}

func roundUp(v, align uint32) uint32 {
	if align <= 1 {
		return v
	}
	return (v + align - 1) &^ (align - 1)
}