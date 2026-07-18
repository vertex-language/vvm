package vir

// Module is the single IR-level program representation (README §"One Idea").
// Field order mirrors the mandatory section order (§1.2).
type Module struct {
	Name    string
	Target  *Target // nil for pure-compute modules (§1.2 rule 10)
	Structs []*Struct
	FnSigs  []*FnSig
	Consts  []*Const
	Globals []*Global
	Links   []*Link
	Externs []*ExternGroup
	Funcs   []*Func
}

// Target is the in-file target declaration (§10.6).
type Target struct {
	Arch  string   // canonical spelling only (§10.1)
	OS    string   // canonical spelling only (§10.2)
	ABI   string   // canonical spelling or "" (§10.3)
	Tiers []string // feature-tier flags (§10.4)
}

type Field struct {
	Name string
	Type Type
}

type Struct struct {
	Name   string
	Fields []Field
}

func (s *Struct) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// FnSig is a named signature used to type-check indirect calls (§1.3).
type FnSig struct {
	Name     string
	Params   []Type
	Variadic bool
	Ret      Type
}

// Const is an immutable compile-time scalar (§8). Value is a literal operand.
type Const struct {
	Name  string
	Type  Type
	Value Operand
}

// ConstInit is the global-initializer grammar (§8).
type ConstInit interface{ isInit() }

type InitLit struct{ Value Operand } // scalar literal / null
type InitZero struct{}               // zero
type InitAddr struct{ Name string }  // addr ident
type InitAgg struct{ Elems []ConstInit }
type InitBytes struct{ Data []byte } // "..." for array[i8, N]

func (InitLit) isInit()   {}
func (InitZero) isInit()  {}
func (InitAddr) isInit()  {}
func (InitAgg) isInit()   {}
func (InitBytes) isInit() {}

type Global struct {
	Name   string
	Type   Type
	Export bool
	TLS    bool
	Align  int // 0 = natural
	Init   ConstInit
}

// LinkKind names the portable dependency semantic (§7.4).
type LinkKind string

const (
	LinkStatic    LinkKind = "static"
	LinkShared    LinkKind = "shared"
	LinkFramework LinkKind = "framework"
)

type Link struct {
	Kind LinkKind
	Name string // short or exact name, as written
}

type ExternGroup struct {
	Dep string // byte-for-byte link string; "" = anonymous group
	Fns []*ExternFn
}

type FnAttr string

const (
	AttrNoReturn FnAttr = "noreturn"
	AttrReadonly FnAttr = "readonly"
	AttrInline   FnAttr = "inline"
	AttrNoInline FnAttr = "noinline"
	AttrCold     FnAttr = "cold"
)

type Param struct {
	Name  string
	Type  Type
	ByVal string // struct name, "" if absent
	SRet  string // struct name, "" if absent
}

type ExternFn struct {
	Name     string
	Params   []Param
	Variadic bool
	Ret      Type
	Attrs    []FnAttr
}

type Func struct {
	Name   string
	Params []Param
	Ret    Type
	Attrs  []FnAttr
	Export bool
	Entry  *Block   // unlabeled, untargetable (§1.3 rule 1)
	Blocks []*Block // labeled blocks in textual order
}

func (f *Func) HasAttr(a FnAttr) bool {
	for _, x := range f.Attrs {
		if x == a {
			return true
		}
	}
	return false
}

// AllBlocks returns entry followed by labeled blocks.
func (f *Func) AllBlocks() []*Block {
	out := make([]*Block, 0, len(f.Blocks)+1)
	if f.Entry != nil {
		out = append(out, f.Entry)
	}
	return append(out, f.Blocks...)
}

type Block struct {
	Label string // "" for entry
	Insts []Inst // includes loc lines (Op == "loc")
	Term  Terminator
}

// Inst is one body line. The textual `<op>.<suffix>` is stored split:
// Op holds the base name; exactly one of Suffix (a type) or Sig (a fnsig
// name, for indirect call/tailcall) may be set.
type Inst struct {
	Result string // "" iff the instruction produces no value (§1.3 rule 6)
	Op     string
	Suffix Type   // nil if no type suffix
	Sig    string // fnsig name for call.<fnsig>; "" otherwise
	Args   []Operand
	Align  int // trailing ", align N"; 0 = unspecified
}

// Terminators (§5).
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
type Return struct{ Value *Operand } // nil for `return` of void
type TailCall struct {
	Callee string    // direct callee name; "" if indirect
	Sig    string    // fnsig name for indirect; "" if direct
	Args   []Operand // for indirect, Args[0] is the callee ptr
}
type Trap struct{}
type Unreachable struct{}

func (Br) isTerm()          {}
func (BrIf) isTerm()        {}
func (Switch) isTerm()      {}
func (Return) isTerm()      {}
func (TailCall) isTerm()    {}
func (Trap) isTerm()        {}
func (Unreachable) isTerm() {}

// Successors returns the labels a terminator may transfer to.
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
	return nil // return, tailcall, trap, unreachable
}