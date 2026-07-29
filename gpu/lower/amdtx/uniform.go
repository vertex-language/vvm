// uniform.go
package amdtx

import "github.com/vertex-language/vvm/ir/gvir"

// The §7.4 uniformity analysis, re-derived over the region tree.
//
// §7.4 is normative and all three backends must agree on its answer, so this
// must produce the classification ir/verify already produced. It is short only
// because the region tree supplies control dependence directly: a name
// assigned inside an `if` whose condition is not uniform at S is not uniform at
// S, and the region tree says exactly which names those are.
//
// It is consulted for one decision — the form of a structured guard — because
// that decision is what keeps s_barrier out of divergent control flow (V21).

type uniformity struct {
	group map[string]bool
	sub   map[string]bool
}

type uniLevel uint8

const (
	uniNever     uniLevel = iota // loads, atomics, shuffles
	uniSubgroup                  // ballot, broadcast, the reductions
	uniGroup                     // the extent builtins and group positions
	uniPropagate                 // uniform iff every operand is
)

func opLevel(op gvir.Opcode) uniLevel {
	switch op {
	case gvir.OpThreadInGrid, gvir.OpThreadInGroup, gvir.OpThreadInSubgroup,
		gvir.OpLoad, gvir.OpAtomicLoad, gvir.OpAtomicAdd, gvir.OpAtomicSub,
		gvir.OpAtomicAnd, gvir.OpAtomicOr, gvir.OpAtomicXor, gvir.OpAtomicXchg,
		gvir.OpAtomicUMin, gvir.OpAtomicUMax, gvir.OpAtomicSMin, gvir.OpAtomicSMax,
		gvir.OpCmpxchg, gvir.OpShuffle, gvir.OpShuffleXor, gvir.OpShuffleUp,
		gvir.OpShuffleDown, gvir.OpMaskLt, gvir.OpMaskLe, gvir.OpMaskGt,
		gvir.OpMaskGe, gvir.OpMaskEq:
		return uniNever

	case gvir.OpSubgroupInGroup, gvir.OpBroadcast, gvir.OpBroadcastFirst,
		gvir.OpBallot, gvir.OpMaskCount, gvir.OpMaskFirst, gvir.OpMaskEmpty,
		gvir.OpAny, gvir.OpAll, gvir.OpSubAdd, gvir.OpSubMin, gvir.OpSubMax,
		gvir.OpSubAnd, gvir.OpSubOr, gvir.OpSubXor:
		return uniSubgroup

	case gvir.OpGroupInGrid, gvir.OpThreadsPerGroup, gvir.OpGroupsPerGrid,
		gvir.OpThreadsPerGrid, gvir.OpThreadsPerSubgroup, gvir.OpSubgroupsPerGroup,
		gvir.OpDynamicGroupSize:
		return uniGroup

	case gvir.OpCall:
		// A call's result depends on the callee's body, which this analysis
		// does not enter. Conservative: never uniform.
		return uniNever
	}
	return uniPropagate
}

// analyze runs the §7.4 analysis to a greatest fixpoint: every assigned name
// starts uniform and rules only ever lower it, which is the right direction
// for loop-carried values.
func (b *bodyLowerer) analyze(root seq) *uniformity {
	u := &uniformity{group: map[string]bool{}, sub: map[string]bool{}}

	// Kernel parameters, consts and group addresses are uniform at group; a
	// func's parameters are not, because argument uniformity is a per-call-site
	// fact this analysis does not carry across the call graph.
	for name := range b.vals {
		uniform := b.kernel != nil
		u.group[name] = uniform
		u.sub[name] = uniform
	}
	collectNames(root, u)

	for i := 0; i < 8; i++ {
		changed := false
		b.walkUniform(root, true, true, u, &changed)
		if !changed {
			return u
		}
	}
	return u
}

func collectNames(s seq, u *uniformity) {
	for _, n := range s {
		switch x := n.(type) {
		case *blockNode:
			for _, in := range x.lines {
				if in.Result != "" {
					if _, ok := u.group[in.Result]; !ok {
						u.group[in.Result] = true
						u.sub[in.Result] = true
					}
				}
			}
		case *ifNode:
			collectNames(x.then, u)
			collectNames(x.els, u)
		case *loopNode:
			collectNames(x.body, u)
		case *switchNode:
			for _, c := range x.cases {
				collectNames(c.body, u)
			}
			collectNames(x.def, u)
		}
	}
}

// walkUniform applies the propagation rule once. ctxGroup and ctxSub record
// whether every enclosing control dependence is uniform at that scope; a name
// assigned under a divergent condition is not uniform even when its operands
// are, which is the load-bearing half of §7.4's propagation rule.
func (b *bodyLowerer) walkUniform(s seq, ctxGroup, ctxSub bool, u *uniformity, changed *bool) {
	for _, n := range s {
		switch x := n.(type) {
		case *blockNode:
			for _, in := range x.lines {
				if in.Result == "" || in.Op == gvir.OpLoc {
					continue
				}
				g, sub := instUniform(in, u)
				b.lower(u.group, in.Result, g && ctxGroup, changed)
				b.lower(u.sub, in.Result, sub && ctxSub, changed)
			}
		case *ifNode:
			cg, cs := operandUniform(x.cond, u)
			b.walkUniform(x.then, ctxGroup && cg, ctxSub && cs, u, changed)
			b.walkUniform(x.els, ctxGroup && cg, ctxSub && cs, u, changed)
		case *switchNode:
			cg, cs := operandUniform(x.value, u)
			for _, c := range x.cases {
				b.walkUniform(c.body, ctxGroup && cg, ctxSub && cs, u, changed)
			}
			b.walkUniform(x.def, ctxGroup && cg, ctxSub && cs, u, changed)
		case *loopNode:
			// A loop whose exit or continue is decided per lane makes its own
			// body control-dependent on that decision.
			bg, bs := loopExitUniform(x.body, u)
			b.walkUniform(x.body, ctxGroup && bg, ctxSub && bs, u, changed)
		}
	}
}

func (b *bodyLowerer) lower(m map[string]bool, name string, val bool, changed *bool) {
	if !val && m[name] {
		m[name] = false
		*changed = true
	}
}

func instUniform(in *gvir.Instruction, u *uniformity) (group, sub bool) {
	switch opLevel(in.Op) {
	case uniNever:
		return false, false
	case uniSubgroup:
		return false, true
	case uniGroup:
		return true, true
	}
	group, sub = true, true
	for _, a := range in.Args {
		g, s := operandUniform(a, u)
		group = group && g
		sub = sub && s
	}
	return group, sub
}

func operandUniform(o gvir.Operand, u *uniformity) (bool, bool) {
	if o.Kind != gvir.OperandIdent {
		return true, true // literals, orderings and scopes are uniform
	}
	g, ok := u.group[o.Ident]
	if !ok {
		return false, false
	}
	return g, u.sub[o.Ident]
}

func loopExitUniform(s seq, u *uniformity) (bool, bool) {
	group, sub := true, true
	for _, n := range s {
		switch x := n.(type) {
		case *breakNode:
			g, sb := operandUniform(x.cond, u)
			group, sub = group && g, sub && sb
		case *continueNode:
			g, sb := operandUniform(x.cond, u)
			group, sub = group && g, sub && sb
		case *ifNode:
			g1, s1 := loopExitUniform(x.then, u)
			g2, s2 := loopExitUniform(x.els, u)
			group, sub = group && g1 && g2, sub && s1 && s2
		}
	}
	return group, sub
}

// uniformAtGroup reports whether a guard operand is uniform at group scope,
// which is the question that decides the guard's form.
func (b *bodyLowerer) uniformAtGroup(o gvir.Operand) bool {
	g, _ := operandUniform(o, b.uni)
	return g
}