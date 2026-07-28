// layout.go
package gvir

import "fmt"

// Layout rules (§4.4, §4.7, §6.3). Defined by the IR itself and identical
// on every backend — not inherited from a C ABI and, for msl, explicitly
// not Metal's struct rules. The conformance suite compares kernarg buffers
// byte for byte across all three backends (§13), so this file is the one
// place that computation may live.

// SizeOf returns t's size in bytes.
func (m *Module) SizeOf(t Type) (int, error) {
	switch x := t.(type) {
	case IntType:
		// i1 is a byte in memory. The spec fixes no width for it; a byte
		// is the only choice that keeps struct layout addressable and
		// matches every backend's bool storage.
		if x.Bits == 1 {
			return 1, nil
		}
		return x.Bits / 8, nil
	case FloatType:
		return x.Bits() / 8, nil
	case PtrType:
		if x.Space == SpaceNone {
			return 0, fmt.Errorf("layout: bare `.ptr` suffix is not a value type")
		}
		return 8, nil // 64-bit on every backend (§4.1)
	case VecType:
		if IsPredVec(x) {
			return 0, fmt.Errorf("layout: vec[i1,%d] has no memory representation (§4.5)", x.Len)
		}
		es, err := m.SizeOf(x.Elem)
		if err != nil {
			return 0, err
		}
		return nextPow2(x.Len) * es, nil // vec[T,3] is 4 elements wide
	case ArrayType:
		es, err := m.SizeOf(x.Elem)
		if err != nil {
			return 0, err
		}
		ea, err := m.AlignOf(x.Elem)
		if err != nil {
			return 0, err
		}
		// No inter-element padding: the element's own size is already a
		// multiple of its alignment for every legal element type.
		if es%ea != 0 {
			return 0, fmt.Errorf("layout: element %s is not a multiple of its own alignment", x.Elem)
		}
		return es * x.Len, nil
	case StructType:
		f, err := m.StructLayout(x.Name)
		if err != nil {
			return 0, err
		}
		return f.Size, nil
	}
	return 0, fmt.Errorf("layout: %s has no size", t)
}

// AlignOf returns t's natural alignment in bytes.
func (m *Module) AlignOf(t Type) (int, error) {
	switch x := t.(type) {
	case IntType, FloatType, PtrType:
		return m.SizeOf(x) // scalars are naturally aligned to their size
	case VecType:
		return m.SizeOf(x) // vec alignment == its padded size (§4.4)
	case ArrayType:
		return m.AlignOf(x.Elem)
	case StructType:
		f, err := m.StructLayout(x.Name)
		if err != nil {
			return 0, err
		}
		return f.Align, nil
	}
	return 0, fmt.Errorf("layout: %s has no alignment", t)
}

// FieldOffset is one laid-out struct field.
type FieldOffset struct {
	Name   string
	Type   Type
	Offset int
	Size   int
	Align  int
}

// StructInfo is a fully laid-out struct (§4.7): fields in declaration
// order, each naturally aligned, the whole padded to the largest member
// alignment.
type StructInfo struct {
	Name   string
	Fields []FieldOffset
	Size   int
	Align  int
}

// StructLayout computes the layout of the named struct.
func (m *Module) StructLayout(name string) (*StructInfo, error) {
	s := m.Struct(name)
	if s == nil {
		return nil, fmt.Errorf("layout: undeclared struct %q", name)
	}
	info := &StructInfo{Name: name, Align: 1}
	off := 0
	for _, f := range s.Fields {
		sz, err := m.SizeOf(f.Type)
		if err != nil {
			return nil, fmt.Errorf("layout: struct %s field %s: %w", name, f.Name, err)
		}
		al, err := m.AlignOf(f.Type)
		if err != nil {
			return nil, fmt.Errorf("layout: struct %s field %s: %w", name, f.Name, err)
		}
		off = roundUp(off, al)
		info.Fields = append(info.Fields, FieldOffset{
			Name: f.Name, Type: f.Type, Offset: off, Size: sz, Align: al,
		})
		off += sz
		if al > info.Align {
			info.Align = al
		}
	}
	info.Size = roundUp(off, info.Align)
	return info, nil
}

// --- kernarg (§6.3) --------------------------------------------------------

// KernargField is one slot of the packed kernarg buffer. Index is the §6.2
// parameter index, or -1 for a hidden trailer field.
type KernargField struct {
	Name   string
	Index  int
	Type   Type
	Offset int
	Size   int
	Align  int
	Hidden bool
}

// KernargLayout is the packed argument buffer, identical on all three
// backends. On msl it is realized as the single argument buffer at
// buffer(0), whose offsets and padding come from here rather than from
// Metal — which is why it requires Argument Buffers Tier 2.
type KernargLayout struct {
	Fields []KernargField
	Size   int
	Align  int
}

// KernargLayout computes k's argument buffer.
func (m *Module) KernargLayout(k *Kernel) (*KernargLayout, error) {
	l := &KernargLayout{Align: 8} // buffer align is max(8, largest member)
	off := 0
	for i, p := range k.Params {
		sz, err := m.SizeOf(p.Type)
		if err != nil {
			return nil, fmt.Errorf("kernarg: kernel %s param %s: %w", k.Name, p.Name, err)
		}
		al, err := m.AlignOf(p.Type)
		if err != nil {
			return nil, fmt.Errorf("kernarg: kernel %s param %s: %w", k.Name, p.Name, err)
		}
		off = roundUp(off, al)
		l.Fields = append(l.Fields, KernargField{
			Name: p.Name, Index: i, Type: p.Type, Offset: off, Size: sz, Align: al,
		})
		off += sz
		if al > l.Align {
			l.Align = al
		}
	}
	// Hidden trailer, on every backend, at the next natural offset after
	// the last explicit argument.
	if k.Dynamic != nil {
		off = roundUp(off, 4)
		l.Fields = append(l.Fields, KernargField{
			Name:   "dynamic_group_size",
			Index:  -1,
			Type:   I32,
			Offset: off,
			Size:   4,
			Align:  4,
			Hidden: true,
		})
		off += 4
	}
	l.Size = roundUp(off, l.Align)
	return l, nil
}

// --- group memory (§6.5) ---------------------------------------------------

// StaticGroupBytes totals a kernel's statically declared group memory,
// laying declarations out in order at their declared-or-natural alignment.
// This is the figure §6.5 checks against GroupMemoryLimit; a backend may
// pad further, never less.
func (m *Module) StaticGroupBytes(k *Kernel) (int, error) {
	off, maxAlign := 0, 1
	for _, g := range k.Groups {
		sz, err := m.SizeOf(g.Type)
		if err != nil {
			return 0, fmt.Errorf("group %s: %w", g.Name, err)
		}
		al := g.Align
		if al == 0 {
			if al, err = m.AlignOf(g.Type); err != nil {
				return 0, fmt.Errorf("group %s: %w", g.Name, err)
			}
		}
		off = roundUp(off, al)
		off += sz
		if al > maxAlign {
			maxAlign = al
		}
	}
	return roundUp(off, maxAlign), nil
}

// FitsEverywhere reports whether k's static group memory stays under the
// portable budget (§6.5).
func (m *Module) FitsEverywhere(k *Kernel) (bool, error) {
	n, err := m.StaticGroupBytes(k)
	if err != nil {
		return false, err
	}
	return n <= PortableGroupLimit, nil
}

func roundUp(n, align int) int {
	if align <= 1 {
		return n
	}
	return (n + align - 1) / align * align
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// ValidAlign reports whether n is a legal `align N` clause: a power of two
// in [1, 1024] (§2).
func ValidAlign(n int) bool {
	return n >= 1 && n <= 1024 && n&(n-1) == 0
}