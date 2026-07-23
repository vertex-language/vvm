// body.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// checkFunctionBody validates one function's §4 body shape and dataflow:
// block termination, label resolution, per-instruction arity/type/result
// checks, noreturn call sites, definite assignment, and valist lifetimes.
func checkFunctionBody(f *vir.Function, ctx *fnCtx) error {
	if f.Entry == nil {
		return fmt.Errorf("function has no entry block")
	}
	blocks := f.AllBlocks()

	byLabel := make(map[string]*vir.Block, len(f.Blocks))
	for _, b := range f.Blocks {
		if b.Label == "" {
			return fmt.Errorf("labeled block has an empty label")
		}
		if _, dup := byLabel[b.Label]; dup {
			return fmt.Errorf("label %q declared twice (labels are function-scoped, §4.3)", b.Label)
		}
		byLabel[b.Label] = b
	}

	types := make(map[string]vir.Type, len(f.Params)+8)
	for _, p := range f.Params {
		types[p.Name] = p.Type
	}

	for _, b := range blocks {
		if b.Term == nil {
			return fmt.Errorf("block %q does not terminate (§4.3)", displayLabel(b))
		}
		for _, lbl := range vir.Successors(b.Term) {
			if _, ok := byLabel[lbl]; !ok {
				return fmt.Errorf("block %q: terminator targets undefined label %q", displayLabel(b), lbl)
			}
		}
		for i, line := range b.Lines {
			if err := checkInstruction(line, ctx, types); err != nil {
				return fmt.Errorf("block %q line %d: %w", displayLabel(b), i, err)
			}
		}
		if err := checkTerminatorShape(f, b.Term, ctx); err != nil {
			return fmt.Errorf("block %q: %w", displayLabel(b), err)
		}
		if err := checkNoreturnCallSites(b, ctx); err != nil {
			return fmt.Errorf("block %q: %w", displayLabel(b), err)
		}
	}

	if err := checkDefiniteAssignment(f, blocks, byLabel, ctx); err != nil {
		return err
	}
	if err := checkValistLifetimes(f, blocks, byLabel, ctx); err != nil {
		return err
	}
	return nil
}

func displayLabel(b *vir.Block) string {
	if b.Label == "" {
		return "<entry>"
	}
	return b.Label
}

// checkInstruction checks one body-line's arity, numeric constraint, and
// result-type/binding rules, fixing the result's type on first assignment
// (§4.3 rule 2) or rejecting a conflicting later one.
func checkInstruction(i *vir.Instruction, ctx *fnCtx, types map[string]vir.Type) error {
	if i.Op == vir.OpInvalid {
		return fmt.Errorf("instruction has invalid opcode")
	}
	info, ok := opInfoTable[i.Op]
	if !ok {
		return fmt.Errorf("opcode %s: no verify metadata registered", i.Op)
	}
	if info.arity >= 0 && len(i.Args) != info.arity {
		return fmt.Errorf("%s: expects %d operand(s), got %d", i.Op, info.arity, len(i.Args))
	}
	if !numericConstraintOK(i.Suffix, info.num) {
		return fmt.Errorf("%s legal only on %s (§4/§9.18)", i.Op, constraintDesc(info.num))
	}

	rt, err := resultType(i, ctx)
	if err != nil {
		return err
	}

	if i.Result == "" {
		switch info.result {
		case rSuffix, rBool:
			return fmt.Errorf("%s: produces a value and must bind a result name", i.Op)
		case rSpecial:
			if i.Op != vir.OpCall && i.Op != vir.OpSyscall {
				return fmt.Errorf("%s: produces a value and must bind a result name", i.Op)
			}
		}
		return nil
	}
	if info.result == rVoid {
		return fmt.Errorf("%s: never produces a value, must not bind a result name", i.Op)
	}
	if rt == nil {
		return nil // type not statically knowable here (e.g. cross-module call); importer's job
	}
	if prev, ok := types[i.Result]; ok {
		if !vir.Equal(prev, rt) {
			return fmt.Errorf("%q: type fixed to %s at first assignment, conflicting reassignment as %s (§4.3 rule 2)", i.Result, prev, rt)
		}
	} else {
		types[i.Result] = rt
	}
	return nil
}

func constraintDesc(c numConstraint) string {
	switch c {
	case cInt:
		return "iN / vec[iN, W]"
	case cFloat:
		return "fN / vec[fN, W]"
	case cIntOrFloat:
		return "iN or fN (incl. vector forms)"
	case cIntOrPtr:
		return "iN / vec[iN, W] or ptr"
	}
	return "a compatible type"
}

func resultType(i *vir.Instruction, ctx *fnCtx) (vir.Type, error) {
	info := opInfoTable[i.Op]
	switch info.result {
	case rVoid:
		return nil, nil
	case rSuffix:
		if i.Suffix == nil {
			return nil, fmt.Errorf("%s: requires a type suffix", i.Op)
		}
		return i.Suffix, nil
	case rBool:
		if v, ok := i.Suffix.(vir.VecType); ok {
			return vir.VecType{Elem: vir.I1, Len: v.Len}, nil
		}
		return vir.I1, nil
	case rSpecial:
		return resultTypeSpecial(i, ctx)
	}
	return nil, fmt.Errorf("%s: unrecognized result rule", i.Op)
}

func resultTypeSpecial(i *vir.Instruction, ctx *fnCtx) (vir.Type, error) {
	switch i.Op {
	case vir.OpMin, vir.OpMax:
		elem := vir.ElemOrSelf(i.Suffix)
		if vir.IsInt(elem) {
			return nil, fmt.Errorf("%s: illegal on integers (§9.17) — use smin/smax/umin/umax", i.Op)
		}
		if !vir.IsFloat(elem) {
			return nil, fmt.Errorf("%s: requires a float (or vector-of-float) type", i.Op)
		}
		return i.Suffix, nil

	case vir.OpAlloca:
		// Arity is variant-dependent (opinfo.go registers OpAlloca's arity
		// as -1, "checked elsewhere", specifically so this is the one
		// place that check happens): alloca.ptr takes exactly one operand
		// (the size), while alloca.valist takes none at all — its layout
		// is target-defined, not something a frontend sizes (README §4.4:
		// "No size/align operand ... its layout is target-defined and not
		// something a frontend sizes"). AllocaValist's own builder method
		// (builder.go) never appends an Args entry, precisely matching
		// this.
		if vir.IsPtr(i.Suffix) {
			if len(i.Args) != 1 {
				return nil, fmt.Errorf("alloca.ptr: expects 1 operand (size), got %d", len(i.Args))
			}
			return vir.Ptr, nil
		}
		if vir.IsValist(i.Suffix) {
			if len(i.Args) != 0 {
				return nil, fmt.Errorf("alloca.valist: expects 0 operands (target-defined layout, no size), got %d", len(i.Args))
			}
			return vir.Valist, nil
		}
		return nil, fmt.Errorf("alloca: suffix must be .ptr or .valist")

	case vir.OpExtract:
		if len(i.Args) < 2 {
			return nil, fmt.Errorf("extract: expects a vector operand and an index")
		}
		if i.Suffix == nil {
			return nil, fmt.Errorf("extract: requires the element type as a suffix")
		}
		return i.Suffix, nil

	case vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax, vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		if i.Suffix == nil {
			return nil, fmt.Errorf("%s: requires a scalar result type suffix", i.Op)
		}
		return i.Suffix, nil

	case vir.OpCall:
		if len(i.Args) < 1 {
			return nil, fmt.Errorf("call: expects a callee operand")
		}
		callee := i.Args[0]
		if callee.Kind != vir.OperandIdent {
			return nil, fmt.Errorf("call: first operand must be a callee identifier")
		}
		if callee.IsQualified() {
			return nil, nil // cross-module callee: importer's job (verify.md)
		}
		if fn, ok := ctx.fns[callee.Ident]; ok {
			return fn.Ret, nil
		}
		if ef, ok := ctx.externs[callee.Ident]; ok {
			return ef.Ret, nil
		}
		return nil, fmt.Errorf("call: undeclared callee %q (§2.2 declare-before-use)", callee.Ident)

	case vir.OpSyscall:
		if len(i.Args) < 1 || len(i.Args) > 7 {
			return nil, fmt.Errorf("syscall: expects 1-7 operands (sysno + up to six args, §4.2)")
		}
		if i.Suffix == nil {
			return nil, fmt.Errorf("syscall: requires a result type suffix")
		}
		return i.Suffix, nil
	}
	return nil, fmt.Errorf("%s: no special-case result rule implemented", i.Op)
}

// checkTerminatorShape checks operand presence/void-agreement for
// terminators, and applies tailcall's return-type/byval-sret rule (§4.2).
func checkTerminatorShape(f *vir.Function, term vir.Terminator, ctx *fnCtx) error {
	switch t := term.(type) {
	case vir.Branch, vir.Trap, vir.Unreachable:
		return nil
	case vir.BranchIf:
		if t.Then == "" || t.Else == "" {
			return fmt.Errorf("br_if: both labels are required")
		}
		return nil
	case vir.Switch:
		if t.Default == "" {
			return fmt.Errorf("switch: default label is required")
		}
		return nil
	case vir.Return:
		if t.Value == nil {
			if !vir.IsVoid(f.Ret) {
				return fmt.Errorf("return: function returns %s, but no value given", f.Ret)
			}
			return nil
		}
		if vir.IsVoid(f.Ret) {
			return fmt.Errorf("return: function is void, must not return a value")
		}
		return nil
	case vir.TailCall:
		return checkTailcallTarget(f, t, ctx)
	default:
		return fmt.Errorf("unrecognized terminator %T", t)
	}
}

// checkTailcallTarget checks return-type agreement and rejects byval/sret
// callee params (§4.2). Cross-module targets are left to importer.
func checkTailcallTarget(f *vir.Function, t vir.TailCall, ctx *fnCtx) error {
	var ret vir.Type
	var params []vir.Param
	found := false

	if t.Sig != "" {
		if sig, ok := ctx.fnsigs[t.Sig]; ok {
			ret, found = sig.Ret, true
		}
	} else if t.Callee != "" {
		if fn, ok := ctx.fns[t.Callee]; ok {
			ret, params, found = fn.Ret, fn.Params, true
		} else if ef, ok := ctx.externs[t.Callee]; ok {
			ret, params, found = ef.Ret, ef.Params, true
		}
	}
	if !found {
		return nil
	}
	if !vir.Equal(ret, f.Ret) {
		return fmt.Errorf("tailcall: callee returns %s, caller returns %s — types must match (§4.2)", ret, f.Ret)
	}
	for _, p := range params {
		if p.ByVal != "" || p.SRet != "" {
			return fmt.Errorf("tailcall: callee %q has byval/sret params, which tailcall rejects (§4.2)", t.Callee)
		}
	}
	return nil
}

// checkNoreturnCallSites enforces §4.2: a direct call to a callee whose
// noreturn attribute is visible within this module must be immediately
// followed (after loc lines) by unreachable, or itself precede a
// trap/unreachable terminator. Purely structural — no analysis of the
// callee's body. Qualified (imported) callees are exempt; importer checks
// those once it can see the real callee's attributes.
func checkNoreturnCallSites(b *vir.Block, ctx *fnCtx) error {
	for i, ln := range b.Lines {
		if ln.Op != vir.OpCall || len(ln.Args) == 0 {
			continue
		}
		callee := ln.Args[0]
		if callee.Kind != vir.OperandIdent || callee.IsQualified() {
			continue
		}
		if !ctx.localNoreturn[callee.Ident] && !ctx.externNoreturn[callee.Ident] {
			continue
		}
		for _, rest := range b.Lines[i+1:] {
			if rest.Op != vir.OpLoc {
				return fmt.Errorf("call to noreturn fn %q must be immediately followed by unreachable, after loc/comments (§4.2)", callee.Ident)
			}
		}
		switch b.Term.(type) {
		case vir.Trap, vir.Unreachable:
		default:
			return fmt.Errorf("call to noreturn fn %q must precede a trap/unreachable terminator (§4.2)", callee.Ident)
		}
	}
	return nil
}