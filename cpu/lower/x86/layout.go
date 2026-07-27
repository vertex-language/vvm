// layout.go
package x86

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Layout answers size/alignment/field-offset questions about vir types
// under the Intel386 C ABI (language spec §6.1).
//
// The defining i386 rule, and the one that most often surprises people
// arriving from x86-64: scalar alignment is capped at four bytes. An i64
// is eight bytes with four-byte alignment; an f64 likewise. That is not a
// simplification this backend made — it is what the psABI specifies, and
// struct layouts computed with eight-byte alignment would not be binary
// compatible with anything else on the platform.
type Layout struct {
	m       *vir.Module
	structs map[string]*vir.Struct

	// cache memoizes StructLayout. Beyond avoiding recomputation on every
	// field.ptr, it doubles as the cycle guard: computing a layout marks
	// it in progress, so a struct that (transitively) contains itself by
	// value is reported as an error instead of recursing until the
	// goroutine stack dies. ir/verify should reject such a module first,
	// but "should have been caught upstream" is not a reason to turn a
	// diagnosable input into a crash.
	cache      map[string]*structLayout
	inProgress map[string]bool
}

type structLayout struct {
	size  int
	align int
	offs  map[string]int
}

func newLayout(m *vir.Module) *Layout {
	l := &Layout{
		m:          m,
		structs:    map[string]*vir.Struct{},
		cache:      map[string]*structLayout{},
		inProgress: map[string]bool{},
	}
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
		switch x.Bits {
		case 16, 32, 64:
			return x.Bits / 8, nil
		}
		return 0, fmt.Errorf("layout: unsupported float width f%d", x.Bits)
	case vir.PtrType:
		return 4, nil
	case vir.VecType:
		return l.elemTimesLen(x.Elem, x.Len, "vector")
	case vir.ArrayType:
		return l.elemTimesLen(x.Elem, x.Len, "array")
	case vir.StructType:
		sl, err := l.structLayout(x.Name)
		if err != nil {
			return 0, err
		}
		return sl.size, nil
	}
	return 0, fmt.Errorf("layout: %s has no size", t)
}

func (l *Layout) elemTimesLen(elem vir.Type, n int, what string) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("layout: %s length %d is negative", what, n)
	}
	es, err := l.Size(elem)
	if err != nil {
		return 0, err
	}
	return es * n, nil
}

func (l *Layout) AlignOf(t vir.Type) (int, error) {
	switch x := t.(type) {
	case vir.IntType, vir.FloatType, vir.PtrType:
		sz, err := l.Size(t)
		if err != nil {
			return 0, err
		}
		// The i386 four-byte cap: i64/f64 align to 4, not 8.
		if sz > 4 {
			return 4, nil
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
		if sz == 0 {
			return 1, nil
		}
		return sz, nil
	case vir.ArrayType:
		return l.AlignOf(x.Elem)
	case vir.StructType:
		sl, err := l.structLayout(x.Name)
		if err != nil {
			return 0, err
		}
		return sl.align, nil
	}
	return 0, fmt.Errorf("layout: %s has no alignment", t)
}

// StructLayout returns a struct's total size, alignment, and field
// offsets. The returned map is the cached one and must not be mutated by
// callers.
func (l *Layout) StructLayout(name string) (int, int, map[string]int, error) {
	sl, err := l.structLayout(name)
	if err != nil {
		return 0, 0, nil, err
	}
	return sl.size, sl.align, sl.offs, nil
}

func (l *Layout) structLayout(name string) (*structLayout, error) {
	if sl, ok := l.cache[name]; ok {
		return sl, nil
	}
	s, ok := l.structs[name]
	if !ok {
		return nil, fmt.Errorf("layout: struct %q not declared", name)
	}
	if l.inProgress[name] {
		return nil, fmt.Errorf("layout: struct %q contains itself by value", name)
	}
	l.inProgress[name] = true
	defer delete(l.inProgress, name)

	off, align := 0, 1
	offs := make(map[string]int, len(s.Fields))
	for _, f := range s.Fields {
		fa, err := l.AlignOf(f.Type)
		if err != nil {
			return nil, fmt.Errorf("layout: struct %s field %s: %w", name, f.Name, err)
		}
		fs, err := l.Size(f.Type)
		if err != nil {
			return nil, fmt.Errorf("layout: struct %s field %s: %w", name, f.Name, err)
		}
		if fa < 1 {
			fa = 1
		}
		off = roundUp(off, fa)
		offs[f.Name] = off
		off += fs
		if fa > align {
			align = fa
		}
	}
	sl := &structLayout{size: roundUp(off, align), align: align, offs: offs}
	l.cache[name] = sl
	return sl, nil
}

func (l *Layout) FieldOffset(structName, field string) (int, error) {
	sl, err := l.structLayout(structName)
	if err != nil {
		return 0, err
	}
	o, ok := sl.offs[field]
	if !ok {
		return 0, fmt.Errorf("layout: struct %s has no field %q", structName, field)
	}
	return o, nil
}

// ByValSize is the stack size a byval[name] argument occupies before
// argument-area rounding. It exists so callconv.go and frame.go can be
// handed a plain func(string) (int, error) rather than the whole Layout.
func (l *Layout) ByValSize(name string) (int, error) {
	sl, err := l.structLayout(name)
	if err != nil {
		return 0, err
	}
	return sl.size, nil
}

func (l *Layout) Struct(name string) (*vir.Struct, bool) {
	s, ok := l.structs[name]
	return s, ok
}