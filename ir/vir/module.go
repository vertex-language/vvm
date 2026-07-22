// module.go
package vir

// Module is the single IR-level program representation (README §"One Idea").
// Field order mirrors the mandatory section order (§2.1).
type Module struct {
	Name               string
	Namespace          string // "" unless declared (§2.1 step 2, §6.3)
	Target             *Target
	Structs            []*Struct
	FunctionSignatures []*FunctionSignature
	Constants          []*Constant
	Globals            []*Global
	Links              []*Link
	Externs            []*ExternGroup
	Imports            []*Import
	Functions          []*Function
}

// Target is the in-file target declaration (§7.1).
type Target struct {
	Arch  string
	OS    string
	ABI   string
	Tiers []string
}

type Field struct {
	Name string
	Type Type
}

type Struct struct {
	Name   string
	Export bool
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

// FunctionSignature is a named signature used to type-check indirect
// calls/tailcalls (§4.2).
type FunctionSignature struct {
	Name     string
	Export   bool
	Params   []Type
	Variadic bool
	Ret      Type
}

// Constant is an immutable compile-time scalar (§6.2). Value is a literal operand.
type Constant struct {
	Name   string
	Export bool
	Type   Type
	Value  Operand
}

// ConstInit is the global-initializer grammar (§6.2).
type ConstInit interface{ isInit() }

type InitLiteral struct{ Value Operand }
type InitZero struct{}
type InitAddressOf struct{ Name string }
type InitAggregate struct{ Elems []ConstInit }
type InitByteString struct{ Data []byte }

func (InitLiteral) isInit()    {}
func (InitZero) isInit()       {}
func (InitAddressOf) isInit()  {}
func (InitAggregate) isInit()  {}
func (InitByteString) isInit() {}

type Global struct {
	Name   string
	Type   Type
	Export bool
	TLS    bool
	Align  int // 0 = natural
	Init   ConstInit
}

// LinkKind names the portable dependency semantic (§7.2).
type LinkKind string

const (
	LinkStatic    LinkKind = "static"
	LinkShared    LinkKind = "shared"
	LinkFramework LinkKind = "framework"
)

type Link struct {
	Kind LinkKind
	Name string
}

// ExternGroup declares imported functions and the dependency that provides
// them (§7.2). Dependency must always be a non-empty string matching a
// previously declared Link's Name byte-for-byte.
type ExternGroup struct {
	Dependency string
	Functions  []*ExternFunction
}

// Import is one cross-module `import` declaration (§7.3). Path is either
// "namespace/module" or bare "module" — resolved against real modules by
// the importer package, not by anything in this one.
type Import struct {
	Path string
}

type FunctionAttribute string

const (
	AttributeNoReturn FunctionAttribute = "noreturn"
	AttributeReadonly FunctionAttribute = "readonly"
	AttributeInline   FunctionAttribute = "inline"
	AttributeNoInline FunctionAttribute = "noinline"
	AttributeCold     FunctionAttribute = "cold"
	// AttributeEntry marks the platform handoff point (§2.2, §9.4a). At
	// most one fn per module may carry it; forces a bare symbol.
	AttributeEntry FunctionAttribute = "entry"
	// AttributeExternC forces a bare C symbol even in a namespaced module
	// (§2.2, §6.3). Mutually exclusive with AttributeEntry on the same fn
	// — both are distinct overrides of symbol naming, never resolved by
	// silent precedence.
	AttributeExternC FunctionAttribute = "extern_c"
)

type Param struct {
	Name  string
	Type  Type
	ByVal string
	SRet  string
}

type ExternFunction struct {
	Name     string
	Params   []Param
	Variadic bool
	Ret      Type
	Attrs    []FunctionAttribute
}

type Function struct {
	Name     string
	Params   []Param
	Variadic bool // param-list ends in "..." (§4.4)
	Ret      Type
	Attrs    []FunctionAttribute
	Export   bool
	Entry    *Block
	Blocks   []*Block
}

func (f *Function) HasAttribute(a FunctionAttribute) bool {
	for _, x := range f.Attrs {
		if x == a {
			return true
		}
	}
	return false
}

// AllBlocks returns entry followed by labeled blocks.
func (f *Function) AllBlocks() []*Block {
	out := make([]*Block, 0, len(f.Blocks)+1)
	if f.Entry != nil {
		out = append(out, f.Entry)
	}
	return append(out, f.Blocks...)
}

// Block is one labeled (or, for the entry block, unlabeled) sequence of
// instructions ending in exactly one terminator (§4.3). Lines holds every
// non-terminator body-line, including `loc` (§2.3 body-line grammar).
type Block struct {
	Label string
	Lines []*Instruction
	Term  Terminator
}

// Instruction is one instruction body-line. Op holds the opcode
// (opcode.go); exactly one of Suffix (a type) or Sig (a fnsig name, for
// indirect call/tailcall, or the self-referential token for va_start) may
// be set. OpInvalid is never a legal instruction opcode.
type Instruction struct {
	Result string
	Op     Opcode
	Suffix Type
	Sig    string
	Args   []Operand
	Align  int
}

// ---------------------------------------------------------------------------
// Terminators (§4.3).
// ---------------------------------------------------------------------------

type Terminator interface{ isTerm() }

type Branch struct{ Label string }
type BranchIf struct {
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
type Return struct{ Value *Operand }
type TailCall struct {
	Callee string
	Sig    string
	Args   []Operand
}
type Trap struct{}
type Unreachable struct{}

func (Branch) isTerm()      {}
func (BranchIf) isTerm()    {}
func (Switch) isTerm()      {}
func (Return) isTerm()      {}
func (TailCall) isTerm()    {}
func (Trap) isTerm()        {}
func (Unreachable) isTerm() {}

// Successors returns the labels a terminator may transfer to.
func Successors(t Terminator) []string {
	switch x := t.(type) {
	case Branch:
		return []string{x.Label}
	case BranchIf:
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

// EntryFunction returns the module's single `entry`-attributed function, or
// nil if none is declared (§9.4a).
func (m *Module) EntryFunction() *Function {
	for _, f := range m.Functions {
		if f.HasAttribute(AttributeEntry) {
			return f
		}
	}
	return nil
}