// layout.go
package gvir

import "fmt"

// Memory and kernarg layout (§4.4, §4.7, §6.3). Layout is defined by the
// specification, not inherited from a C ABI, and the kernarg buffer must be
// byte-identical across all three backends (§13 "Layout").
//
// Exported so the launcher generator, the differential test suite and
// ir/verify all resolve the same offsets without re-implementing the
// derivation a second time.

// PointerSize is 8 on every backend (§4.1).
const PointerSize = 8

// FieldOffset is one member's placement inside a struct.
type FieldOffset struct {
	Name   string
	Type   Type
	Offset int
	Size   int
	Align  int
}

// StructLayoutInfo is a struct's complete §4.7 layout: fields in
// declaration order, naturally aligned, padded to the largest member
// alignment.
type StructLayoutInfo struct {
	Fields []FieldOffset
	Size   int
	Align  int
}

// SizeOf reports the byte size t occupies in memory.
func (m *Module) SizeOf(t Type) (int, error) {
	size, _, err := m.sizeAlign(t, map[string]bool{})
	return size, err
}

// AlignOf reports t's natural alignment.
func (m *Module) AlignOf(t Type) (int, error) {
	_, align, err := m.sizeAlign(t, map[string]bool{})
	return align, err
}

// StructLayout computes s's field offsets, total size and alignment.
func (m *Module) StructLayout(s *Struct) (StructLayoutInfo, error) {
	return m.structLayout(s, map[string]bool{})
}

func (m *Module) structLayout(s *Struct, seen map[string]bool) (StructLayoutInfo, error) {
	if seen[s.Name] {
		return StructLayoutInfo{}, fmt.Errorf("struct %s is recursive — no forward references exist in .gvir (§2)", s.Name)
	}
	seen[s.Name] = true
	defer delete(seen, s.Name)

	out := StructLayoutInfo{Align: 1}
	offset := 0
	for _, f := range s.Fields {
		size, align, err := m.sizeAlign(f.Type, seen)
		if err != nil {
			return StructLayoutInfo{}, fmt.Errorf("struct %s field %s: %w", s.Name, f.Name, err)
		}
		offset = alignUp(offset, align)
		out.Fields = append(out.Fields, FieldOffset{
			Name: f.Name, Type: f.Type, Offset: offset, Size: size, Align: align,
		})
		offset += size
		if align > out.Align {
			out.Align = align
		}
	}
	out.Size = alignUp(offset, out.Align)
	return out, nil
}

func (m *Module) sizeAlign(t Type, seen map[string]bool) (int, int, error) {
	switch x := t.(type) {
	case IntType:
		if !IsSInt(x) {
			return 0, 0, fmt.Errorf("i1 has no defined size or layout (§4.1)")
		}
		n := x.Bits / 8
		return n, n, nil

	case FloatType:
		n := x.Bits / 8
		if n == 0 {
			return 0, 0, fmt.Errorf("%s is not a float type (§4.1)", x)
		}
		return n, n, nil

	case PtrType:
		if x.Space == "" {
			return 0, 0, fmt.Errorf("the bare `ptr` suffix word is not a value type and has no layout")
		}
		return PointerSize, PointerSize, nil

	case VecType:
		if IsBool(x.Elem) {
			return 0, 0, fmt.Errorf("vec[i1,N] is value-only and has no layout (§4.5)")
		}
		if !IsVecElemType(x.Elem) {
			return 0, 0, fmt.Errorf("vec element %s is not a non-i1 scalar (§4.4)", x.Elem)
		}
		if x.Len < 2 || x.Len > 4 {
			return 0, 0, fmt.Errorf("vec width %d is not 2, 3 or 4 (§4.4)", x.Len)
		}
		elemSize, _, err := m.sizeAlign(x.Elem, seen)
		if err != nil {
			return 0, 0, err
		}
		// Width 3 rounds up to 4: element 3 is padding (§4.4).
		n := nextPow2(x.Len) * elemSize
		return n, n, nil

	case ArrayType:
		elemSize, elemAlign, err := m.sizeAlign(x.Elem, seen)
		if err != nil {
			return 0, 0, err
		}
		if x.Len < 0 {
			return 0, 0, fmt.Errorf("array length %d is negative", x.Len)
		}
		// No inter-element padding (§4.7).
		return elemSize * x.Len, elemAlign, nil

	case StructType:
		s := m.StructByName(x.Name)
		if s == nil {
			return 0, 0, fmt.Errorf("undeclared struct %s", x.Name)
		}
		l, err := m.structLayout(s, seen)
		if err != nil {
			return 0, 0, err
		}
		return l.Size, l.Align, nil

	case SubmaskType:
		return 0, 0, fmt.Errorf("submask is opaque: its width is the runtime subgroup width (§4.6)")

	case VoidType:
		return 0, 0, fmt.Errorf("void has no layout")
	}
	return 0, 0, fmt.Errorf("type %s has no memory layout", t)
}

// ---------------------------------------------------------------------------
// Kernarg layout (§6.3)
// ---------------------------------------------------------------------------

// KernargParam is one argument's placement in the packed kernarg buffer.
// Index — not Offset — is the portable identity of a parameter (§6.2).
type KernargParam struct {
	Index  int
	Name   string
	Type   Type
	Offset int
	Size   int
	Align  int
}

// KernargLayout is a kernel's complete argument buffer layout. There is no
// hidden trailer: every backend carries the dynamic group size natively, so
// the buffer holds explicit arguments and nothing else and is byte-identical
// across all three backends (§6.3).
type KernargLayout struct {
	Params []KernargParam
	Size   int
	Align  int
}

// KernargLayout computes k's argument buffer layout: arguments in
// declaration order, each at the next offset satisfying its natural
// alignment; buffer aligned to max(8, largest member alignment); trailing
// padding rounding the size up to that alignment.
func (m *Module) KernargLayout(k *Kernel) (KernargLayout, error) {
	out := KernargLayout{Align: 8}
	offset := 0
	for i, p := range k.Params {
		if !IsKernelParamType(p.Type) {
			return KernargLayout{}, fmt.Errorf("kernel %s param %d (%s): %s is not a permitted kernel parameter type (§6.2)", k.Name, i, p.Name, p.Type)
		}
		size, align, err := m.sizeAlign(p.Type, map[string]bool{})
		if err != nil {
			return KernargLayout{}, fmt.Errorf("kernel %s param %d (%s): %w", k.Name, i, p.Name, err)
		}
		offset = alignUp(offset, align)
		out.Params = append(out.Params, KernargParam{
			Index: i, Name: p.Name, Type: p.Type,
			Offset: offset, Size: size, Align: align,
		})
		offset += size
		if align > out.Align {
			out.Align = align
		}
	}
	out.Size = alignUp(offset, out.Align)
	return out, nil
}

// StaticGroupBytes reports the total statically declared `group` footprint
// of a kernel, laid out in declaration order with each declaration at its
// declared or natural alignment. This is the figure §6.5 checks against the
// per-backend budget; the `dynamic_group` allocation is host-provisioned and
// deliberately not counted.
func (m *Module) StaticGroupBytes(k *Kernel) (int, error) {
	offset, maxAlign := 0, 1
	for _, g := range k.Groups {
		size, align, err := m.sizeAlign(g.Type, map[string]bool{})
		if err != nil {
			return 0, fmt.Errorf("kernel %s group %s: %w", k.Name, g.Name, err)
		}
		if g.Align > 0 {
			align = g.Align
		}
		offset = alignUp(offset, align)
		offset += size
		if align > maxAlign {
			maxAlign = align
		}
	}
	return alignUp(offset, maxAlign), nil
}

func alignUp(v, a int) int {
	if a <= 1 {
		return v
	}
	if r := v % a; r != 0 {
		return v + a - r
	}
	return v
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