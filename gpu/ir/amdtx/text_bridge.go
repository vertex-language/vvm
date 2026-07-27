// in package amdtx (root), file text_bridge.go
package amdtx

// ResolvedTypeString is the arg's value type as it appears in .amdtx text.
func (a KernelArg) ResolvedTypeString() string { return a.resolvedType().String() }

// Hidden reports whether the arg is an implicit ABI argument.
func (a KernelArg) Hidden() bool { return a.Kind.hidden() }

// ForEachInst iterates a code builder's virtual instructions (printer/lower use).
func (cb *CodeBuilder) ForEachInst(fn func(Inst)) {
	for _, in := range cb.insts {
		fn(in)
	}
}

// Accessors used by the text/lower packages.
func (in Inst) Opcode() Op            { return in.Op }
func (in Inst) DataType() Type        { return in.Type }
func (in Inst) Dests() []Reg          { return in.Dst }
func (in Inst) Sources() []Operand    { return in.Src }
func (in Inst) LabelName() string     { return in.Label }
func (in Inst) Waits() []Waitcnt      { return in.Wait }
func (in Inst) Raw() (string, []byte) { return in.RawText, in.RawData }
func (in Inst) ForcedEnc() Encoding   { return in.forcedEnc }
func (in Inst) Loc() (int, int, int)  { return in.FileIdx, in.Line, in.Col }

// Text renders an operand as .amdtx text (exported for the printer).
func Text(o Operand) string { return o.textForm() }

// RegText renders a register as .amdtx text.
func (r Reg) Text() string { return r.textForm() }