package ptx

import "strings"

// Item is one entry in an instruction body.
type Item interface{ item() }

// Pred is an optional guard predicate on an instruction.
type Pred struct {
	Reg Reg
	Neg bool
}

// IsSet reports whether the predicate is present.
func (p Pred) IsSet() bool { return p.Reg.IsValid() }

func (p Pred) Text() string {
	if !p.IsSet() {
		return ""
	}
	if p.Neg {
		return "@!" + p.Reg.Text()
	}
	return "@" + p.Reg.Text()
}

// Instr is a single PTX instruction. The mnemonic is not stored; it is
// derived from Op, Q and Types in canonical order by Mnemonic.
type Instr struct {
	Pred    Pred
	Op      Op
	Q       Quals
	Types   [2]Type
	Dst     []Operand
	Src     []Operand
	Comment string

	// custom holds the base mnemonic for Op == OpCustom.
	custom string
}

func (*Instr) item() {}

// If guards the instruction on p.
func (i *Instr) If(p Reg) *Instr { i.Pred = Pred{Reg: p}; return i }

// IfNot guards the instruction on the negation of p.
func (i *Instr) IfNot(p Reg) *Instr { i.Pred = Pred{Reg: p, Neg: true}; return i }

// Note attaches a trailing comment. Comments are whitespace in PTX and are
// emitted only when the printer is configured to include them.
func (i *Instr) Note(s string) *Instr { i.Comment = s; return i }

// Base returns the instruction's base mnemonic without qualifiers or types.
func (i *Instr) Base() string {
	if i.Op == OpCustom {
		return i.custom
	}
	return opTable[i.Op].mnemonic
}

// Mnemonic returns the fully qualified mnemonic: base, then every set
// qualifier in the canonical order recorded for the opcode, then the type
// specifiers. Equivalent IR always yields identical text.
func (i *Instr) Mnemonic() string {
	var b strings.Builder
	b.WriteString(i.Base())
	for _, k := range opTable[i.Op].quals {
		b.WriteString(i.Q.text(k))
	}
	for _, t := range i.Types {
		if t != NoType {
			b.WriteString(t.String())
		}
	}
	return b.String()
}

// Operands returns destinations followed by sources.
func (i *Instr) Operands() []Operand {
	out := make([]Operand, 0, len(i.Dst)+len(i.Src))
	out = append(out, i.Dst...)
	return append(out, i.Src...)
}

// LabelBind marks the position of a label in a body.
type LabelBind struct{ Label *Label }

func (*LabelBind) item() {}

// Label is a symbolic branch target. There are no byte offsets, so no
// fixed-point resolution pass exists.
type Label struct{ name string }

func (l *Label) Text() string { return l.name }
func (l *Label) Name() string { return l.name }
func (*Label) operand()       {}

// LocDir is a .loc debug directive. Func and Inlined are optional; when
// Inlined is set the directive carries the inlined_at attribute.
type LocDir struct {
	File   *File
	Line   int
	Col    int
	Func   *Label // function_name label, optional
	FuncOff int64
	Inlined *LocSite
}

// LocSite is the inlined_at position of a LocDir.
type LocSite struct {
	File *File
	Line int
	Col  int
}

func (*LocDir) item() {}

// BranchTargets is a .branchtargets directive naming the destinations of a
// brx.idx.
type BranchTargets struct {
	Name    string
	Targets []*Label
}

func (*BranchTargets) item() {}
func (b *BranchTargets) Text() string { return b.Name }
func (*BranchTargets) operand()       {}

// CallTargets is a .calltargets directive naming the possible destinations
// of an indirect call.
type CallTargets struct {
	Name    string
	Targets []*Func
}

func (*CallTargets) item() {}
func (c *CallTargets) Text() string { return c.Name }
func (*CallTargets) operand()       {}

// Pragma is a .pragma directive. It is valid at module scope, at entry
// scope, and as a statement inside a body.
type Pragma struct{ Strings []string }

func (*Pragma) item() {}

// Block is a brace-delimited nested scope, used for the call sequence and
// for scoping temporary .param variables.
type Block struct {
	Locals []*Var
	Items  []Item
}

func (*Block) item() {}