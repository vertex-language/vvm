// module.go
package gvir

// Module is the IR-level representation of one .gvir compilation unit
// (§2). Field order mirrors the mandatory section order, which is what
// makes one-pass verification possible.
//
// Device-only (§1): there is no host entry point, no globals, no links,
// no externs, no imports. Everything a vir Module carries for the host
// side is absent here by construction, not by convention.
type Module struct {
	Version      Version
	Name         string
	Targets      []Backend
	FloatProfile FloatProfile
	Structs      []*Struct
	Constants    []*Constant
	Funcs        []*Func
	Kernels      []*Kernel
}

type Version struct{ Major, Minor int }

// SpecVersion is the language version this package implements.
var SpecVersion = Version{Major: 1, Minor: 0}

func (v Version) String() string { return itoa(v.Major) + "." + itoa(v.Minor) }

// FloatProfile is the module-wide float-profile declaration (§2, §11.6).
type FloatProfile string

const (
	// ProfileUnset means no float_profile line was declared. The
	// declaration is optional in the grammar; this package treats an
	// absent one as strict, the conservative reading — an unannotated
	// module never silently gains FMA contraction or approximate math.
	ProfileUnset   FloatProfile = ""
	ProfileStrict  FloatProfile = "strict"
	ProfileBounded FloatProfile = "bounded"
)

// Effective resolves ProfileUnset to its default.
func (p FloatProfile) Effective() FloatProfile {
	if p == ProfileUnset {
		return ProfileStrict
	}
	return p
}

// Struct is a memory-only aggregate declaration (§4.7). Layout is defined
// by this IR (layout.go), not inherited from any C ABI.
type Struct struct {
	Name   string
	Fields []Field
}

type Field struct {
	Name string
	Type Type
}

func (s *Struct) FieldByName(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func (s *Struct) FieldIndex(name string) int {
	for i, f := range s.Fields {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// Struct looks up a declared struct by name. Struct types are nominal and
// the module namespace is flat (§2), so the name alone resolves it.
func (m *Module) Struct(name string) *Struct {
	for _, s := range m.Structs {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// Constant is a module-scope immutable (§2 const-decl).
type Constant struct {
	Name string
	Type Type
	Init ConstInit
}

// ConstInit is the const-init grammar (§2). There is no address-of form:
// device-only modules have no globals whose address could be taken.
type ConstInit interface{ isInit() }

type InitLiteral struct{ Value Operand }
type InitZero struct{}
type InitAggregate struct{ Elems []ConstInit }

func (InitLiteral) isInit()   {}
func (InitZero) isInit()      {}
func (InitAggregate) isInit() {}

// Param is one declared parameter. Kernel parameters are identified
// portably by index, never by byte offset (§6.2).
type Param struct {
	Name string
	Type Type
}

// FuncAttr is a device-function attribute (§6.4).
type FuncAttr string

const (
	AttrInline   FuncAttr = "inline"
	AttrNoInline FuncAttr = "noinline"
	// AttrReadonly asserts the function writes through no pointer
	// reachable from its arguments. Violating it is UB (§12.10).
	AttrReadonly FuncAttr = "readonly"
)

// Func is a device-side helper (§6.4). Direct calls only; recursion,
// mutual recursion, indirect calls, and address-taking are all illegal,
// so there is no function-pointer or signature machinery here — the
// FunctionSignature table vir needs for indirect calls has no analogue.
type Func struct {
	Name   string
	Params []Param
	Ret    Type
	Attrs  []FuncAttr
	Entry  *Block
	Blocks []*Block
}

func (f *Func) HasAttr(a FuncAttr) bool {
	for _, x := range f.Attrs {
		if x == a {
			return true
		}
	}
	return false
}

// GroupShape is an exact group_size X,Y,Z attribute (§6.1).
type GroupShape struct{ X, Y, Z int }

func (g GroupShape) Threads() int { return g.X * g.Y * g.Z }

// DynamicGroup is the one launch-sized group allocation a kernel may
// declare (§6.1, §8.2). Its byte size is read back through the
// dynamic_group_size builtin (§9.1), which reads the hidden kernarg field
// of §6.3.
type DynamicGroup struct {
	Name  string
	Align int // 0 = unspecified
}

// GroupDecl is one statically sized, kernel-scoped group allocation
// (§8.2). Zero-initialization is not guaranteed.
type GroupDecl struct {
	Name  string
	Type  Type
	Align int // 0 = natural
}

// Kernel is a dispatchable entry point (§6.1). Attributes are separate
// typed fields rather than a slice because each is at-most-once and
// carries its own operands; a zero value means the attribute is absent.
type Kernel struct {
	Name   string
	Params []Param

	// GroupSize is the exact required group shape. Normative: launching
	// a contradicting shape is UB (§12.7).
	GroupSize *GroupShape
	// MaxGroupSize bounds X*Y*Z. Normative. 0 = absent.
	MaxGroupSize int
	// MinGroupsPerUnit is an occupancy hint. Advisory only — it never
	// affects semantics (§6.1). 0 = absent.
	MinGroupsPerUnit int
	// SubgroupSize requests a specific subgroup width. Normative and
	// gated: unavailable on every msl artifact (§4.3, §9.2). 0 = absent.
	SubgroupSize int
	// Dynamic is the at-most-one dynamic_group declaration.
	Dynamic *DynamicGroup

	Groups []*GroupDecl
	Entry  *Block
	Blocks []*Block
}

// HasShapeContract reports whether the kernel constrains its group shape
// at compile time. Without one, the host still supplies group dimensions
// at launch — it is the *compile-time* contract that is absent (§6.1).
func (k *Kernel) HasShapeContract() bool {
	return k.GroupSize != nil || k.MaxGroupSize > 0
}

func (m *Module) Kernel(name string) *Kernel {
	for _, k := range m.Kernels {
		if k.Name == name {
			return k
		}
	}
	return nil
}

func (m *Module) Func(name string) *Func {
	for _, f := range m.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Blocks and merge annotations (§7).
// ---------------------------------------------------------------------------

// Block is the entry block (Label "") or a labelled block (§7.1). Merge is
// non-nil exactly when the terminator has more than one distinct successor
// (§7.2); it sits on the line immediately after the label.
type Block struct {
	Label string
	Merge Merge
	Lines []*Instruction
	Term  Terminator
}

func (b *Block) IsEntry() bool { return b.Label == "" }

// Merge is a merge-decl (§7.2).
type Merge interface{ isMerge() }

// SelectionMerge is `merge L`: the selection headed here reconverges at L.
type SelectionMerge struct{ Label string }

// LoopMerge is `loop_merge Lexit, Lcontinue`: this block is a loop header.
type LoopMerge struct{ Exit, Continue string }

func (SelectionMerge) isMerge() {}
func (LoopMerge) isMerge()      {}

// Dim is a builtin's optional .x/.y/.z suffix (§9).
type Dim uint8

const (
	DimNone Dim = iota
	DimX
	DimY
	DimZ
)

func (d Dim) String() string {
	switch d {
	case DimX:
		return "x"
	case DimY:
		return "y"
	case DimZ:
		return "z"
	}
	return ""
}

// Instruction is one body-line (§2). At most one of Suffix, Dim, and Exec
// is set, and which one is fixed by the opcode:
//
//	Suffix — the `.<T>` type suffix on an ordinary op, or AnyPtr for the
//	         bare `.ptr` ident suffix (eq.ptr / index.ptr / field.ptr).
//	Dim    — the `.x`/`.y`/`.z` suffix on a §9 execution builtin.
//	Exec   — the execution scope on `barrier.<exec-scope>`; barrier's
//	         optional memory scope is Args[0], since the grammar puts it
//	         after the comma.
//
// Note for printers and parsers: a type suffix may itself contain commas
// (`add.vec[f32,4] a, b`), so splitting an inst line on commas requires
// tracking bracket depth (§2 lexical).
type Instruction struct {
	Result string
	Op     Opcode
	Suffix Type
	Dim    Dim
	Exec   ExecScope
	Args   []Operand
	Align  int // 0 = natural; power of two, 1..1024 (§2)
}

// ---------------------------------------------------------------------------
// Terminators (§2, §7). No trap and no tailcall: .gvir has no trap
// semantics at all (§1) and no indirect or tail calls (§6.4).
// ---------------------------------------------------------------------------

type Terminator interface{ isTerm() }

type Br struct{ Label string }
type BrIf struct {
	Cond       Operand
	Then, Else string
}
type SwitchCase struct {
	Value int64
	Label string
}
type Switch struct {
	Value   Operand
	Default string
	Cases   []SwitchCase
}

// Return carries no operand inside a kernel (§6.1).
type Return struct{ Value *Operand }

type Unreachable struct{}

func (Br) isTerm()          {}
func (BrIf) isTerm()        {}
func (Switch) isTerm()      {}
func (Return) isTerm()      {}
func (Unreachable) isTerm() {}

// Successors returns the distinct labels a terminator may transfer to, in
// first-mention order.
func Successors(t Terminator) []string {
	switch x := t.(type) {
	case Br:
		return []string{x.Label}
	case BrIf:
		if x.Then == x.Else {
			return []string{x.Then}
		}
		return []string{x.Then, x.Else}
	case Switch:
		out := []string{x.Default}
		seen := map[string]bool{x.Default: true}
		for _, c := range x.Cases {
			if !seen[c.Label] {
				seen[c.Label] = true
				out = append(out, c.Label)
			}
		}
		return out
	}
	return nil
}

// NeedsMerge reports whether a block ending in t must carry a merge-decl:
// exactly the "more than one distinct successor" test of §7.2. A br_if
// whose arms are the same label does not qualify.
func NeedsMerge(t Terminator) bool {
	switch t.(type) {
	case BrIf, Switch:
		return len(Successors(t)) > 1
	}
	return false
}

// AllBlocks returns entry followed by the labelled blocks.
func (k *Kernel) AllBlocks() []*Block { return allBlocks(k.Entry, k.Blocks) }
func (f *Func) AllBlocks() []*Block   { return allBlocks(f.Entry, f.Blocks) }

func allBlocks(entry *Block, rest []*Block) []*Block {
	out := make([]*Block, 0, len(rest)+1)
	if entry != nil {
		out = append(out, entry)
	}
	return append(out, rest...)
}