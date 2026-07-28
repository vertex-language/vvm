package amdtx

import "strconv"

// Item is one entry in an instruction body.
type Item interface{ item() }

// Instr is a single AMDTX instruction. The mnemonic is not stored; it is
// derived from Op and Width, so equivalent IR always prints identically.
type Instr struct {
	Op      Op
	Width   Width // data width for width-suffixed mnemonics
	Dst     []Operand
	Src     []Operand
	Mods    []Mod
	Enc     Enc
	Comment string

	// Fence ordering and scope; set only for OpFence.
	Ord   Ordering
	Scope Scope

	// custom holds the base mnemonic for Op == OpCustom.
	custom string
}

func (*Instr) item() {}

// Base returns the mnemonic without its width suffix.
func (i *Instr) Base() string {
	if i.Op == OpCustom {
		return i.custom
	}
	return opTable[i.Op].mnemonic
}

// Mnemonic returns the full mnemonic, including the width suffix derived
// from the data register.
func (i *Instr) Mnemonic() string {
	b := i.Base()
	if i.Op != OpCustom && opTable[i.Op].widthSuffix && i.Width != NoWidth {
		b += i.Width.Suffix()
	}
	return b
}

// Class returns the instruction family.
func (i *Instr) Class() Class { return Classify(i.Mnemonic()) }

// Pin asserts a physical encoding. Verification fails if instruction
// selection picks a different one, or if the encoding cannot represent the
// operands (V25).
func (i *Instr) Pin(e Enc) *Instr { i.Enc = e; return i }

// With appends modifiers.
func (i *Instr) With(m ...Mod) *Instr { i.Mods = append(i.Mods, m...); return i }

// Note attaches a trailing comment. Comments are whitespace and the
// canonical printer drops them; they survive only in hand-written text.
func (i *Instr) Note(s string) *Instr { i.Comment = s; return i }

// Operands returns destinations followed by sources.
func (i *Instr) Operands() []Operand {
	out := make([]Operand, 0, len(i.Dst)+len(i.Src))
	out = append(out, i.Dst...)
	return append(out, i.Src...)
}

// ---- Labels and debug -----------------------------------------------------

// Label is a symbolic branch target. There are no byte offsets, so no
// fixed-point resolution pass exists.
type Label struct{ name string }

func (l *Label) Text() string { return l.name }
func (l *Label) Name() string { return l.name }
func (*Label) operand()       {}

// LabelBind marks the position of a label in a body.
type LabelBind struct{ Label *Label }

func (*LabelBind) item() {}

// LocDir is a .loc directive; it attaches to the next instruction.
type LocDir struct {
	File *File
	Line int
	Col  int
}

func (*LocDir) item() {}

// ---- Structured control flow ----------------------------------------------

// IfStmt is a structured conditional region. The guard's register class
// determines uniformity: .lanemask is divergent and lowers to an EXEC-mask
// save/and/restore, an .sgpr.b32 or %scc guard is uniform and lowers to
// s_cbranch_scc* (§10.1).
type IfStmt struct {
	Guard  Operand
	Assert Uniformity
	Then   *Body
	Else   *Body

	owner *Body
}

func (*IfStmt) item() {}

// ElseBody creates the else block on first call and returns it.
func (s *IfStmt) ElseBody() *Body {
	if s.Else == nil {
		s.Else = s.owner.sub()
	}
	return s.Else
}

// LoopStmt is a structured loop region.
type LoopStmt struct{ Body *Body }

func (*LoopStmt) item() {}

// BreakIf leaves the innermost enclosing loop when the guard is set.
type BreakIf struct {
	Guard  Operand
	Assert Uniformity
}

func (*BreakIf) item() {}

// ContinueIf restarts the innermost enclosing loop when the guard is set.
type ContinueIf struct {
	Guard  Operand
	Assert Uniformity
}

func (*ContinueIf) item() {}

// ---- Escape hatches -------------------------------------------------------

// Raw passes text or bytes through the pipeline untouched. Both forms are
// optimisation barriers: no pass moves an instruction across them and no
// pass reasons about their effects beyond the declared lists. An escape
// hatch that lies to the register allocator is worse than no escape hatch,
// so the lists are mandatory (P8, V39).
type Raw struct {
	Text     string   // raw "..." form
	Bytes    []uint32 // rawbytes form; non-nil selects it
	Defs     []Operand
	Uses     []Operand
	Clobbers []Operand
	Comment  string
}

func (*Raw) item() {}

// IsBytes reports whether r is the rawbytes form.
func (r *Raw) IsBytes() bool { return r.Bytes != nil }

// Subst returns the operand bound to %N, indexed over the combined Defs and
// Uses lists.
func (r *Raw) Subst(n int) (Operand, bool) {
	if n < 0 {
		return nil, false
	}
	if n < len(r.Defs) {
		return r.Defs[n], true
	}
	n -= len(r.Defs)
	if n < len(r.Uses) {
		return r.Uses[n], true
	}
	return nil, false
}

// Def declares a register the raw sequence writes.
func (r *Raw) Def(ops ...Operand) *Raw { r.Defs = append(r.Defs, ops...); return r }

// Use declares a register the raw sequence reads.
func (r *Raw) Use(ops ...Operand) *Raw { r.Uses = append(r.Uses, ops...); return r }

// Clobber declares a register the raw sequence destroys.
func (r *Raw) Clobber(ops ...Operand) *Raw {
	r.Clobbers = append(r.Clobbers, ops...)
	return r
}

func itoa(n int) string { return strconv.Itoa(n) }