// cfg.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// The structurizer. §7.2 exists because two of the three backends require
// structured control flow; MSL is the strictest of them — it is C++, so there
// is no goto and no label to fall back on — and the merge annotations are the
// whole input here.
//
//   loop_merge Lexit, Lcontinue   ->  while (true) { ... }
//   merge L on a br_if            ->  if (c) { ... } else { ... }
//   merge L on a switch           ->  switch (t) { case ...: }
//   branch to Lexit               ->  break
//   branch to the header          ->  continue
//
// A construction that does not fit is a lowering error naming the block, never
// something approximate.

type loopCtx struct {
	header, exit, cont string
}

// region is the emission context: the labels that end the current region (the
// enclosing merge points), the innermost loop, and whether a `break` here
// would be captured by a switch instead of that loop.
type region struct {
	stops    map[string]bool
	loop     *loopCtx
	inSwitch bool
}

func (r region) with(labels ...string) region {
	out := region{stops: map[string]bool{}, loop: r.loop, inSwitch: r.inSwitch}
	for k := range r.stops {
		out.stops[k] = true
	}
	for _, l := range labels {
		if l != "" {
			out.stops[l] = true
		}
	}
	return out
}

func (f *fnLower) emitBody(b *msl.Block, src *gvir.Body) error {
	for _, blk := range src.Blocks {
		if _, dup := f.blocks[blk.Label]; dup {
			return fmt.Errorf("duplicate label %s", blk.Label)
		}
		f.blocks[blk.Label] = blk
	}
	if src.Entry == nil {
		return fmt.Errorf("body has no entry block (§7.1)")
	}
	// The entry block cannot be branched to (§7.1), so it is emitted directly
	// and never enters the visited/stop machinery.
	return f.emitBlock(b, src.Entry, region{stops: map[string]bool{}})
}

// emitLabel resolves a branch target: an enclosing region's merge point, a
// loop edge, or a block to emit inline here.
func (f *fnLower) emitLabel(b *msl.Block, label string, r region) error {
	if r.stops[label] {
		return nil // control falls out of this region to the caller's merge
	}
	if lc := r.loop; lc != nil {
		switch label {
		case lc.exit:
			if r.inSwitch {
				return fmt.Errorf("branch to loop exit %s from inside a switch: `break` would leave the switch, not the loop", label)
			}
			b.Break()
			return nil
		case lc.header:
			b.Continue()
			return nil
		case lc.cont:
			// A separate continue block is tail-duplicated at each edge that
			// reaches it: MSL's `continue` jumps past it, so emitting it here
			// is the only way the latch runs on every iteration.
			latch, ok := f.blocks[label]
			if !ok {
				return fmt.Errorf("undefined continue target %s", label)
			}
			if latch.Merge != nil {
				return fmt.Errorf("continue block %s carries its own merge annotation; only a straight-line latch is duplicable", label)
			}
			if err := f.lines(b, latch); err != nil {
				return err
			}
			br, ok := latch.Term.(gvir.Br)
			if !ok || br.Label != lc.header {
				return fmt.Errorf("continue block %s does not end in a back-edge to %s", label, lc.header)
			}
			b.Continue()
			return nil
		}
	}
	blk, ok := f.blocks[label]
	if !ok {
		return fmt.Errorf("undefined label %s", label)
	}
	if f.visited[label] {
		return fmt.Errorf("block %s is reached from more than one region: the CFG is not structurable as written (§7.2)", label)
	}
	f.visited[label] = true
	return f.emitBlock(b, blk, r)
}

func (f *fnLower) emitBlock(b *msl.Block, blk *gvir.Block, r region) error {
	if blk.Merge != nil && blk.Merge.Kind == gvir.MergeLoop {
		return f.emitLoop(b, blk, r)
	}
	if err := f.lines(b, blk); err != nil {
		return err
	}
	if blk.Merge != nil {
		if err := f.emitSelection(b, blk, r); err != nil {
			return err
		}
		return f.emitLabel(b, blk.Merge.Merge, r)
	}
	return f.emitTerm(b, blk, r)
}

func (f *fnLower) lines(b *msl.Block, blk *gvir.Block) error {
	for _, in := range blk.Lines {
		if err := f.instruction(b, in); err != nil {
			where := blk.Label
			if where == "" {
				where = "entry"
			}
			return fmt.Errorf("block %s: %s: %w", where, in.Op, err)
		}
	}
	return nil
}

// emitLoop opens a cycle. The header's own lines run at the top of every
// iteration, and the header's terminator is dispatched inside the loop, so a
// header that tests and exits becomes `if (c) { body } else { break; }` with
// no extra structure.
func (f *fnLower) emitLoop(b *msl.Block, blk *gvir.Block, outer region) error {
	m := blk.Merge
	lc := &loopCtx{header: blk.Label, exit: m.Merge, cont: m.Continue}
	if _, ok := f.blocks[lc.exit]; !ok {
		return fmt.Errorf("loop %s exits to undefined label %s", blk.Label, lc.exit)
	}

	// Inside the loop the enclosing merge points are unreachable: a branch out
	// of a cycle goes to its declared exit (§7.2), so the stop set is cleared
	// and a stray edge becomes an explicit error rather than a fallthrough
	// that would spin.
	inner := region{stops: map[string]bool{}, loop: lc}
	if lc.cont != "" && lc.cont != lc.header {
		f.visited[lc.cont] = true // reached only by duplication
	}

	var loopErr error
	b.While(msl.B(true), func(wb *msl.Block) {
		if err := f.lines(wb, blk); err != nil {
			loopErr = err
			return
		}
		if blk.Merge != nil && len(gvir.Successors(blk.Term)) > 1 {
			// A loop header may also head a selection; its merge point is the
			// continue target, which the loop context already handles.
			if err := f.emitSelection(wb, blk, inner); err != nil {
				loopErr = err
			}
			return
		}
		if err := f.emitTerm(wb, blk, inner); err != nil {
			loopErr = err
		}
	})
	if loopErr != nil {
		return fmt.Errorf("loop %s: %w", blk.Label, loopErr)
	}
	return f.emitLabel(b, lc.exit, outer)
}

// emitSelection emits the if or switch a `merge L` annotation heads. Both arms
// stop at L, which the caller then emits once.
func (f *fnLower) emitSelection(b *msl.Block, blk *gvir.Block, r region) error {
	stop := blk.Merge.Merge
	arm := r.with(stop)

	switch t := blk.Term.(type) {
	case gvir.BrIf:
		cond, err := f.operand(t.Cond, msl.Bool)
		if err != nil {
			return err
		}
		var inner error
		if t.Then == stop {
			// Only the else arm has a body; invert rather than emit an empty
			// then-branch.
			b.If(cond.Not(), func(tb *msl.Block) {
				inner = f.emitLabel(tb, t.Else, arm)
			})
			return inner
		}
		s := b.If(cond, func(tb *msl.Block) {
			inner = f.emitLabel(tb, t.Then, arm)
		})
		if inner != nil {
			return inner
		}
		if t.Else != stop {
			s.Else(func(eb *msl.Block) {
				inner = f.emitLabel(eb, t.Else, arm)
			})
		}
		return inner

	case gvir.Switch:
		tagB, err := f.switchTagType(t.Value)
		if err != nil {
			return err
		}
		tag, err := f.operand(t.Value, tagB)
		if err != nil {
			return err
		}
		arm.inSwitch = true
		sw := b.Switch(tag)

		// Case labels that share a target become one arm with several labels,
		// which is how the printer spells them and how C++ reads them.
		var order []string
		grouped := map[string][]msl.Expr{}
		for _, c := range t.Cases {
			if _, ok := grouped[c.Label]; !ok {
				order = append(order, c.Label)
			}
			grouped[c.Label] = append(grouped[c.Label], msl.Cast(tagB, msl.I(c.Value)))
		}
		var inner error
		for _, label := range order {
			target := label
			sw.Case(grouped[target], func(cb *msl.Block) {
				if inner == nil {
					inner = f.emitLabel(cb, target, arm)
				}
			})
		}
		if inner != nil {
			return inner
		}
		if t.Default != stop {
			sw.Default(func(db *msl.Block) {
				inner = f.emitLabel(db, t.Default, arm)
			})
		}
		return inner
	}
	return fmt.Errorf("block %s carries a merge annotation but ends in %T", blk.Label, blk.Term)
}

func (f *fnLower) switchTagType(o gvir.Operand) (msl.Type, error) {
	if o.Kind == gvir.OperandIdent {
		if b, ok := f.vals.lookup(o.Ident); ok {
			return b.typ, nil
		}
	}
	return msl.Int, nil
}

func (f *fnLower) emitTerm(b *msl.Block, blk *gvir.Block, r region) error {
	switch t := blk.Term.(type) {
	case gvir.Br:
		return f.emitLabel(b, t.Label, r)

	case gvir.BrIf:
		if t.Then == t.Else {
			// One distinct successor: §7.2 requires no annotation and this is
			// an unconditional edge.
			return f.emitLabel(b, t.Then, r)
		}
		return fmt.Errorf("block %s branches two ways without a merge annotation (§7.2)", blk.Label)

	case gvir.Switch:
		if len(gvir.Successors(t)) == 1 {
			return f.emitLabel(b, t.Default, r)
		}
		return fmt.Errorf("block %s switches without a merge annotation (§7.2)", blk.Label)

	case gvir.Return:
		if t.Value == nil {
			b.Return()
			return nil
		}
		e, err := f.operand(*t.Value, f.fn.Ret)
		if err != nil {
			return err
		}
		b.Return(e)
		return nil

	case gvir.Unreachable:
		// §12.6 makes executing one UB, so any realization conforms; returning
		// is the least surprising one and keeps the function well-formed.
		if f.l.opt.Comments {
			b.Comment("unreachable (§12.6)")
		}
		if f.fn.Ret == nil {
			b.Return()
		} else {
			b.Return(msl.Ctor(f.fn.Ret))
		}
		return nil
	}
	return fmt.Errorf("block %s has no terminator (§7.1)", blk.Label)
}