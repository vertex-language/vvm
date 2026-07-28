// module.go
package gvir

import "fmt"

// Version is the `gvir MAJOR.MINOR` version declaration (§2).
type Version struct{ Major, Minor int }

// LanguageVersion is the specification version this package implements.
var LanguageVersion = Version{Major: 1, Minor: 0}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Module is the single IR-level representation of one .gvir translation
// unit. Field order mirrors the mandatory section order (§2):
//
//	version-decl module-header target-decl float-profile-decl?
//	struct-decl* const-decl* func-def* kernel-def*
//
// There is deliberately no host surface: no globals, no imports, no link
// dependencies, no entry point, no TLS (§1). A .gvir module is device-only
// and its host integration lives entirely in gvir_arch.md §6.
type Module struct {
	Version   Version
	Name      string
	Target    *Target
	Profile   FloatProfile
	Structs   []*Struct
	Constants []*Const
	Funcs     []*Func
	Kernels   []*Kernel
}

// FloatProfile is the module-wide `float_profile` declaration (§11.6). The
// two flags are orthogonal and both default to off; off means strict IEEE
// with no contraction and no approximate opcodes.
type FloatProfile struct {
	Contract bool // permits mul+add -> fma fusion
	Approx   bool // enables rcp/rsqrt/sin/cos/exp2/log2/tanh
}

func (p FloatProfile) Declared() bool { return p.Contract || p.Approx }

// ---------------------------------------------------------------------------
// Structs and constants (§2, §4.7)
// ---------------------------------------------------------------------------

type Field struct {
	Name string
	Type Type
}

// Struct is a memory-only aggregate. Layout is defined by §4.7 and computed
// here (layout.go), not inherited from any C ABI.
type Struct struct {
	Name   string
	Fields []Field
}

func (s *Struct) FieldByName(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// FieldIndex returns the positional index of a field, or -1. `field.ptr`
// takes a literal index (§8.3), not a name, so frontends resolve names here.
func (s *Struct) FieldIndex(name string) int {
	for i, f := range s.Fields {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// Const is a module-scope compile-time value (§2). i1, vec[i1,N] and
// submask are value-only and never legal as a const type (§4.1, §4.5, §4.6).
type Const struct {
	Name string
	Type Type
	Init ConstInit
}

// ConstInit is the const-init grammar (§2): literal | zero | aggregate.
// There is no address-of form — there are no module-scope objects to take
// the address of.
type ConstInit interface{ isInit() }

type InitLiteral struct{ Value Operand }
type InitZero struct{}
type InitAggregate struct{ Elems []ConstInit }

func (InitLiteral) isInit()   {}
func (InitZero) isInit()      {}
func (InitAggregate) isInit() {}

// ---------------------------------------------------------------------------
// Functions and kernels (§6)
// ---------------------------------------------------------------------------

type Param struct {
	Name string
	Type Type
}

// Body is the block structure shared by funcs and kernels (§7.1): one
// unlabelled entry block followed by labelled blocks.
type Body struct {
	Entry  *Block
	Blocks []*Block
}

// AllBlocks returns the entry block followed by the labelled blocks.
func (b *Body) AllBlocks() []*Block {
	out := make([]*Block, 0, len(b.Blocks)+1)
	if b.Entry != nil {
		out = append(out, b.Entry)
	}
	return append(out, b.Blocks...)
}

// BlockByLabel finds a labelled block. It deliberately never returns the
// entry block: the entry block cannot be branched to (§7.1).
func (b *Body) BlockByLabel(label string) *Block {
	for _, x := range b.Blocks {
		if x.Label == label {
			return x
		}
	}
	return nil
}

// Func is a device-side helper (§6.4). Direct calls only; recursion,
// indirect calls and address-taking are illegal, so there is no signature
// table and no function-pointer type anywhere in this package.
//
// There is no inline/noinline attribute: inlining is not observable and not
// controllable (§6.4).
type Func struct {
	Name     string
	Params   []Param
	Ret      Type // value type or Void; never struct/array (§6.4)
	Readonly bool // writes through no pointer reachable from its arguments
	Body
}

// GroupShape is the `group_size X,Y,Z` exact shape contract (§6.1).
type GroupShape struct{ X, Y, Z int }

func (g GroupShape) Threads() int { return g.X * g.Y * g.Z }

// GroupVar is one kernel-scoped, statically sized `group` declaration
// (§8.2). Zero-initialization is not guaranteed.
type GroupVar struct {
	Name  string
	Type  Type
	Align int // 0 = natural
}

// DynamicGroup is the one launch-sized group allocation a kernel may
// declare (§6.1, §8.2). Its byte size is read with the dynamic_group_size
// builtin; access past it is UB (§12.7), the single host-contract trigger.
type DynamicGroup struct {
	Name  string
	Align int // 0 = natural
}

// Kernel is a dispatchable entry point (§6.1). Kernels implicitly return
// void; `return` inside a kernel takes no operand.
type Kernel struct {
	Name         string
	Params       []Param
	GroupSize    *GroupShape   // nil = no compile-time shape contract
	MaxGroupSize int           // 0 = unset
	SubgroupSize int           // 0 = unset; gated on msl (§4.3, §9.2)
	DynamicGroup *DynamicGroup // nil = none; at most one per kernel
	Groups       []*GroupVar
	Body
}

func (k *Kernel) GroupByName(name string) *GroupVar {
	for _, g := range k.Groups {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Blocks (§7.1, §7.2)
// ---------------------------------------------------------------------------

// MergeKind discriminates the two merge-decl forms (§7.2).
type MergeKind uint8

const (
	MergeSelection MergeKind = iota // merge L
	MergeLoop                       // loop_merge Lexit, Lcontinue
)

// Merge is the reconvergence annotation a block carries on the line
// immediately after its label (§7.2). Note the §2 grammar attaches
// merge-decl to `block` only, never to `entry-block`; whether an entry
// block ending in a multi-successor br_if is representable is a question
// for ir/verify, not something this package silently decides.
type Merge struct {
	Kind     MergeKind
	Merge    string // `merge L`, or a loop's Lexit
	Continue string // loop only: Lcontinue
}

// Block is one sequence of body-lines ending in exactly one terminator
// (§7.1). Label is "" for the entry block. Lines holds every body-line in
// source order, including alloca-lines (which §8.1 requires to come first
// in the entry block, enforced by ir/verify) and loc-lines.
type Block struct {
	Label string
	Merge *Merge // nil unless the block carries a reconvergence annotation
	Lines []*Instruction
	Term  Terminator
}

// Instruction is one body-line. Exactly one suffix channel is used per
// opcode, and which one is registered in opTable (opcode.go): Suffix for a
// type suffix (and for the bare `ptr` suffix word, spelled PtrWord), Dim
// for dimension-suffixed builtins (§9), Exec for barrier (§10.1).
// OpInvalid is never a legal instruction opcode.
type Instruction struct {
	Result string
	Op     Opcode
	Suffix Type
	Dim    Dim
	Exec   ExecScope
	Args   []Operand
	Align  int // `align N` clause (§8.3); 0 = natural
}

// ---------------------------------------------------------------------------
// Terminators (§2, §7.1)
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
type Return struct{ Value *Operand } // nil Value in kernels and void funcs
type Unreachable struct{}            // executing one is UB (§12.6)

func (Br) isTerm()          {}
func (BrIf) isTerm()        {}
func (Switch) isTerm()      {}
func (Return) isTerm()      {}
func (Unreachable) isTerm() {}

// Successors returns the labels a terminator may transfer to, deduplicated
// in first-occurrence order. A br_if whose arms are identical has one
// successor, which is why §7.2's "more than one distinct successor" test
// can be written against this function.
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

// ---------------------------------------------------------------------------
// Module lookups. The namespace is flat and module-wide (§2).
// ---------------------------------------------------------------------------

func (m *Module) StructByName(name string) *Struct {
	for _, s := range m.Structs {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (m *Module) ConstByName(name string) *Const {
	for _, c := range m.Constants {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (m *Module) FuncByName(name string) *Func {
	for _, f := range m.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func (m *Module) KernelByName(name string) *Kernel {
	for _, k := range m.Kernels {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Lexical helpers (§2)
// ---------------------------------------------------------------------------

// ValidIdent reports whether s matches [A-Za-z_][A-Za-z0-9_]* and contains
// no "__". The double underscore is forbidden module-wide because it is
// reserved as the host symbol ABI separator (§2, gvir_arch.md §6).
func ValidIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
		if i > 0 && c == '_' && s[i-1] == '_' {
			return false
		}
	}
	return true
}