// dataflow.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// --- shared set helpers -----------------------------------------------

func cloneSet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for k := range s {
		out[k] = true
	}
	return out
}

func intersect(a, b map[string]bool) {
	for k := range a {
		if !b[k] {
			delete(a, k)
		}
	}
}

func union(a, b map[string]bool) {
	for k := range b {
		a[k] = true
	}
}

func setsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func blockLabel(f *vir.Function, b *vir.Block) string {
	if b == f.Entry {
		return ""
	}
	return b.Label
}

func computePredecessors(f *vir.Function, blocks []*vir.Block) map[string][]string {
	preds := make(map[string][]string)
	for _, b := range blocks {
		from := blockLabel(f, b)
		for _, succ := range vir.Successors(b.Term) {
			preds[succ] = append(preds[succ], from)
		}
	}
	return preds
}

// --- definite assignment (§4.3 rule 3/5, the Join Convention) ----------

// checkDefiniteAssignment is a must-reach forward dataflow: a name is
// "assigned" at a point only if every path from entry assigns it first.
// Non-entry blocks start optimistic (the full universe of names) and
// shrink via intersection with predecessors until a fixpoint — so any
// violation detected mid-iteration is already a real one (current sets
// are always supersets of the true fixpoint), and the final full pass
// with no further change reflects the true fixpoint everywhere.
//
// That "always a superset" invariant requires BOTH inSet and outSet to
// start at the full universe for every non-entry block — not just inSet.
// A block's `in` is computed by intersecting its predecessors' `out`
// sets, and blocks are visited in a fixed order each pass; a back-edge
// predecessor (e.g. a loop body that hasn't been visited yet this pass)
// would still be holding its *initial* outSet value at that point. If
// that initial value were empty rather than the universe, the
// intersection collapses to {} purely from visitation order — not from
// any real unassigned path — which incorrectly rejects legal
// loop-carried values (§4.3 rule 4: "an assignment before the loop plus
// reassignment in the body satisfies rule 3 on both edges"). Starting
// outSet at the universe too keeps every set a true superset of the
// fixpoint at every point in the iteration, which is what makes it sound
// to report a violation the moment one is seen instead of waiting for
// convergence.
//
// Module-scope names (globals, consts — ctx.moduleScope) are outside this
// dataflow entirely: they exist before the function is ever entered
// (§6.2), so a read of one is always legal regardless of path. They are
// deliberately excluded from `universe`/the flow-sensitive sets and
// instead checked as a flat allow-list alongside `assigned` in
// checkReadsAssigned/checkTermReadsAssigned.
func checkDefiniteAssignment(f *vir.Function, blocks []*vir.Block, byLabel map[string]*vir.Block, ctx *fnCtx) error {
	universe := make(map[string]bool)
	for _, p := range f.Params {
		universe[p.Name] = true
	}
	for _, b := range blocks {
		for _, l := range b.Lines {
			if l.Result != "" {
				universe[l.Result] = true
			}
		}
	}

	preds := computePredecessors(f, blocks)
	paramSet := make(map[string]bool, len(f.Params))
	for _, p := range f.Params {
		paramSet[p.Name] = true
	}

	inSet := make(map[string]map[string]bool, len(blocks))
	outSet := make(map[string]map[string]bool, len(blocks))
	for _, b := range blocks {
		l := blockLabel(f, b)
		if b == f.Entry {
			inSet[l] = cloneSet(paramSet)
		} else {
			inSet[l] = cloneSet(universe)
		}
		outSet[l] = cloneSet(universe)
	}

	changed := true
	for changed {
		changed = false
		for _, b := range blocks {
			l := blockLabel(f, b)
			var in map[string]bool
			if b == f.Entry {
				in = inSet[l]
			} else if ps := preds[l]; len(ps) == 0 {
				in = map[string]bool{}
			} else {
				in = cloneSet(universe)
				for _, p := range preds[l] {
					intersect(in, outSet[p])
				}
			}
			if !setsEqual(in, inSet[l]) {
				inSet[l] = in
				changed = true
			}

			out := cloneSet(in)
			for _, ln := range b.Lines {
				if err := checkReadsAssigned(ln, out, ctx.moduleScope); err != nil {
					return fmt.Errorf("block %q: %w", displayLabel(b), err)
				}
				if ln.Result != "" {
					out[ln.Result] = true
				}
			}
			if err := checkTermReadsAssigned(b.Term, out, ctx.moduleScope); err != nil {
				return fmt.Errorf("block %q: %w", displayLabel(b), err)
			}
			if !setsEqual(out, outSet[l]) {
				outSet[l] = out
				changed = true
			}
		}
	}
	_ = byLabel
	return nil
}

// checkReadsAssigned checks one body-line's value-reading operands.
// OpField's struct/field-name idents (args 1,2) and OpCall's callee ident
// (arg 0) are name references, not value reads, and are skipped. A name
// found in moduleScope (global/const, §6.2) is always legal to read,
// independent of the path-sensitive `assigned` set.
func checkReadsAssigned(line *vir.Instruction, assigned map[string]bool, moduleScope map[string]bool) error {
	for idx, a := range line.Args {
		if line.Op == vir.OpField && idx > 0 {
			continue
		}
		if line.Op == vir.OpCall && idx == 0 {
			continue
		}
		if a.Kind != vir.OperandIdent || a.IsQualified() {
			continue
		}
		if assigned[a.Ident] || moduleScope[a.Ident] {
			continue
		}
		return fmt.Errorf("%s reads %q before it's assigned on every path (§4.3 rules 3/5)", line.Op, a.Ident)
	}
	return nil
}

func checkTermReadsAssigned(t vir.Terminator, assigned map[string]bool, moduleScope map[string]bool) error {
	check := func(op vir.Operand) error {
		if op.Kind != vir.OperandIdent || op.IsQualified() {
			return nil
		}
		if assigned[op.Ident] || moduleScope[op.Ident] {
			return nil
		}
		return fmt.Errorf("terminator reads %q before it's assigned on every path (§4.3 rules 3/5)", op.Ident)
	}
	switch x := t.(type) {
	case vir.BranchIf:
		return check(x.Cond)
	case vir.Switch:
		return check(x.Value)
	case vir.Return:
		if x.Value != nil {
			return check(*x.Value)
		}
	case vir.TailCall:
		// An indirect tailcall (Sig != "") carries its callee function
		// pointer in Args[0], the exact same position/shape OpCall uses
		// for its own indirect-call callee — and checkReadsAssigned above
		// already exempts OpCall's idx-0 callee operand from this check
		// (a callee reference, direct or indirect, isn't a "read" in the
		// local-dataflow sense; a bare fn name used this way takes its
		// address, same as a global, never requiring prior local
		// assignment). A direct tailcall never puts its callee in Args at
		// all (it's the separate Callee field), so this carve-out only
		// ever applies to the indirect shape.
		for i, a := range x.Args {
			if x.Sig != "" && i == 0 {
				continue
			}
			if err := check(a); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- valist lifetimes (§4.3 rule 5 addendum, §4.4) ----------------------

// checkValistLifetimes runs two dataflows per valist name over the same
// CFG: mustOpen (must-analysis, intersection — "started on every path,
// not yet closed") backing va_arg/va_end legality, and mayOpen
// (may-analysis, union — "possibly still open on some path") backing the
// re-va_start and open-across-return checks. Both converge monotonically
// (mustOpen down from the full set, mayOpen up from empty), so any
// violation caught mid-iteration is real; the argument is symmetric with
// checkDefiniteAssignment's.
func checkValistLifetimes(f *vir.Function, blocks []*vir.Block, byLabel map[string]*vir.Block, ctx *fnCtx) error {
	valists := make(map[string]bool)
	for _, b := range blocks {
		for _, l := range b.Lines {
			if l.Op == vir.OpAlloca && vir.IsValist(l.Suffix) && l.Result != "" {
				valists[l.Result] = true
			}
		}
	}
	if len(valists) == 0 {
		return nil
	}

	preds := computePredecessors(f, blocks)
	mustIn, mustOut := map[string]map[string]bool{}, map[string]map[string]bool{}
	mayIn, mayOut := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, b := range blocks {
		l := blockLabel(f, b)
		mustOut[l], mayOut[l] = map[string]bool{}, map[string]bool{}
		if b == f.Entry {
			mustIn[l], mayIn[l] = map[string]bool{}, map[string]bool{}
		} else {
			mustIn[l], mayIn[l] = cloneSet(valists), map[string]bool{}
		}
	}

	changed := true
	for changed {
		changed = false
		for _, b := range blocks {
			l := blockLabel(f, b)
			var mIn, yIn map[string]bool
			if b == f.Entry {
				mIn, yIn = map[string]bool{}, map[string]bool{}
			} else if ps := preds[l]; len(ps) == 0 {
				mIn, yIn = map[string]bool{}, map[string]bool{}
			} else {
				mIn, yIn = cloneSet(valists), map[string]bool{}
				for _, p := range preds[l] {
					intersect(mIn, mustOut[p])
					union(yIn, mayOut[p])
				}
			}
			if !setsEqual(mIn, mustIn[l]) {
				mustIn[l] = mIn
				changed = true
			}
			if !setsEqual(yIn, mayIn[l]) {
				mayIn[l] = yIn
				changed = true
			}

			mOut, yOut := cloneSet(mIn), cloneSet(yIn)
			for _, ln := range b.Lines {
				switch ln.Op {
				case vir.OpVaStart:
					name := ln.Args[0].Ident
					if yOut[name] {
						return fmt.Errorf("block %q: va_start on %q without an intervening va_end (§4.3, §4.4)", displayLabel(b), name)
					}
					mOut[name] = true
					yOut[name] = true
				case vir.OpVaArg:
					name := ln.Args[0].Ident
					if !mOut[name] {
						return fmt.Errorf("block %q: va_arg reads %q which isn't va_start-initialized on every path (§4.3, §4.4)", displayLabel(b), name)
					}
				case vir.OpVaEnd:
					name := ln.Args[0].Ident
					if !mOut[name] {
						return fmt.Errorf("block %q: va_end on %q which isn't open on every path (§4.4)", displayLabel(b), name)
					}
					delete(mOut, name)
					delete(yOut, name)
				}
			}
			if _, isReturn := b.Term.(vir.Return); isReturn {
				for name := range yOut {
					return fmt.Errorf("block %q: valist %q left open across return (§4.4)", displayLabel(b), name)
				}
			}
			if tc, isTail := b.Term.(vir.TailCall); isTail && f.Variadic && len(yOut) > 0 {
				if err := checkTailcallValistConflict(tc, ctx); err != nil {
					return fmt.Errorf("block %q: %w", displayLabel(b), err)
				}
			}

			if !setsEqual(mOut, mustOut[l]) {
				mustOut[l] = mOut
				changed = true
			}
			if !setsEqual(yOut, mayOut[l]) {
				mayOut[l] = yOut
				changed = true
			}
		}
	}
	_ = byLabel
	return nil
}

// checkTailcallValistConflict enforces §4.2's tailcall/valist rule: a
// tailcall targeting a variadic fnsig/callee is rejected if the caller
// has an active valist from its own variadic parameter (frame reuse
// would invalidate the still-live save area).
func checkTailcallValistConflict(tc vir.TailCall, ctx *fnCtx) error {
	variadic := false
	if tc.Sig != "" {
		if sig, ok := ctx.fnsigs[tc.Sig]; ok && sig.Variadic {
			variadic = true
		}
	} else if tc.Callee != "" {
		if fn, ok := ctx.fns[tc.Callee]; ok && fn.Variadic {
			variadic = true
		} else if ef, ok := ctx.externs[tc.Callee]; ok && ef.Variadic {
			variadic = true
		}
	}
	if variadic {
		return fmt.Errorf("tailcall to a variadic callee with an open caller valist is illegal (§4.2, §4.4)")
	}
	return nil
}