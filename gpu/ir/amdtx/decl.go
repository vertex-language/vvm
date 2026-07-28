package amdtx

import "strconv"

// Decl is a module-scope declaration.
type Decl interface{ decl() }

// Linkage is a symbol linkage directive. In AMDTX 1.0 it applies to .kernel
// definitions: .func symbols are always module-local, since external .func
// linkage waits on a call ABI (§3.2).
type Linkage uint8

const (
	Default Linkage = iota // externally dispatchable
	Visible
	Local
)

func (l Linkage) String() string {
	return [...]string{"", ".visible", ".local"}[l]
}

// Dim3 is a three-element directive operand. The zero value means the
// directive is omitted.
type Dim3 [3]int

// IsZero reports whether the directive should be omitted.
func (d Dim3) IsZero() bool { return d[0] == 0 && d[1] == 0 && d[2] == 0 }

// Product returns the workgroup size the dimensions describe.
func (d Dim3) Product() int { return d[0] * d[1] * d[2] }

func (d Dim3) String() string {
	return strconv.Itoa(d[0]) + ", " + strconv.Itoa(d[1]) + ", " + strconv.Itoa(d[2])
}

// ---- Module-scope objects -------------------------------------------------

// Object is a module-scope .global or .shared variable. Objects are always
// bracketed; Len 0 means the length was omitted and is taken from Init, or,
// for .shared, from .dynamic_group_segment.
type Object struct {
	Linkage Linkage
	Space   Space
	Align   int
	Width   Width
	Name    string
	Len     int
	Init    []Operand
}

func (o *Object) Text() string { return o.Name }
func (*Object) operand()       {}
func (*Object) addressable()   {}
func (*Object) decl()          {}

// Elems returns the array length: the declared length, or the initializer
// length when the declared length is omitted.
func (o *Object) Elems() int {
	if o.Len != 0 {
		return o.Len
	}
	return len(o.Init)
}

// ---- Parameters -----------------------------------------------------------

// Param is a kernel parameter. Kernargs are not operands: a kernel reads
// them through %kernarg_ptr at the offset the layout assigns, so the
// parameter list is a layout description, not a register binding.
type Param struct {
	Name   string
	Kind   ParamKind
	Space  Space
	Access Access
	Width  Width
	Align  int
}

// NaturalAlign returns the alignment implied by the parameter's width.
func (p *Param) NaturalAlign() int {
	b := p.Width.Bytes()
	if b > 16 {
		return 16
	}
	if b < 4 {
		return 4
	}
	return b
}

// EffectiveAlign returns the stricter of the natural and declared
// alignments.
func (p *Param) EffectiveAlign() int {
	if p.Align > p.NaturalAlign() {
		return p.Align
	}
	return p.NaturalAlign()
}

// FParam is a .func formal parameter: a register class and a name.
type FParam struct {
	Class RegClass
	Name  string
}

// ---- Launch directives ----------------------------------------------------

// Launch is the set of kernel launch directives. Zero-valued fields are
// omitted by the printer and map onto the assembler's .amdhsa_* directives
// at lowering.
type Launch struct {
	KernargSize          int
	KernargAlign         int
	GroupSegmentSize     int
	DynamicGroupSegment  bool
	PrivateSegmentSize   int
	ReqdWorkgroupSize    Dim3
	MaxFlatWorkgroupSize int
	WavesPerEU           [2]int
	KernargPreload       int
}

// ---- Kernels and functions ------------------------------------------------

// Kernel is an AQL dispatch entry point. A kernel is not callable (V3).
type Kernel struct {
	Name    string
	Linkage Linkage
	Params  []*Param
	Launch  Launch
	Body    *Body
}

// NewKernel returns a kernel with an empty body ready for emission.
func NewKernel(name string) *Kernel {
	return &Kernel{Name: name, Body: NewBody()}
}

func (k *Kernel) Text() string { return k.Name }
func (*Kernel) operand()       {}
func (*Kernel) decl()          {}

// Param appends a scalar parameter and returns a handle to it.
func (k *Kernel) Param(name string, w Width) *Param {
	p := &Param{Name: name, Width: w}
	k.Params = append(k.Params, p)
	return p
}

// ParamPtr appends a 64-bit pointer parameter carrying space and access
// qualifiers.
func (k *Kernel) ParamPtr(name string, sp Space, acc Access) *Param {
	p := &Param{Name: name, Space: sp, Access: acc, Width: B64}
	k.Params = append(k.Params, p)
	return p
}

// ParamSlot is one entry in the computed kernarg layout.
type ParamSlot struct {
	Param  *Param
	Offset int
	Size   int
	Align  int
}

// KernargLayout assigns offsets in declaration order, each at the next
// offset satisfying its alignment, with padding inserted as needed (§11.2).
// Hidden implicit arguments follow every explicit argument and are the
// runtime's contract, so they never appear here (§11.3).
func (k *Kernel) KernargLayout() []ParamSlot {
	var out []ParamSlot
	off := 0
	for _, p := range k.Params {
		a := p.EffectiveAlign()
		if a > 0 && off%a != 0 {
			off += a - off%a
		}
		size := p.Width.Bytes()
		out = append(out, ParamSlot{Param: p, Offset: off, Size: size, Align: a})
		off += size
	}
	return out
}

// KernargOffset returns the byte offset of p within the kernarg segment.
func (k *Kernel) KernargOffset(p *Param) (int, bool) {
	for _, s := range k.KernargLayout() {
		if s.Param == p {
			return s.Offset, true
		}
	}
	return 0, false
}

// Func is a device function. Function bodies are inlined at every call site
// by the lowering pipeline; AMDTX 1.0 defines no calling convention, no
// stack frame and no call ABI (§3.2).
type Func struct {
	Name   string
	Params []FParam
	Body   *Body
}

// NewFunc returns a device function with an empty body.
func NewFunc(name string) *Func {
	return &Func{Body: NewBody(), Name: name}
}

// Param appends a formal parameter and returns the register that names it
// inside the body.
func (f *Func) Param(c RegClass, name string) Reg {
	f.Params = append(f.Params, FParam{Class: c, Name: name})
	return f.Body.Regs.New(c, name)
}

func (f *Func) Text() string { return f.Name }
func (*Func) operand()       {}
func (*Func) decl()          {}

// File is an entry in the module's .file table. Indices are 1-based and
// every .file precedes every .loc that references it (V2).
type File struct {
	Index int
	Name  string
}

func (*File) decl() {}