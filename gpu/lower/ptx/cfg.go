package ptx

import (
	ptx "github.com/vertex-language/vvm/gpu/ir/ptx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Block emission and terminators.
//
// Merge annotations (§7.2) are dropped: they exist because two of the three
// backends require structured control flow, and PTX is not one of them. A
// reducible annotated CFG is already a legal PTX branch graph.

func (f *fn) lowerBody(body *gvir.Body) error {
	// Labels are created up front so a forward branch resolves without a
	// fixup pass — ptx.Label is symbolic and carries no offset.
	for _, blk := range body.Blocks {
		f.labels[blk.Label] = f.b.Label(blk.Label)
	}

	if body.Entry != nil {
		if err := f.lowerBlock(body.Entry); err != nil {
			return err
		}
	}
	for _, blk := range body.Blocks {
		f.b.Bind(f.labels[blk.Label])
		if err := f.lowerBlock(blk); err != nil {
			return err
		}
	}
	return nil
}

func (f *fn) lowerBlock(blk *gvir.Block) error {
	for _, in := range blk.Lines {
		if err := f.instr(in); err != nil {
			return f.errAt(in, err)
		}
	}
	if blk.Merge != nil && f.opts.Comments {
		f.noteMerge(blk)
	}
	return f.terminator(blk.Term)
}

func (f *fn) noteMerge(blk *gvir.Block) {
	msg := "merge " + blk.Merge.Merge
	if blk.Merge.Kind == gvir.MergeLoop {
		msg = "loop_merge " + blk.Merge.Merge + ", " + blk.Merge.Continue
	}
	if n := f.b.Len(); n > 0 {
		if in := f.b.InstrAt(n - 1); in != nil {
			in.Note(msg)
		}
	}
}

func (f *fn) label(name string) *ptx.Label { return f.labels[name] }

func (f *fn) terminator(t gvir.Terminator) error {
	switch x := t.(type) {
	case nil:
		return todof("block has no terminator (§7.1)")

	case gvir.Br:
		if f.label(x.Label) == nil {
			return todof("branch to unknown label %q", x.Label)
		}
		f.b.Bra(f.label(x.Label))
		return nil

	case gvir.BrIf:
		p, err := f.pred(x.Cond)
		if err != nil {
			return err
		}
		if f.label(x.Then) == nil || f.label(x.Else) == nil {
			return todof("br_if to unknown label")
		}
		// The guard is attached to the branch instruction itself, so it cannot
		// leak across the label that follows.
		f.b.Bra(f.label(x.Then)).If(p)
		f.b.Bra(f.label(x.Else))
		return nil

	case gvir.Switch:
		return f.lowerSwitch(x)

	case gvir.Return:
		return f.lowerReturn(x)

	case gvir.Unreachable:
		// §12.6: executing one is UB. trap is the least surprising realization.
		f.b.Trap()
		return nil
	}
	return todof("unknown terminator %T", t)
}

// lowerSwitch emits a compare chain. brx.idx wants a dense index and a
// .branchtargets table; the chain is correct for every case list §2 admits.
func (f *fn) lowerSwitch(s gvir.Switch) error {
	vt, ok := f.typeOf(s.Value)
	if !ok {
		return todof("switch operand %s has no bound type", s.Value)
	}
	it, err := intType(vt, false)
	if err != nil {
		return err
	}
	v, err := f.scalar(s.Value, vt)
	if err != nil {
		return err
	}
	for _, c := range s.Cases {
		l := f.label(c.Label)
		if l == nil {
			return todof("switch case branches to unknown label %q", c.Label)
		}
		p := f.tempReg(ptx.Pred)
		f.b.Setp(it, ptx.Eq, p, v, ptx.Imm(c.Value))
		f.b.Bra(l).If(p)
	}
	def := f.label(s.Default)
	if def == nil {
		return todof("switch default branches to unknown label %q", s.Default)
	}
	f.b.Bra(def)
	return nil
}

func (f *fn) lowerReturn(r gvir.Return) error {
	if r.Value == nil {
		// Kernels implicitly return void and `return` takes no operand (§6.1).
		f.b.Ret()
		return nil
	}
	if len(f.retPar) == 0 {
		return todof("value returned from a void callable")
	}
	ops, err := f.lanes(*r.Value, f.retType)
	if err != nil {
		return err
	}
	mt, err := memType(gvir.ElemOrSelf(f.retType))
	if err != nil {
		return err
	}
	for lane, par := range f.retPar {
		f.b.St(mt, ptx.At(par), ops[lane], ptx.ParamSpace)
	}
	f.b.Ret()
	return nil
}