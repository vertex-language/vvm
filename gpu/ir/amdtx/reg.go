package amdtx

import "strconv"

// Reg is a virtual register handed out by a RegFile, optionally narrowed to
// a dword slice of its tuple. The lowering pipeline performs the real
// allocation; this package never does (P2). The zero Reg is invalid and
// prints as a recognizable placeholder rather than panicking.
type Reg struct {
	r      *regInfo
	lo, hi int
	sliced bool
}

type regInfo struct {
	class RegClass
	name  string // non-empty for explicitly named registers
	block *RegBlock
	idx   int // index within the block, 0-based and dense
	file  *RegFile
}

func (r Reg) Text() string {
	if r.r == nil {
		return "%<invalid>"
	}
	s := "%" + r.baseName()
	if !r.sliced {
		return s
	}
	if r.lo == r.hi {
		return s + "[" + strconv.Itoa(r.lo) + "]"
	}
	return s + "[" + strconv.Itoa(r.lo) + ":" + strconv.Itoa(r.hi) + "]"
}

func (r Reg) baseName() string {
	if r.r.name != "" {
		return r.r.name
	}
	return r.r.block.Prefix + strconv.Itoa(r.r.idx)
}

func (Reg) operand()     {}
func (Reg) addressable() {}

// Class returns the declared class of the underlying register.
func (r Reg) Class() RegClass {
	if r.r == nil {
		return RegClass{}
	}
	return r.r.class
}

// Kind returns the register file the operand occupies.
func (r Reg) Kind() RegKind { return r.Class().PhysKind() }

// Width returns the width of the operand: the slice width if sliced, the
// declared width otherwise. A .lanemask reports NoWidth, since its width
// comes from .wave.
func (r Reg) Width() Width {
	if r.r == nil {
		return NoWidth
	}
	if r.sliced {
		return Width((r.hi - r.lo + 1) * 32)
	}
	return r.r.class.Width
}

// Dword narrows r to a single tuple-relative dword: %sd0[1].
func (r Reg) Dword(i int) Reg { return r.Dwords(i, i) }

// Dwords narrows r to the inclusive tuple-relative dword range [lo, hi].
//
// The index domain is tuple-relative dwords, not absolute physical register
// numbers (§7.5, PTX/LLVM deviation). Slicing an already-sliced register
// composes, so r.Dwords(2,3).Dword(0) is r.Dword(2).
func (r Reg) Dwords(lo, hi int) Reg {
	base := 0
	if r.sliced {
		base = r.lo
	}
	return Reg{r: r.r, lo: base + lo, hi: base + hi, sliced: true}
}

// IsSlice reports whether r is a sub-register slice.
func (r Reg) IsSlice() bool { return r.sliced }

// SliceBounds returns the tuple-relative dword bounds of the slice.
func (r Reg) SliceBounds() (lo, hi int, ok bool) { return r.lo, r.hi, r.sliced }

// IsValid reports whether r came from a RegFile.
func (r Reg) IsValid() bool { return r.r != nil }

// SameReg reports whether a and b denote the same virtual register,
// ignoring slicing.
func SameReg(a, b Operand) bool {
	ra, ok1 := a.(Reg)
	rb, ok2 := b.(Reg)
	return ok1 && ok2 && ra.r != nil && ra.r == rb.r
}

// RegBlock is a parameterised declaration such as ".reg .vgpr.b32 %v<64>;".
// Numbering is 0-based and dense: %v0 through %v63 (§7.1).
type RegBlock struct {
	Class  RegClass
	Prefix string
	Count  int

	regs []Reg
}

// At returns the i-th register of the block.
func (b *RegBlock) At(i int) Reg {
	if i < 0 || i >= len(b.regs) {
		return Reg{}
	}
	return b.regs[i]
}

// All returns every register in the block, in order.
func (b *RegBlock) All() []Reg { return b.regs }

// Len returns the block size.
func (b *RegBlock) Len() int { return b.Count }

// RegDecl is one printed .reg declaration: either a parameterised block or
// a comma-separated name list.
type RegDecl struct {
	Class RegClass
	Block *RegBlock // nil for a name list
	Names []string  // empty for a block
}

// RegFile is a per-body allocator for virtual registers. Registers are
// body-scoped (§3.2), so every body owns exactly one.
type RegFile struct {
	decls  []RegDecl
	names  map[string]bool
	dups   []string
	blocks []*RegBlock
}

func newRegFile() *RegFile { return &RegFile{names: map[string]bool{}} }

// Block declares ".reg <class> %<prefix><N>;" and returns a handle.
func (f *RegFile) Block(c RegClass, prefix string, n int) *RegBlock {
	b := &RegBlock{Class: c, Prefix: prefix, Count: n}
	for i := 0; i < n; i++ {
		name := prefix + strconv.Itoa(i)
		if f.names[name] {
			f.dups = append(f.dups, name)
		}
		f.names[name] = true
		b.regs = append(b.regs, Reg{r: &regInfo{class: c, block: b, idx: i, file: f}})
	}
	f.blocks = append(f.blocks, b)
	f.decls = append(f.decls, RegDecl{Class: c, Block: b})
	return f.blocks[len(f.blocks)-1]
}

// New declares a single named register, as in ".reg .sgpr.b64 %kbase;".
func (f *RegFile) New(c RegClass, name string) Reg {
	return f.NewN(c, name)[0]
}

// NewN declares several named registers sharing one declaration, as in
// ".reg .sgpr.b64 %kbase, %kend;".
func (f *RegFile) NewN(c RegClass, names ...string) []Reg {
	out := make([]Reg, len(names))
	for i, n := range names {
		if f.names[n] {
			f.dups = append(f.dups, n)
		}
		f.names[n] = true
		out[i] = Reg{r: &regInfo{class: c, name: n, file: f}}
	}
	f.decls = append(f.decls, RegDecl{Class: c, Names: append([]string(nil), names...)})
	return out
}

// Decls returns the declarations in declaration order. The printer groups
// them by class; it does not reorder within a class.
func (f *RegFile) Decls() []RegDecl { return f.decls }

// ---- Special registers ----------------------------------------------------

// SReg is a special register, referenceable without declaration (§7.3).
type SReg uint8

const (
	NoSReg SReg = iota

	Exec
	VCC
	SCC
	M0
	FlatScratch
	KernargPtr
	DispatchPtr
	Null

	WgIdX
	WgIdY
	WgIdZ

	TidX
	TidY
	TidZ
)

type sregInfo struct {
	name  string
	kind  RegKind
	width Width // NoWidth means the width follows .wave
}

var sregTable = map[SReg]sregInfo{
	Exec:        {"%exec", SGPR, NoWidth},
	VCC:         {"%vcc", SGPR, NoWidth},
	SCC:         {"%scc", SGPR, B32},
	M0:          {"%m0", SGPR, B32},
	FlatScratch: {"%flat_scratch", SGPR, B64},
	KernargPtr:  {"%kernarg_ptr", SGPR, B64},
	DispatchPtr: {"%dispatch_ptr", SGPR, B64},
	Null:        {"%null", NoRegKind, NoWidth},

	WgIdX: {"%wgid.x", SGPR, B32},
	WgIdY: {"%wgid.y", SGPR, B32},
	WgIdZ: {"%wgid.z", SGPR, B32},

	// On targets with packed work-item IDs the three IDs arrive in one
	// VGPR; AMDTX presents them as three values and lowering unpacks.
	TidX: {"%tid.x", VGPR, B32},
	TidY: {"%tid.y", VGPR, B32},
	TidZ: {"%tid.z", VGPR, B32},
}

func (s SReg) Text() string  { return sregTable[s].name }
func (SReg) operand()        {}
func (SReg) addressable()    {}
func (s SReg) Kind() RegKind { return sregTable[s].kind }

// Width returns the register's width under wave width w.
func (s SReg) Width(w Wave) Width {
	if i := sregTable[s]; i.width != NoWidth {
		return i.width
	}
	if s == Exec || s == VCC {
		return w.MaskWidth()
	}
	return NoWidth
}

// IsValid reports whether s names a modelled special register.
func (s SReg) IsValid() bool { _, ok := sregTable[s]; return ok }