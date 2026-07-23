// layout.go
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Layout answers size/alignment/field-offset questions under AAPCS64.
//
// Against lower/x86's Intel386 psABI there is no 4-byte alignment cap: an
// i64/f64 is 8-byte aligned, a pointer is 8 bytes and 8-byte aligned, and a
// struct's alignment is its largest member's. Against lower/arm the only
// change is pointer width. Computing any of this the 32-bit way would not be
// binary-compatible with anything else on the platform.
type Layout struct {
	structs map[string]*vir.Struct
	cache   map[string]*structLayout
	busy    map[string]bool // by-value cycle guard, sharing the cache's keyspace
}

type structLayout struct {
	size    uint32
	align   uint32
	offsets []uint32
}

func newLayout(m *vir.Module) *Layout {
	l := &Layout{
		structs: make(map[string]*vir.Struct, len(m.Structs)),
		cache:   map[string]*structLayout{},
		busy:    map[string]bool{},
	}
	for _, s := range m.Structs {
		l.structs[s.Name] = s
	}
	return l
}

// MaxVectorAlign caps a vector's alignment. AAPCS64 aligns a short vector to
// its size; 16 is the largest alignment the base standard requires of any
// fundamental type.
const MaxVectorAlign = 16

func (l *Layout) Size(t vir.Type) (uint32, error) {
	switch x := t.(type) {
	case vir.IntType:
		return uint32((x.Bits + 7) / 8), nil
	case vir.FloatType:
		return uint32(x.Bits / 8), nil
	case vir.PtrType:
		return 8, nil
	case vir.ValistType:
		// Target-defined (§3). This backend's cursor is one pointer; see
		// isel_va.go for why that is affordable here.
		return ValistBytes, nil
	case vir.VoidType:
		return 0, nil
	case vir.VecType:
		e, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		return e * uint32(x.Len), nil
	case vir.ArrayType:
		e, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		a, err := l.Align(x.Elem)
		if err != nil {
			return 0, err
		}
		return roundUp(e, a) * uint32(x.Len), nil
	case vir.StructType:
		sl, err := l.Struct(x)
		if err != nil {
			return 0, err
		}
		return sl.size, nil
	}
	return 0, fmt.Errorf("no size for type %s", t)
}

func (l *Layout) Align(t vir.Type) (uint32, error) {
	switch x := t.(type) {
	case vir.IntType, vir.FloatType, vir.PtrType, vir.ValistType:
		n, err := l.Size(t)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 1, nil
		}
		return n, nil
	case vir.VoidType:
		return 1, nil
	case vir.VecType:
		n, err := l.Size(t)
		if err != nil {
			return 0, err
		}
		if n > MaxVectorAlign {
			return MaxVectorAlign, nil
		}
		return n, nil
	case vir.ArrayType:
		return l.Align(x.Elem)
	case vir.StructType:
		sl, err := l.Struct(x)
		if err != nil {
			return 0, err
		}
		return sl.align, nil
	}
	return 0, fmt.Errorf("no alignment for type %s", t)
}

// Struct memoizes a struct's layout by name. The cache doubles as the
// by-value cycle guard, so a struct that transitively contains itself is
// reported as an error rather than recursing to a stack overflow.
func (l *Layout) Struct(t vir.StructType) (*structLayout, error) {
	if t.Import != "" {
		return nil, fmt.Errorf("struct %s.%s still carries an import: importer.Rewrite has not run", t.Import, t.Name)
	}
	if sl, ok := l.cache[t.Name]; ok {
		return sl, nil
	}
	if l.busy[t.Name] {
		return nil, fmt.Errorf("struct %s contains itself by value", t.Name)
	}
	s, ok := l.structs[t.Name]
	if !ok {
		return nil, fmt.Errorf("undeclared struct %s", t.Name)
	}
	l.busy[t.Name] = true
	defer delete(l.busy, t.Name)

	sl := &structLayout{align: 1, offsets: make([]uint32, len(s.Fields))}
	var off uint32
	for i, f := range s.Fields {
		fsz, err := l.Size(f.Type)
		if err != nil {
			return nil, fmt.Errorf("struct %s field %s: %w", t.Name, f.Name, err)
		}
		fal, err := l.Align(f.Type)
		if err != nil {
			return nil, fmt.Errorf("struct %s field %s: %w", t.Name, f.Name, err)
		}
		off = roundUp(off, fal)
		sl.offsets[i] = off
		off += fsz
		if fal > sl.align {
			sl.align = fal
		}
	}
	sl.size = roundUp(off, sl.align)
	l.cache[t.Name] = sl
	return sl, nil
}

// FieldOffset resolves a field.ptr to a byte offset and the field's type.
func (l *Layout) FieldOffset(structName, field string) (uint32, vir.Type, error) {
	sl, err := l.Struct(vir.StructType{Name: structName})
	if err != nil {
		return 0, nil, err
	}
	s := l.structs[structName]
	for i, f := range s.Fields {
		if f.Name == field {
			return sl.offsets[i], f.Type, nil
		}
	}
	return 0, nil, fmt.Errorf("struct %s has no field %s", structName, field)
}

func roundUp(v, a uint32) uint32 {
	if a <= 1 {
		return v
	}
	return (v + a - 1) &^ (a - 1)
}