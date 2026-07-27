package ptx

// CodeItem is one entry in a body: an instruction, a label binding, a
// .loc directive, a .branchtargets directive, or a blank separator line.
type CodeItem interface{ isCodeItem() }

// Instruction is a single (possibly guarded) PTX instruction. Opcode is
// the fully joined mnemonic including qualifiers ("mad.lo.u32"). Raw, if
// non-empty, is emitted verbatim (still honoring Guard).
type Instruction struct {
	Guard    string // "@%p1" / "@!%p1" / ""
	Opcode   string
	Operands []Operand
	Raw      string
	Comment  string
}

func (*Instruction) isCodeItem() {}

// LabelBind marks the position of a label.
type LabelBind struct{ Label *Label }

func (*LabelBind) isCodeItem() {}

// BlankLine is an explicit blank separator.
type BlankLine struct{}

func (*BlankLine) isCodeItem() {}

// LocDirective is a .loc debug directive.
type LocDirective struct{ File, Line, Col int }

func (*LocDirective) isCodeItem() {}

// BranchTargets is a .branchtargets directive for brx.idx.
type BranchTargets struct {
	Name    string
	Targets []*Label
}

func (*BranchTargets) isCodeItem() {}

// Label is a symbolic branch target — no offset resolution is ever
// needed; labels print by name.
type Label struct{ name string }

func (l *Label) Name() string { return l.name }
func (l *Label) Text() string { return l.name }