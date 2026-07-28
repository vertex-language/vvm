package amdtx

// Body is an instruction body: a register file and an ordered, editable
// list of items. Nested blocks share the enclosing body's register file and
// label namespace, because registers and labels are body-scoped, not
// block-scoped (§3.2).
type Body struct {
	Regs *RegFile

	items  []Item
	labels map[string]bool
}

// NewBody returns an empty body with a fresh register file.
func NewBody() *Body {
	return &Body{Regs: newRegFile(), labels: map[string]bool{}}
}

func (b *Body) sub() *Body {
	return &Body{Regs: b.Regs, labels: b.labels}
}

// ---- Inspection -----------------------------------------------------------

// Len returns the number of items in the body.
func (b *Body) Len() int { return len(b.items) }

// At returns the item at index i.
func (b *Body) At(i int) Item { return b.items[i] }

// Items returns the underlying slice. Callers must not append to it; use
// Append, InsertBefore, Replace and Remove instead.
func (b *Body) Items() []Item { return b.items }

// InstrAt returns the instruction at index i, or nil if the item at i is
// not an instruction.
func (b *Body) InstrAt(i int) *Instr {
	if i < 0 || i >= len(b.items) {
		return nil
	}
	in, _ := b.items[i].(*Instr)
	return in
}

// ---- Mutation -------------------------------------------------------------

// Append adds items to the end of the body.
func (b *Body) Append(its ...Item) { b.items = append(b.items, its...) }

// InsertBefore inserts items so that the first lands at index i.
func (b *Body) InsertBefore(i int, its ...Item) {
	rest := append([]Item(nil), b.items[i:]...)
	b.items = append(append(b.items[:i:i], its...), rest...)
}

// Replace substitutes the item at index i.
func (b *Body) Replace(i int, it Item) { b.items[i] = it }

// Remove deletes the item at index i.
func (b *Body) Remove(i int) { b.items = append(b.items[:i], b.items[i+1:]...) }

// RemoveRange deletes items in [lo, hi).
func (b *Body) RemoveRange(lo, hi int) { b.items = append(b.items[:lo], b.items[hi:]...) }

// ---- Labels and debug -----------------------------------------------------

// Label creates a fresh label, uniquifying against every name already
// issued in this body.
func (b *Body) Label(name string) *Label {
	n := name
	for i := 1; b.labels[n]; i++ {
		n = name + "_" + itoa(i)
	}
	b.labels[n] = true
	return &Label{name: n}
}

// Bind places a label at the current position.
func (b *Body) Bind(l *Label) *Body {
	b.items = append(b.items, &LabelBind{Label: l})
	return b
}

// Loc emits a .loc directive, which attaches to the next instruction.
func (b *Body) Loc(f *File, line, col int) *LocDir {
	d := &LocDir{File: f, Line: line, Col: col}
	b.items = append(b.items, d)
	return d
}

// ---- Structured regions ---------------------------------------------------

// If appends a structured conditional and returns it; write into s.Then and
// s.ElseBody(). An optional Uniformity is an assertion checked against the
// guard's class, not a declaration.
func (b *Body) If(guard Operand, u ...Uniformity) *IfStmt {
	s := &IfStmt{Guard: guard, Then: b.sub(), owner: b}
	if len(u) > 0 {
		s.Assert = u[0]
	}
	b.items = append(b.items, s)
	return s
}

// Loop appends a structured loop and returns the body to fill.
func (b *Body) Loop() *Body {
	s := &LoopStmt{Body: b.sub()}
	b.items = append(b.items, s)
	return s.Body
}

// BreakIf leaves the innermost enclosing loop when guard is set.
func (b *Body) BreakIf(guard Operand, u ...Uniformity) *BreakIf {
	s := &BreakIf{Guard: guard}
	if len(u) > 0 {
		s.Assert = u[0]
	}
	b.items = append(b.items, s)
	return s
}

// ContinueIf restarts the innermost enclosing loop when guard is set.
func (b *Body) ContinueIf(guard Operand, u ...Uniformity) *ContinueIf {
	s := &ContinueIf{Guard: guard}
	if len(u) > 0 {
		s.Assert = u[0]
	}
	b.items = append(b.items, s)
	return s
}

// ---- Escape hatches -------------------------------------------------------

// Raw appends a verbatim assembly fragment. Declare its registers with
// Def, Use and Clobber on the returned handle.
func (b *Body) Raw(text string) *Raw {
	r := &Raw{Text: text}
	b.items = append(b.items, r)
	return r
}

// RawBytes appends verbatim dwords to the instruction stream.
func (b *Body) RawBytes(words ...uint32) *Raw {
	r := &Raw{Bytes: words}
	b.items = append(b.items, r)
	return r
}

// ---- Analysis helpers -----------------------------------------------------

// UsedAfter reports whether o appears as a source at index i or later. It
// is a conservative linear scan with no control-flow analysis: a false
// result proves the value dead only in straight-line code.
func (b *Body) UsedAfter(o Operand, i int) bool {
	for ; i < len(b.items); i++ {
		in, ok := b.items[i].(*Instr)
		if !ok {
			continue
		}
		for _, s := range in.Src {
			found := false
			operandRegs(s, func(r Reg) {
				if SameReg(r, o) {
					found = true
				}
			})
			if found {
				return true
			}
		}
	}
	return false
}

// Defs reports the index of the last instruction that writes o at or before
// index i, or -1.
func (b *Body) Defs(o Operand, i int) int {
	for ; i >= 0; i-- {
		in, ok := b.items[i].(*Instr)
		if !ok {
			continue
		}
		for _, d := range in.Dst {
			if SameReg(d, o) {
				return i
			}
		}
	}
	return -1
}

// add appends an instruction and returns it.
func (b *Body) add(in *Instr) *Instr {
	b.items = append(b.items, in)
	return in
}