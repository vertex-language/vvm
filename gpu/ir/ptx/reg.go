package ptx

// Reg is a virtual register handed out by a RegFile. ptxas performs the real
// allocation; this package never does. The zero Reg is invalid and prints as
// a recognizable placeholder rather than panicking.
type Reg struct{ r *regInfo }

type regInfo struct {
	typ    Type
	class  *RegClass
	n      int    // 1-based index within the class; 0 for named registers
	name   string // non-empty for explicitly named registers
}

func (r Reg) Text() string {
	if r.r == nil {
		return "%<invalid>"
	}
	if r.r.name != "" {
		return "%" + r.r.name
	}
	return "%" + r.r.class.Prefix + itoa(r.r.n)
}
func (Reg) operand()     {}
func (Reg) addressable() {}

// Type returns the register's declared type.
func (r Reg) Type() Type {
	if r.r == nil {
		return NoType
	}
	return r.r.typ
}

// IsValid reports whether r came from a RegFile.
func (r Reg) IsValid() bool { return r.r != nil }

// SameReg reports whether a and b denote the same virtual register.
func SameReg(a, b Operand) bool {
	ra, ok1 := a.(Reg)
	rb, ok2 := b.(Reg)
	return ok1 && ok2 && ra.r != nil && ra.r == rb.r
}

// RegClass is one register group, printed as a single ranged .reg
// declaration such as ".reg .f32 %f<9>;".
type RegClass struct {
	Type   Type
	Prefix string
	Count  int
}

// NamedReg is an explicitly named register declaration.
type NamedReg struct {
	Type Type
	Name string
}

// regPrefix follows the conventional ptxas naming so output is comparable
// with nvcc's: %p for predicates, %rs for 16-bit, %r for 32-bit integers,
// %rd for 64-bit integers, %rq for 128-bit, %h for 16-bit float, %f for
// f32, %fd for f64.
func regPrefix(t Type) string {
	switch t {
	case Pred:
		return "p"
	case F16, BF16:
		return "h"
	case F32:
		return "f"
	case F64:
		return "fd"
	case B128:
		return "rq"
	}
	switch t.Bits() {
	case 8, 16:
		return "rs"
	case 32:
		return "r"
	case 64:
		return "rd"
	}
	return "x"
}

// RegFile is a per-body naming allocator for virtual registers, grouped by
// type. The printer collapses each group into one ranged .reg declaration.
type RegFile struct {
	classes []*RegClass
	byType  map[Type]*RegClass
	named   []NamedReg
	names   map[string]bool
	dups    []string
}

func newRegFile() *RegFile {
	return &RegFile{byType: map[Type]*RegClass{}, names: map[string]bool{}}
}

// New allocates the next virtual register of type t.
func (f *RegFile) New(t Type) Reg {
	c := f.byType[t]
	if c == nil {
		c = &RegClass{Type: t, Prefix: regPrefix(t)}
		f.byType[t] = c
		f.classes = append(f.classes, c)
	}
	c.Count++
	return Reg{&regInfo{typ: t, class: c, n: c.Count}}
}

// NewN allocates n consecutive registers of type t.
func (f *RegFile) NewN(t Type, n int) []Reg {
	out := make([]Reg, n)
	for i := range out {
		out[i] = f.New(t)
	}
	return out
}

// Named declares a register with an explicit name, as in ".reg .u32 %idx;".
// Duplicate names and collisions with generated names are recorded and
// reported by Verify.
func (f *RegFile) Named(t Type, name string) Reg {
	if f.names[name] {
		f.dups = append(f.dups, name)
	}
	f.names[name] = true
	f.named = append(f.named, NamedReg{Type: t, Name: name})
	return Reg{&regInfo{typ: t, name: name}}
}

// Classes returns the register groups in declaration order.
func (f *RegFile) Classes() []*RegClass { return f.classes }

// NamedRegs returns the explicitly named registers in declaration order.
func (f *RegFile) NamedRegs() []NamedReg { return f.named }