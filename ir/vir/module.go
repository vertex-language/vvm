// vir/module.go
// module.go
package vir

// Module is the single IR-level program representation (README §"One Idea").
// Field order mirrors the mandatory section order (§1.2).
type Module struct {
	Name       string
	Target     *Target     // nil for pure-compute modules (§1.2 rule 10)
	AsmDialect *AsmDialect // nil unless declared; module-wide asm syntax dialect (§1.2 rule 11)
	Structs []*Struct
	FunctionSignatures []*FunctionSignature
	Constants          []*Constant
	Globals            []*Global
	Links               []*Link
	Externs             []*ExternGroup
	Functions           []*Function
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

func (s *Struct) FieldByName(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// FunctionSignature is a named signature used to type-check indirect
// calls/tailcalls (§1.3).
type FunctionSignature struct {
	Name     string
	Params   []Type
	Variadic bool
	Ret      Type
}

// Constant is an immutable compile-time scalar (§8). Value is a literal operand.
type Constant struct {
	Name  string
	Type  Type
	Value Operand
}

// ConstInit is the global-initializer grammar (§8).
type ConstInit interface{ isInit() }

type InitLiteral struct{ Value Operand } // scalar literal / null
type InitZero struct{}                  // zero
type InitAddressOf struct{ Name string } // addr ident
type InitAggregate struct{ Elems []ConstInit }
type InitByteString struct{ Data []byte } // "..." for array[i8, N]

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

// ExternGroup declares imported functions and the dependency that provides
// them (§7.4). There is no anonymous/default-namespace group: Dependency
// must always be a non-empty string matching a previously declared Link's
// Name byte-for-byte (§1.2 rule 9). Even libc must be declared via an
// ordinary `link` line and named here explicitly.
type ExternGroup struct {
	Dependency string // byte-for-byte link string; never "" (§1.2 rule 9)
	Functions  []*ExternFunction
}

type FunctionAttribute string

const (
	AttributeNoReturn FunctionAttribute = "noreturn"
	AttributeReadonly FunctionAttribute = "readonly"
	AttributeInline   FunctionAttribute = "inline"
	AttributeNoInline FunctionAttribute = "noinline"
	AttributeCold     FunctionAttribute = "cold"
	// AttributeEntry marks the function as the platform handoff point
	// (§1.1 fn-attr, §9.4a). At most one fn per module may carry it; the
	// linker resolves what it's actually wired to (process entry, DLL
	// load hook, driver entry, ...) at link time, based on the requested
	// output kind — the IR module itself asserts only "this is the
	// function you'd hand control to, if you hand control to anything."
	AttributeEntry FunctionAttribute = "entry"
)

type Param struct {
	Name  string
	Type  Type
	ByVal string // struct name, "" if absent
	SRet  string // struct name, "" if absent
}

type ExternFunction struct {
	Name     string
	Params   []Param
	Variadic bool
	Ret      Type
	Attrs    []FunctionAttribute
}

type Function struct {
	Name   string
	Params []Param
	Ret    Type
	Attrs  []FunctionAttribute
	Export bool
	Entry  *Block   // unlabeled, untargetable (§1.3 rule 1) — the function's own entry BLOCK, distinct from the entry ATTRIBUTE above
	Blocks []*Block // labeled blocks in textual order
}

func (f *Function) HasAttribute(a FunctionAttribute) bool {
	for _, x := range f.Attrs {
		if x == a {
			return true
		}
	}
	return false
}

// IsVariadic reports whether the function is variadic. The grammar can't
// express this for fn-def (§1.2 rule 5); kept for symmetry with ExternFunction.
func (f *Function) IsVariadic() bool { return false }

// AllBlocks returns entry followed by labeled blocks.
func (f *Function) AllBlocks() []*Block {
	out := make([]*Block, 0, len(f.Blocks)+1)
	if f.Entry != nil {
		out = append(out, f.Entry)
	}
	return append(out, f.Blocks...)
}

type Block struct {
	Label string     // "" for entry
	Lines []BodyLine // ordinary instructions (incl. loc) and asm blocks, in order
	Term  Terminator
}

// BodyLine is one body-line (§1.1 body-line grammar): either an ordinary
// instruction (including a `loc` line) or an inline-asm block. Exactly one
// of Instruction / Asm is set.
type BodyLine struct {
	Instruction *Instruction
	Asm         *AsmBlock
}

// Instruction is one instruction body-line. The textual `<op>.<suffix>` is
// stored split: Op holds the base name; exactly one of Suffix (a type) or
// Sig (a fnsig name, for indirect call/tailcall) may be set.
type Instruction struct {
	Result string // "" iff the instruction produces no value (§1.3 rule 6)
	Op     string
	Suffix Type   // nil if no type suffix
	Sig    string // fnsig name for call.<fnsig>; "" otherwise
	Args   []Operand
	Align  int // trailing ", align N"; 0 = unspecified
}

// ---------------------------------------------------------------------------
// Inline assembly (§4).
// ---------------------------------------------------------------------------

// AsmDialect is the module-wide asmdialect-decl token (§1.1 grammar, §1.2
// rule 11, §4). It is declared once per module, not per asm block.
type AsmDialect string

const (
	DialectIntel  AsmDialect = "intel"
	DialectATT    AsmDialect = "att"
	DialectA32    AsmDialect = "a32"
	DialectT32    AsmDialect = "t32"
	DialectNative AsmDialect = "native"
)

// AsmBindingKind is the kind of one asm binding line (§4 Bindings).
type AsmBindingKind string

const (
	BindingIn      AsmBindingKind = "in"
	BindingOut     AsmBindingKind = "out"
	BindingClobber AsmBindingKind = "clobber"
)

// AsmBinding is one `in`/`out`/`clobber` line at the top of an asm block.
// Register is used for in/out (one register each); Registers is used for
// clobber (one or more registers on the line); Ident is the bound IR value,
// for in/out only.
type AsmBinding struct {
	Kind      AsmBindingKind
	Register  string
	Registers []string
	Ident     string
}

// AsmOperandKind discriminates operand forms inside an asm-line (§1.1
// asm-operand grammar).
type AsmOperandKind string

const (
	AsmOperandKindRegister AsmOperandKind = "register"
	AsmOperandKindImmediate AsmOperandKind = "immediate"
	AsmOperandKindMemory    AsmOperandKind = "memory"
	AsmOperandKindLabel     AsmOperandKind = "label" // branch target, asm-local (§4)
)

type AsmOperand struct {
	Kind      AsmOperandKind
	Register  string  // for AsmOperandKindRegister
	Immediate Operand // for AsmOperandKindImmediate
	Memory    string  // for AsmOperandKindMemory — raw dialect-specific addressing text
	Label     string  // for AsmOperandKindLabel
}

func AsmRegister(r string) AsmOperand      { return AsmOperand{Kind: AsmOperandKindRegister, Register: r} }
func AsmImmediate(o Operand) AsmOperand    { return AsmOperand{Kind: AsmOperandKindImmediate, Immediate: o} }
func AsmMemory(text string) AsmOperand     { return AsmOperand{Kind: AsmOperandKindMemory, Memory: text} }
func AsmLabelReference(name string) AsmOperand {
	return AsmOperand{Kind: AsmOperandKindLabel, Label: name}
}

// AsmCodeLine is one line inside `code:` — either a mnemonic instruction or
// a dialect-local label declaration (§1.1 asm-line grammar).
type AsmCodeLine struct {
	LabelDeclaration string // non-empty ⇒ this line is solely "name:" (§4 label isolation)
	Mnemonic         string // empty when LabelDeclaration is set
	Operands         []AsmOperand
}

func AsmInstructionLine(mnemonic string, ops ...AsmOperand) AsmCodeLine {
	return AsmCodeLine{Mnemonic: mnemonic, Operands: ops}
}
func AsmLabelDeclaration(name string) AsmCodeLine {
	return AsmCodeLine{LabelDeclaration: name}
}

// AsmBlock is a whole inline-assembly block (§4). It is an ordinary
// body-line — ordering relative to other instructions matters — but is not
// a terminator (no `asm goto`). The dialect that governs its `code:` syntax
// comes from the enclosing module's AsmDialect, not from the block itself
// (§1.2 rule 11).
type AsmBlock struct {
	Bindings []AsmBinding
	Code     []AsmCodeLine
}

// ---------------------------------------------------------------------------
// Terminators (§5).
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
type Return struct{ Value *Operand } // nil for `return` of void
type TailCall struct {
	Callee string    // direct callee name; "" if indirect
	Sig    string    // fnsig name for indirect; "" if direct
	Args   []Operand // for indirect, Args[0] is the callee ptr
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
	return nil // return, tailcall, trap, unreachable
}

// EntryFunction returns the module's single `entry`-attributed function, or
// nil if none is declared (§9.4a). At most one exists in a verified module.
func (m *Module) EntryFunction() *Function {
	for _, f := range m.Functions {
		if f.HasAttribute(AttributeEntry) {
			return f
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Recognized `main`-style entry signatures (vvm's entry-thunk gate).
// ---------------------------------------------------------------------------

// MainSignature identifies a recognized libc-style `main` shape. This is
// purely a shape check at the IR level — deciding *whether* an fn matching
// one of these shapes should actually get a libc-style process-entry
// wrapper synthesized (vs. wired in raw) is a policy call made one layer
// up, by vvm, based on target OS and output kind. A struct/array-heavy or
// otherwise unusual entry fn simply reports MainSignatureNone and is always
// wired in raw.
type MainSignature int

const (
	MainSignatureNone         MainSignature = iota // not a recognized shape — wire in raw
	MainSignatureBare                               // () i32
	MainSignatureArgcArgv                           // (i32, ptr) i32
	MainSignatureArgcArgvEnvp                       // (i32, ptr, ptr) i32
)

// RecognizedMainSignature reports which (if any) libc-style `main` shape f
// matches. Return type must be exactly i32 (an exit code); this alone
// already excludes void/noreturn entries, which §9.4a rejects pairing with
// `entry` in the first place.
func RecognizedMainSignature(f *Function) MainSignature {
	if !Equal(f.Ret, I32) {
		return MainSignatureNone
	}
	switch len(f.Params) {
	case 0:
		return MainSignatureBare
	case 2:
		if Equal(f.Params[0].Type, I32) && IsPtr(f.Params[1].Type) {
			return MainSignatureArgcArgv
		}
	case 3:
		if Equal(f.Params[0].Type, I32) && IsPtr(f.Params[1].Type) && IsPtr(f.Params[2].Type) {
			return MainSignatureArgcArgvEnvp
		}
	}
	return MainSignatureNone
}