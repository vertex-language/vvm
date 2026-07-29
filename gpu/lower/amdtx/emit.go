// emit.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Region tree to AMDTX items.

func (b *bodyLowerer) emitSeq(o *amdtx.Body, s seq) error {
	for _, n := range s {
		if err := b.emitNode(o, n); err != nil {
			return err
		}
	}
	return nil
}

func (b *bodyLowerer) emitNode(o *amdtx.Body, n node) error {
	switch x := n.(type) {
	case *blockNode:
		for _, in := range x.lines {
			if err := b.instr(o, in); err != nil {
				return fmt.Errorf("%s: %w", in.Op, err)
			}
		}
		return nil

	case *ifNode:
		g, u, err := b.guard(o, x.cond, x.invert)
		if err != nil {
			return err
		}
		st := o.If(g, u)
		if u == amdtx.DivergentGuard {
			b.divergent++
		}
		if err := b.emitSeq(st.Then, x.then); err != nil {
			return err
		}
		if len(x.els) > 0 {
			if err := b.emitSeq(st.ElseBody(), x.els); err != nil {
				return err
			}
		}
		if u == amdtx.DivergentGuard {
			b.divergent--
		}
		return nil

	case *loopNode:
		return b.emitSeq(o.Loop(), x.body)

	case *breakNode:
		if x.cond == (gvir.Operand{}) {
			return fmt.Errorf("an unconditional break survived structurization; AMDTX has only " +
				"breakif (§10.1) and the hoisting peephole did not apply")
		}
		g, u, err := b.guard(o, x.cond, x.invert)
		if err != nil {
			return err
		}
		o.BreakIf(g, u)
		return nil

	case *continueNode:
		g, u, err := b.guard(o, x.cond, x.invert)
		if err != nil {
			return err
		}
		o.ContinueIf(g, u)
		return nil

	case *returnNode:
		return b.emitReturn(o, x)

	case *unreachableNode:
		// §12.6 makes executing one UB; s_trap is the least surprising
		// realization, and the body's terminator still follows it.
		o.Inst("s_trap", nil, amdtx.Imm(2))
		return nil

	case *switchNode:
		return b.emitSwitch(o, x)
	}
	return fmt.Errorf("unhandled region node %T", n)
}

func (b *bodyLowerer) emitReturn(o *amdtx.Body, r *returnNode) error {
	if b.kernel != nil {
		if b.divergent > 0 {
			return fmt.Errorf("early return under divergent control flow: s_endpgm terminates the " +
				"whole wave, and only the `if c { return }` shape is restructured into an enclosing " +
				"guard by this backend")
		}
		o.EndPgm()
		return nil
	}
	if r.val != nil {
		if b.retVal == nil {
			return fmt.Errorf("value returned from a void func")
		}
		src, err := b.srcValue(o, *r.val, b.retVal.typ)
		if err != nil {
			return err
		}
		n := dwordsOf(b.retVal.typ)
		for lane := range b.retVal.regs {
			for d := 0; d < n; d++ {
				o.Inst("v_mov_b32", dword(b.retVal.regs[lane], d, n), dword(src.reg(lane), d, n))
			}
		}
	}
	o.Ret()
	return nil
}

// emitSwitch lowers a switch to a chain of equality selections. A jump table
// has no structured spelling, and the chain is what a sparse case list lowers
// to anyway.
func (b *bodyLowerer) emitSwitch(o *amdtx.Body, sw *switchNode) error {
	body := o
	for _, c := range sw.cases {
		v, err := b.srcValue(body, sw.value, gvir.I32)
		if err != nil {
			return err
		}
		p, err := b.temp(gvir.I1)
		if err != nil {
			return err
		}
		lit, err := b.src(body, gvir.IntLiteral(c.value), gvir.I32, 0)
		if err != nil {
			return err
		}
		body.Inst("v_cmp_eq_u32", p.regs[0], v.reg(0), lit)
		g, u, err := b.guardReg(body, p, sw.value, false)
		if err != nil {
			return err
		}
		st := body.If(g, u)
		if u == amdtx.DivergentGuard {
			b.divergent++
		}
		if err := b.emitSeq(st.Then, c.body); err != nil {
			return err
		}
		if u == amdtx.DivergentGuard {
			b.divergent--
		}
		body = st.ElseBody()
	}
	return b.emitSeq(body, sw.def)
}

// guard materializes a structured guard and its uniformity assertion.
//
// A group-uniform condition becomes an %scc test, which lowers to
// s_cbranch_scc* and leaves any enclosed s_barrier legal (V21). A divergent
// one stays a .lanemask and lowers to EXEC save/and/restore. The assertion is
// written explicitly because an assertion that cannot lie is worth having
// (§10.1).
func (b *bodyLowerer) guard(o *amdtx.Body, cond gvir.Operand, invert bool) (amdtx.Operand, amdtx.Uniformity, error) {
	if cond.Kind == gvir.OperandBool {
		// A constant guard is uniform by construction.
		x := cond.Bool != invert
		if x {
			o.Inst("s_cmp_eq_u32", amdtx.SCC, amdtx.Imm(0), amdtx.Imm(0))
		} else {
			o.Inst("s_cmp_lg_u32", amdtx.SCC, amdtx.Imm(0), amdtx.Imm(0))
		}
		return amdtx.SCC, amdtx.UniformGuard, nil
	}
	if cond.Kind != gvir.OperandIdent {
		return nil, 0, fmt.Errorf("guard operand %s is not an i1 value", cond)
	}
	v, ok := b.lookup(cond.Ident)
	if !ok {
		return nil, 0, fmt.Errorf("use of unbound guard %s (§7.3)", cond.Ident)
	}
	return b.guardReg(o, v, cond, invert)
}

func (b *bodyLowerer) guardReg(o *amdtx.Body, v *value, cond gvir.Operand, invert bool) (amdtx.Operand, amdtx.Uniformity, error) {
	if b.uniformAtGroup(cond) {
		// v_cmp writes zero into inactive lanes, so for a condition that is
		// uniform across the active lanes the mask is non-zero exactly when
		// the condition holds. The test is therefore exact, not conservative.
		op := "s_cmp_lg_" + b.l.maskCmpSuffix()
		if invert {
			op = "s_cmp_eq_" + b.l.maskCmpSuffix()
		}
		o.Inst(op, amdtx.SCC, v.regs[0], amdtx.Imm(0))
		return amdtx.SCC, amdtx.UniformGuard, nil
	}
	if !invert {
		return v.regs[0], amdtx.DivergentGuard, nil
	}
	n, err := b.temp(gvir.I1)
	if err != nil {
		return nil, 0, err
	}
	o.Inst("s_not_"+b.l.maskSuffix(), n.regs[0], v.regs[0])
	return n.regs[0], amdtx.DivergentGuard, nil
}

// instr dispatches one body-line.
func (b *bodyLowerer) instr(o *amdtx.Body, in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpLoc:
		return b.loc(o, in)
	case gvir.OpAlloca, gvir.OpLoad, gvir.OpStore, gvir.OpIndex, gvir.OpField,
		gvir.OpExtract, gvir.OpInsert, gvir.OpSplat, gvir.OpSwizzle,
		gvir.OpMemcopy, gvir.OpMemmove, gvir.OpMemset:
		return b.mem(o, in)
	case gvir.OpBarrier, gvir.OpFence:
		return b.sync(o, in)
	}
	if in.Op.IsAtomic() || in.Op.IsCollective() {
		return b.sync(o, in)
	}
	switch in.Op {
	case gvir.OpMaskCount, gvir.OpMaskTest, gvir.OpMaskFirst, gvir.OpMaskEmpty,
		gvir.OpMaskLt, gvir.OpMaskLe, gvir.OpMaskGt, gvir.OpMaskGe, gvir.OpMaskEq:
		return b.sync(o, in)
	}
	if in.Op.IsBuiltin() {
		return b.builtin(o, in)
	}
	return b.alu(o, in)
}

func (b *bodyLowerer) loc(o *amdtx.Body, in *gvir.Instruction) error {
	if !b.l.opts.Debug {
		return nil
	}
	if len(in.Args) < 2 || in.Args[0].Kind != gvir.OperandString {
		return fmt.Errorf("malformed loc line")
	}
	col := 0
	if len(in.Args) > 2 {
		col = int(in.Args[2].Int)
	}
	o.Loc(b.l.out.File(in.Args[0].Str), int(in.Args[1].Int), col)
	return nil
}