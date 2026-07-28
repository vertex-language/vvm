package ptx

// Body is an instruction body: a register file, function-scope variables,
// and an ordered, editable list of items.
type Body struct {
	Regs   *RegFile
	Locals []*Var

	items  []Item
	labels map[string]bool
	btSeq  int
	ctSeq  int
}

// NewBody returns an empty body with a fresh register file.
func NewBody() *Body {
	return &Body{Regs: newRegFile(), labels: map[string]bool{}}
}

// ---- Inspection -----------------------------------------------------------

// Len returns the number of items in the body.
func (b *Body) Len() int { return len(b.items) }

// At returns the item at index i.
func (b *Body) At(i int) Item { return b.items[i] }

// Items returns the underlying slice. Callers must not append to it; use
// Append, InsertBefore, Replace and Remove instead.
func (b *Body) Items() []Item { return b.items }

// InstrAt returns the instruction at index i, or nil if the item at i is not
// an instruction.
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
func (b *Body) Remove(i int) {
	b.items = append(b.items[:i], b.items[i+1:]...)
}

// RemoveRange deletes items in [lo, hi).
func (b *Body) RemoveRange(lo, hi int) {
	b.items = append(b.items[:lo], b.items[hi:]...)
}

// ---- Analysis helpers -----------------------------------------------------

// UsedAfter reports whether operand o appears as a source in any instruction
// at index i or later. It is a conservative linear scan with no control-flow
// analysis: a false result means the value is provably dead only in
// straight-line code.
func (b *Body) UsedAfter(o Operand, i int) bool {
	for ; i < len(b.items); i++ {
		in, ok := b.items[i].(*Instr)
		if !ok {
			continue
		}
		for _, s := range in.Src {
			if SameReg(s, o) {
				return true
			}
			if v, ok := s.(VecOp); ok {
				for _, e := range v {
					if SameReg(e, o) {
						return true
					}
				}
			}
			if m, ok := s.(Mem); ok && m.Base != nil && SameReg(m.Base, o) {
				return true
			}
		}
		if in.Pred.IsSet() && SameReg(in.Pred.Reg, o) {
			return true
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

// ---- Labels and scoped declarations ---------------------------------------

// Label creates a fresh label, uniquifying against every name already issued
// in this body.
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

// Local declares a function-scope .local or .shared variable and returns a
// handle usable as an operand.
func (b *Body) Local(v Var) *Var {
	p := &v
	b.Locals = append(b.Locals, p)
	return p
}

// Loc emits a .loc debug directive.
func (b *Body) Loc(f *File, line, col int) *LocDir {
	d := &LocDir{File: f, Line: line, Col: col}
	b.items = append(b.items, d)
	return d
}

// LocInlined emits a .loc directive carrying function_name and inlined_at.
func (b *Body) LocInlined(f *File, line, col int, fn *Label, at *File, atLine, atCol int) *LocDir {
	d := &LocDir{
		File: f, Line: line, Col: col, Func: fn,
		Inlined: &LocSite{File: at, Line: atLine, Col: atCol},
	}
	b.items = append(b.items, d)
	return d
}

// Pragma emits a statement-scope .pragma directive.
func (b *Body) Pragma(s ...string) *Pragma {
	p := &Pragma{Strings: s}
	b.items = append(b.items, p)
	return p
}

// add appends an instruction and returns it.
func (b *Body) add(in *Instr) *Instr {
	b.items = append(b.items, in)
	return in
}