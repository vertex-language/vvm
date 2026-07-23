// layout.go
package x86_64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Layout sizes and aligns types under the x86-64 System V psABI. Unlike the
// IA-32 backend there is NO 4-byte alignment cap: i64/f64/ptr are 8-byte
// aligned, and a struct's alignment is its largest field's.
type Layout struct {
	ix    *index
	cache map[string]structInfo // memoized per struct name; doubles as cycle guard
	busy  map[string]bool
}

type structInfo struct {
	size    int64
	align   int64
	offsets []int64 // one per field, in declaration order
}

func newLayout(ix *index) *Layout {
	return &Layout{ix: ix, cache: map[string]structInfo{}, busy: map[string]bool{}}
}

func (l *Layout) Size(t vir.Type) (int64, error) {
	switch x := t.(type) {
	case vir.IntType:
		return int64((x.Bits + 7) / 8), nil
	case vir.FloatType:
		return int64(x.Bits / 8), nil
	case vir.PtrType:
		return 8, nil
	case vir.VecType:
		es, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * int64(x.Len), nil
	case vir.ArrayType:
		es, err := l.Size(x.Elem)
		if err != nil {
			return 0, err
		}
		return es * int64(x.Len), nil
	case vir.StructType:
		si, err := l.structInfo(x.Name)
		if err != nil {
			return 0, err
		}
		return si.size, nil
	}
	return 0, fmt.Errorf("cannot size type %s", t)
}

func (l *Layout) Align(t vir.Type) (int64, error) {
	switch x := t.(type) {
	case vir.IntType:
		return align8(int64((x.Bits + 7) / 8)), nil
	case vir.FloatType:
		return int64(x.Bits / 8), nil
	case vir.PtrType:
		return 8, nil
	case vir.VecType:
		// Natural vector alignment is the whole width up to 16/32; keep it
		// simple and safe by aligning to size, capped at 16.
		s, err := l.Size(x)
		if err != nil {
			return 0, err
		}
		if s > 16 {
			return 16, nil
		}
		return s, nil
	case vir.ArrayType:
		return l.Align(x.Elem)
	case vir.StructType:
		si, err := l.structInfo(x.Name)
		if err != nil {
			return 0, err
		}
		return si.align, nil
	}
	return 0, fmt.Errorf("cannot align type %s", t)
}

// FieldOffset returns the byte offset of field within struct structName.
func (l *Layout) FieldOffset(structName, field string) (int64, error) {
	s, ok := l.ix.structs[structName]
	if !ok {
		return 0, fmt.Errorf("unknown struct %s", structName)
	}
	si, err := l.structInfo(structName)
	if err != nil {
		return 0, err
	}
	for i, f := range s.Fields {
		if f.Name == field {
			return si.offsets[i], nil
		}
	}
	return 0, fmt.Errorf("struct %s has no field %s", structName, field)
}

func (l *Layout) structInfo(name string) (structInfo, error) {
	if si, ok := l.cache[name]; ok {
		return si, nil
	}
	if l.busy[name] {
		return structInfo{}, fmt.Errorf("struct %s contains itself by value", name)
	}
	s, ok := l.ix.structs[name]
	if !ok {
		return structInfo{}, fmt.Errorf("unknown struct %s", name)
	}
	l.busy[name] = true
	defer delete(l.busy, name)

	var off, maxAlign int64 = 0, 1
	offsets := make([]int64, len(s.Fields))
	for i, f := range s.Fields {
		fa, err := l.Align(f.Type)
		if err != nil {
			return structInfo{}, err
		}
		fs, err := l.Size(f.Type)
		if err != nil {
			return structInfo{}, err
		}
		off = roundUp(off, fa)
		offsets[i] = off
		off += fs
		if fa > maxAlign {
			maxAlign = fa
		}
	}
	size := roundUp(off, maxAlign) // trailing pad to alignment
	si := structInfo{size: size, align: maxAlign, offsets: offsets}
	l.cache[name] = si
	return si, nil
}

func roundUp(v, a int64) int64 {
	if a <= 1 {
		return v
	}
	return (v + a - 1) / a * a
}

// align8 clamps a scalar's alignment to a power of two ≤ 8. Sub-8-byte
// integers align to their next power-of-two size (i24 → 4, etc.).
func align8(size int64) int64 {
	switch {
	case size >= 8:
		return 8
	case size >= 4:
		return 4
	case size >= 2:
		return 2
	default:
		return 1
	}
}