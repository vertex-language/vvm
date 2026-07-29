// cfg.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/gvir"
)

// The structurizer.
//
// §7.2 exists because two of the three backends require structured control
// flow, and AMDTX is one of them: bodies use either structured regions or
// explicit labels, never both (V26), and structured regions are the preferred
// form because EXEC-mask expansion belongs to the lowering pipeline (P2).
//
// The input is therefore exactly right: a reducible CFG in which every
// divergent construct already declares its reconvergence point. Rebuilding
// regions from it is a walk, not an analysis.

type node interface{ node() }

type seq []node

// blockNode is a run of straight-line body-lines. Terminators never appear
// here: they became structure.
type blockNode struct{ lines []*gvir.Instruction }

type ifNode struct {
	cond   gvir.Operand
	invert bool
	then   seq
	els    seq
}

type loopNode struct{ body seq }

type breakNode struct {
	cond   gvir.Operand // nil means unconditional
	invert bool
}

type continueNode struct {
	cond   gvir.Operand
	invert bool
}

type returnNode struct{ val *gvir.Operand }

type unreachableNode struct{}

type switchCase struct {
	value int64
	body  seq
}

type switchNode struct {
	value gvir.Operand
	cases []switchCase
	def   seq
}

func (*blockNode) node()       {}
func (*ifNode) node()          {}
func (*loopNode) node()        {}
func (*breakNode) node()       {}
func (*continueNode) node()    {}
func (*returnNode) node()      {}
func (*unreachableNode) node() {}
func (*switchNode) node()      {}

type loopCtx struct {
	header string
	exit   string
	cont   string
	// contTrivial records whether the continue block is empty and branches
	// straight back to the header, which is what makes an early `continueif`
	// safe: a continue that skipped latch code would be wrong.
	contTrivial bool
}

type structurizer struct {
	body *gvir.Body
}

func structurize(body *gvir.Body) (seq, error) {
	s := &structurizer{body: body}
	if body.Entry == nil {
		return nil, fmt.Errorf("body has no entry block (§7.1)")
	}
	return s.build(body.Entry, "", nil)
}

func (s *structurizer) block(label string) (*gvir.Block, error) {
	blk := s.body.BlockByLabel(label)
	if blk == nil {
		return nil, fmt.Errorf("branch to undefined label %s", label)
	}
	return blk, nil
}

// build walks the linear chain starting at blk, stopping when it reaches the
// block labelled stop or when the chain terminates.
func (s *structurizer) build(blk *gvir.Block, stop string, loops []loopCtx) (seq, error) {
	var out seq
	for blk != nil {
		if blk.Label != "" && blk.Label == stop {
			return out, nil
		}
		// A cycle entry opens a loop, unless we are already building its body.
		if blk.Merge != nil && blk.Merge.Kind == gvir.MergeLoop && !inLoopHeader(loops, blk.Label) {
			body, ctx, err := s.buildLoop(blk, loops)
			if err != nil {
				return nil, err
			}
			out = append(out, &loopNode{body: body})
			next, done, err := s.follow(ctx.exit, stop, loops, &out)
			if err != nil || done {
				return out, err
			}
			blk = next
			continue
		}

		if len(blk.Lines) > 0 {
			out = append(out, &blockNode{lines: blk.Lines})
		}

		switch t := blk.Term.(type) {
		case gvir.Br:
			next, done, err := s.follow(t.Label, stop, loops, &out)
			if err != nil || done {
				return out, err
			}
			blk = next

		case gvir.BrIf:
			if t.Then == t.Else {
				next, done, err := s.follow(t.Then, stop, loops, &out)
				if err != nil || done {
					return out, err
				}
				blk = next
				continue
			}
			// A loop header's second successor is its exit: that is a
			// conditional break, not a selection, and it has no `merge`
			// annotation because §7.2 gives the header `loop_merge` instead.
			if blk.Merge != nil && blk.Merge.Kind == gvir.MergeLoop {
				ctx := loops[len(loops)-1]
				switch {
				case t.Else == ctx.exit:
					out = append(out, &breakNode{cond: t.Cond, invert: true})
					next, err := s.block(t.Then)
					if err != nil {
						return nil, err
					}
					blk = next
				case t.Then == ctx.exit:
					out = append(out, &breakNode{cond: t.Cond})
					next, err := s.block(t.Else)
					if err != nil {
						return nil, err
					}
					blk = next
				default:
					return nil, fmt.Errorf("loop header %s branches to neither its exit nor a single "+
						"body block; a selection in a loop header carries no merge annotation and is "+
						"not structurizable", blk.Label)
				}
				continue
			}
			if blk.Merge == nil {
				return nil, fmt.Errorf("block %s has two distinct successors and no merge annotation (§7.2)", blk.Label)
			}
			rest, err := s.selection(blk, t, stop, loops)
			if err != nil {
				return nil, err
			}
			out = append(out, rest...)
			return out, nil

		case gvir.Switch:
			if blk.Merge == nil && len(gvir.Successors(t)) > 1 {
				return nil, fmt.Errorf("block %s has a multi-successor switch and no merge annotation (§7.2)", blk.Label)
			}
			n, err := s.switchNode(blk, t, loops)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
			if blk.Merge == nil {
				return out, nil
			}
			next, done, err := s.follow(blk.Merge.Merge, stop, loops, &out)
			if err != nil || done {
				return out, err
			}
			blk = next

		case gvir.Return:
			out = append(out, &returnNode{val: t.Value})
			return out, nil

		case gvir.Unreachable:
			out = append(out, &unreachableNode{})
			return out, nil

		default:
			return nil, fmt.Errorf("block %s has no terminator (§7.1)", blk.Label)
		}
	}
	return out, nil
}

// follow classifies a branch target: the enclosing region's stop label, a
// break, a loop back-edge, a continue, or plain linear flow.
func (s *structurizer) follow(target, stop string, loops []loopCtx, out *seq) (*gvir.Block, bool, error) {
	if target == stop {
		return nil, true, nil
	}
	if len(loops) > 0 {
		ctx := loops[len(loops)-1]
		switch target {
		case ctx.exit:
			*out = append(*out, &breakNode{})
			return nil, true, nil
		case ctx.header:
			return nil, true, nil // the back edge is the end of the loop body
		case ctx.cont:
			if ctx.contTrivial {
				return nil, true, nil
			}
			blk, err := s.block(target)
			return blk, false, err
		}
		for _, outer := range loops[:len(loops)-1] {
			if target == outer.exit || target == outer.cont {
				return nil, false, fmt.Errorf("branch to %s leaves more than one enclosing loop; "+
					"AMDTX breakif and continueif act on the innermost loop only (§10.1)", target)
			}
		}
	}
	blk, err := s.block(target)
	return blk, false, err
}

func (s *structurizer) buildLoop(header *gvir.Block, loops []loopCtx) (seq, loopCtx, error) {
	m := header.Merge
	ctx := loopCtx{header: header.Label, exit: m.Merge, cont: m.Continue}
	if cont := s.body.BlockByLabel(m.Continue); cont != nil {
		if br, ok := cont.Term.(gvir.Br); ok && len(cont.Lines) == 0 && br.Label == header.Label {
			ctx.contTrivial = true
		}
	}
	body, err := s.build(header, "", append(loops, ctx))
	return body, ctx, err
}

// selection builds an if region and the continuation that follows its merge
// block, applying the two peepholes that make early exits expressible.
func (s *structurizer) selection(blk *gvir.Block, t gvir.BrIf, stop string, loops []loopCtx) (seq, error) {
	merge := blk.Merge.Merge
	then, err := s.arm(t.Then, merge, loops)
	if err != nil {
		return nil, err
	}
	els, err := s.arm(t.Else, merge, loops)
	if err != nil {
		return nil, err
	}

	// `if c { break } rest` -> `breakif c; rest`. AMDTX has no unconditional
	// break, and hoisting the condition is the form its structurizer expects.
	if isSoleBreak(then) {
		out := seq{&breakNode{cond: t.Cond}}
		out = append(out, els...)
		tail, err := s.continuation(merge, stop, loops)
		if err != nil {
			return nil, err
		}
		return append(out, tail...), nil
	}
	if isSoleBreak(els) {
		out := seq{&breakNode{cond: t.Cond, invert: true}}
		out = append(out, then...)
		tail, err := s.continuation(merge, stop, loops)
		if err != nil {
			return nil, err
		}
		return append(out, tail...), nil
	}

	tail, err := s.continuation(merge, stop, loops)
	if err != nil {
		return nil, err
	}

	// `if c { return } else { X } rest` -> `if !c { X; rest }`. The
	// bounds-check early return is usually divergent, and s_endpgm terminates
	// the whole wave, so the continuation has to move inside the surviving
	// arm rather than the return moving under a guard.
	if isSoleReturn(then) {
		return seq{&ifNode{cond: t.Cond, invert: true, then: append(els, tail...)}}, nil
	}
	if isSoleReturn(els) {
		return seq{&ifNode{cond: t.Cond, then: append(then, tail...)}}, nil
	}

	out := seq{&ifNode{cond: t.Cond, then: then, els: els}}
	return append(out, tail...), nil
}

func (s *structurizer) arm(label, merge string, loops []loopCtx) (seq, error) {
	if label == merge {
		return nil, nil
	}
	blk, err := s.block(label)
	if err != nil {
		return nil, err
	}
	return s.build(blk, merge, loops)
}

func (s *structurizer) continuation(merge, stop string, loops []loopCtx) (seq, error) {
	if merge == stop {
		return nil, nil
	}
	var tail seq
	next, done, err := s.follow(merge, stop, loops, &tail)
	if err != nil || done {
		return tail, err
	}
	rest, err := s.build(next, stop, loops)
	if err != nil {
		return nil, err
	}
	return append(tail, rest...), nil
}

func (s *structurizer) switchNode(blk *gvir.Block, t gvir.Switch, loops []loopCtx) (node, error) {
	merge := ""
	if blk.Merge != nil {
		merge = blk.Merge.Merge
	}
	n := &switchNode{value: t.Value}
	for _, c := range t.Cases {
		body, err := s.arm(c.Label, merge, loops)
		if err != nil {
			return nil, err
		}
		n.cases = append(n.cases, switchCase{value: c.Value, body: body})
	}
	def, err := s.arm(t.Default, merge, loops)
	if err != nil {
		return nil, err
	}
	n.def = def
	return n, nil
}

func inLoopHeader(loops []loopCtx, label string) bool {
	return len(loops) > 0 && loops[len(loops)-1].header == label
}

func isSoleBreak(s seq) bool {
	if len(s) != 1 {
		return false
	}
	br, ok := s[0].(*breakNode)
	return ok && br.cond == (gvir.Operand{})
}

func isSoleReturn(s seq) bool {
	if len(s) != 1 {
		return false
	}
	_, ok := s[0].(*returnNode)
	return ok
}