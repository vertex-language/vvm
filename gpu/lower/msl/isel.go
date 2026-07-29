// isel.go
package msl

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// instruction dispatches one body-line.
func (f *fnLower) instruction(b *msl.Block, in *gvir.Instruction) error {
	switch {
	case in.Op == gvir.OpLoc:
		if f.l.opt.Comments && len(in.Args) >= 2 {
			b.Comment(fmt.Sprintf("loc %s:%s", in.Args[0].Str, in.Args[1]))
		}
		return nil
	case in.Op.IsBuiltin():
		return f.builtin(b, in)
	}

	switch in.Op {
	case gvir.OpAlloca, gvir.OpLoad, gvir.OpStore, gvir.OpIndex, gvir.OpField,
		gvir.OpMemcopy, gvir.OpMemmove, gvir.OpMemset,
		gvir.OpExtract, gvir.OpInsert, gvir.OpSplat, gvir.OpSwizzle:
		return f.memory(b, in)

	case gvir.OpBarrier, gvir.OpFence, gvir.OpCmpxchg:
		return f.sync(b, in)
	}
	if in.Op.IsAtomic() || in.Op.IsCollective() || isMaskOp(in.Op) {
		return f.sync(b, in)
	}
	return f.compute(b, in)
}

// define binds this instruction's result, deriving its type from the opcode
// table where the table can and from the operands where §11 says it cannot.
func (f *fnLower) define(in *gvir.Instruction) (*binding, error) {
	gt, err := f.resultType(in)
	if err != nil {
		return nil, err
	}
	mt, err := f.l.mapType(gt)
	if err != nil {
		return nil, err
	}
	return f.vals.define(in.Result, gt, mt, f.l.names.ident(in.Result))
}

func (f *fnLower) resultType(in *gvir.Instruction) (gvir.Type, error) {
	switch in.Op {
	case gvir.OpCall:
		if len(in.Args) == 0 {
			return nil, fmt.Errorf("call has no callee")
		}
		callee := f.l.src.FuncByName(in.Args[0].Ident)
		if callee == nil {
			return nil, fmt.Errorf("call to undeclared func %s (§6.4)", in.Args[0].Ident)
		}
		return callee.Ret, nil

	case gvir.OpIndex, gvir.OpField:
		p, err := f.ptrOperand(in.Args[0])
		if err != nil {
			return nil, err
		}
		return p.gtyp, nil // both keep the operand's address space (§8.3)
	}
	t, ok := in.Op.ResultType(in.Suffix, in.Dim)
	if !ok {
		return nil, fmt.Errorf("%s has no mechanically derived result type", in.Op)
	}
	return t, nil
}

// assign writes an expression into the instruction's result binding, or emits
// it for effect when the opcode produces no value.
func (f *fnLower) assign(b *msl.Block, in *gvir.Instruction, e msl.Expr) error {
	if in.Result == "" || !in.Op.HasResult() {
		b.Do(e)
		return nil
	}
	bd, err := f.define(in)
	if err != nil {
		return err
	}
	b.Assign(bd.ref, e)
	return nil
}

// args materializes every operand at the instruction's suffix type.
func (f *fnLower) args(in *gvir.Instruction, t msl.Type, n int) ([]msl.Expr, error) {
	if len(in.Args) < n {
		return nil, fmt.Errorf("%s takes %d operands, got %d", in.Op, n, len(in.Args))
	}
	out := make([]msl.Expr, n)
	for i := 0; i < n; i++ {
		e, err := f.operand(in.Args[i], t)
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

// unsigned reads both operands through as_type at the unsigned twin, applies
// op, and reads the result back. .gvir puts signedness in the opcode; MSL puts
// it in the type, and this is the whole of the difference (§11.1).
func (f *fnLower) unsigned(t msl.Type, e ...msl.Expr) ([]msl.Expr, msl.Type, error) {
	u, ok := unsignedTwin(t)
	if !ok {
		return nil, nil, fmt.Errorf("%s has no unsigned twin", t)
	}
	out := make([]msl.Expr, len(e))
	for i, x := range e {
		out[i] = msl.TCall("as_type", []msl.TypeArg{msl.TArg(u)}, x)
	}
	return out, u, nil
}

func reinterpret(t msl.Type, e msl.Expr) msl.Expr {
	return msl.TCall("as_type", []msl.TypeArg{msl.TArg(t)}, e)
}

func (f *fnLower) compute(b *msl.Block, in *gvir.Instruction) error {
	t, err := f.l.mapType(in.Suffix)
	if err != nil {
		return err
	}
	vec := isVector(t)

	switch in.Op {

	// --- Integer and shared arithmetic (§11.1, §11.3) ---------------------
	case gvir.OpAdd, gvir.OpSub, gvir.OpMul:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		switch in.Op {
		case gvir.OpAdd:
			return f.assign(b, in, a[0].Add(a[1]))
		case gvir.OpSub:
			return f.assign(b, in, a[0].Sub(a[1]))
		}
		return f.assign(b, in, a[0].Mul(a[1]))

	case gvir.OpNeg:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		return f.assign(b, in, a[0].Neg())

	case gvir.OpAbs:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		fn := "abs"
		if floatBits(in.Suffix) > 0 {
			fn = "fabs"
		}
		return f.assign(b, in, msl.Call(fn, a[0]))

	case gvir.OpUDiv, gvir.OpURem:
		// §11.1 pins division by zero to 0; Metal leaves it undefined, so the
		// zero test is part of the lowering, not of the optimizer's mood.
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		ua, u, err := f.unsigned(t, a[0], a[1])
		if err != nil {
			return err
		}
		zero := msl.Cast(u, msl.I(0))
		var op msl.Expr
		if in.Op == gvir.OpUDiv {
			op = ua[0].Div(ua[1])
		} else {
			op = ua[0].Rem(ua[1])
		}
		return f.assign(b, in, reinterpret(t, msl.Cond(ua[1].Eq(zero), zero, op)))

	case gvir.OpSDiv, gvir.OpSRem:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		bits := intBits(in.Suffix)
		min, err := minInt(t, bits)
		if err != nil {
			return err
		}
		zero := msl.Cast(t, msl.I(0))
		one := msl.Cast(t, msl.I(1))
		neg1 := msl.Cast(t, msl.I(-1))
		// Both §11.1 guards fold into one predicate: b == 0, or the
		// INT_MIN / -1 overflow. The divisor is replaced by 1 so the machine
		// division is always defined, and the result forced to 0.
		bad := orOf(vec, a[1].Eq(zero), andOf(vec, a[0].Eq(min), a[1].Eq(neg1)))
		safe := selectOn(vec, bad, one, a[1])
		var op msl.Expr
		if in.Op == gvir.OpSDiv {
			op = a[0].Div(safe)
		} else {
			op = a[0].Rem(safe)
		}
		return f.assign(b, in, selectOn(vec, bad, zero, op))

	case gvir.OpUMulH, gvir.OpSMulH:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		if intBits(in.Suffix) == 64 {
			return fmt.Errorf("64-bit %s has no MSL spelling yet (see todos)", in.Op)
		}
		if in.Op == gvir.OpSMulH {
			return f.assign(b, in, msl.Call("mulhi", a[0], a[1]))
		}
		ua, _, err := f.unsigned(t, a[0], a[1])
		if err != nil {
			return err
		}
		return f.assign(b, in, reinterpret(t, msl.Call("mulhi", ua[0], ua[1])))

	case gvir.OpUMin, gvir.OpUMax:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		ua, _, err := f.unsigned(t, a[0], a[1])
		if err != nil {
			return err
		}
		fn := "min"
		if in.Op == gvir.OpUMax {
			fn = "max"
		}
		return f.assign(b, in, reinterpret(t, msl.Call(fn, ua[0], ua[1])))

	case gvir.OpSMin, gvir.OpSMax:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		fn := "min"
		if in.Op == gvir.OpSMax {
			fn = "max"
		}
		return f.assign(b, in, msl.Call(fn, a[0], a[1]))

	// --- Bitwise and shifts (§11.2) ---------------------------------------
	case gvir.OpAnd, gvir.OpOr, gvir.OpXor:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		// and/or/xor also take i1 and vec[i1,N] (§4.5). On a scalar bool the
		// logical operators are the elementwise spelling; on a bool vector the
		// bitwise ones are.
		boolish := isBoolType(t) && !vec
		switch in.Op {
		case gvir.OpAnd:
			if boolish {
				return f.assign(b, in, a[0].And(a[1]))
			}
			return f.assign(b, in, a[0].BitAnd(a[1]))
		case gvir.OpOr:
			if boolish {
				return f.assign(b, in, a[0].Or(a[1]))
			}
			return f.assign(b, in, a[0].BitOr(a[1]))
		}
		if boolish {
			return f.assign(b, in, a[0].Ne(a[1]))
		}
		return f.assign(b, in, a[0].BitXor(a[1]))

	case gvir.OpNot:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		if isBoolType(t) {
			return f.assign(b, in, a[0].Not())
		}
		return f.assign(b, in, a[0].BitNot())

	case gvir.OpShl, gvir.OpLShr, gvir.OpAShr:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		// §11.2 masks the count; C++ leaves a count at or above the width
		// undefined, so the mask is emitted rather than assumed.
		bits := intBits(in.Suffix)
		n := a[1].BitAnd(msl.Cast(t, msl.I(int64(bits-1))))
		switch in.Op {
		case gvir.OpShl:
			return f.assign(b, in, a[0].Shl(n))
		case gvir.OpAShr:
			return f.assign(b, in, a[0].Shr(n))
		}
		ua, u, err := f.unsigned(t, a[0])
		if err != nil {
			return err
		}
		un := reinterpret(u, n)
		return f.assign(b, in, reinterpret(t, ua[0].Shr(un)))

	case gvir.OpRotl, gvir.OpRotr:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		bits := intBits(in.Suffix)
		mask := msl.Cast(t, msl.I(int64(bits-1)))
		n := a[1].BitAnd(mask)
		if in.Op == gvir.OpRotr {
			n = msl.Cast(t, msl.I(int64(bits))).Sub(n).BitAnd(mask)
		}
		ua, u, err := f.unsigned(t, a[0])
		if err != nil {
			return err
		}
		return f.assign(b, in, reinterpret(t, msl.Call("rotate", ua[0], reinterpret(u, n))))

	case gvir.OpCtlz, gvir.OpCttz, gvir.OpPopcnt, gvir.OpBrev:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		ua, _, err := f.unsigned(t, a[0])
		if err != nil {
			return err
		}
		// MSL follows OpenCL: clz and ctz of zero already yield the operand
		// width, which is what §11.2 pins them to.
		fn := map[gvir.Opcode]string{
			gvir.OpCtlz: "clz", gvir.OpCttz: "ctz",
			gvir.OpPopcnt: "popcount", gvir.OpBrev: "reverse_bits",
		}[in.Op]
		return f.assign(b, in, reinterpret(t, msl.Call(fn, ua[0])))

	case gvir.OpBSwap:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		return f.assign(b, in, f.bswap(t, intBits(in.Suffix), a[0]))

	// --- Float (§11.3) -----------------------------------------------------
	case gvir.OpDiv:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		// §11.3 is IEEE; Metal's `/` is an approximation under fast math, and
		// precise::divide is the spelling that is not.
		return f.assign(b, in, msl.Call("precise::divide", a[0], a[1]))

	case gvir.OpSqrt:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Call("precise::sqrt", a[0]))

	case gvir.OpFma:
		a, err := f.args(in, t, 3)
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Call("fma", a[0], a[1], a[2]))

	case gvir.OpMin, gvir.OpMax:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		// §11.3 wants IEEE minNum/maxNum. MSL's fmin/fmax are the quieting
		// pair; min/max are not.
		fn := "fmin"
		if in.Op == gvir.OpMax {
			fn = "fmax"
		}
		return f.assign(b, in, msl.Call(fn, a[0], a[1]))

	case gvir.OpFloor, gvir.OpCeil, gvir.OpRound, gvir.OpRoundEven, gvir.OpTruncF:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		fn := map[gvir.Opcode]string{
			gvir.OpFloor: "floor", gvir.OpCeil: "ceil",
			gvir.OpRound: "round", gvir.OpRoundEven: "rint", gvir.OpTruncF: "trunc",
		}[in.Op]
		return f.assign(b, in, msl.Call(fn, a[0]))

	case gvir.OpCopysign:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Call("copysign", a[0], a[1]))

	case gvir.OpIsNaN, gvir.OpIsInf:
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		fn := "isnan"
		if in.Op == gvir.OpIsInf {
			fn = "isinf"
		}
		return f.assign(b, in, msl.Call(fn, a[0]))

	// --- Approximate opcodes (§11.6) ---------------------------------------
	case gvir.OpRcp, gvir.OpRsqrt, gvir.OpSin, gvir.OpCos, gvir.OpExp2, gvir.OpLog2, gvir.OpTanh:
		if !f.l.src.Profile.Approx {
			return fmt.Errorf("%s requires float_profile approx (§11.6)", in.Op)
		}
		a, err := f.args(in, t, 1)
		if err != nil {
			return err
		}
		switch in.Op {
		case gvir.OpRcp:
			return f.assign(b, in, msl.Call("fast::divide", msl.Cast(t, msl.F(1)), a[0]))
		case gvir.OpTanh:
			return f.assign(b, in, msl.Call("precise::tanh", a[0]))
		}
		fn := map[gvir.Opcode]string{
			gvir.OpRsqrt: "fast::rsqrt", gvir.OpSin: "fast::sin", gvir.OpCos: "fast::cos",
			gvir.OpExp2: "fast::exp2", gvir.OpLog2: "fast::log2",
		}[in.Op]
		return f.assign(b, in, msl.Call(fn, a[0]))

	// --- Comparisons (§11.4) ------------------------------------------------
	case gvir.OpEq, gvir.OpNe:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		if in.Op == gvir.OpEq {
			return f.assign(b, in, a[0].Eq(a[1]))
		}
		return f.assign(b, in, a[0].Ne(a[1]))

	case gvir.OpUlt, gvir.OpUle, gvir.OpUgt, gvir.OpUge:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		if gvir.IsPtrWord(in.Suffix) {
			// The unsigned family doubles as the pointer family; both operands
			// are byte pointers in the same space (§11.4), so C++ compares
			// them directly.
			return f.assign(b, in, cmp(in.Op, a[0], a[1]))
		}
		ua, _, err := f.unsigned(t, a[0], a[1])
		if err != nil {
			return err
		}
		return f.assign(b, in, cmp(in.Op, ua[0], ua[1]))

	case gvir.OpSlt, gvir.OpSle, gvir.OpSgt, gvir.OpSge,
		gvir.OpOlt, gvir.OpOle, gvir.OpOgt, gvir.OpOge, gvir.OpOeq:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		return f.assign(b, in, cmp(in.Op, a[0], a[1]))

	case gvir.OpOne:
		// Ordered not-equal: `!=` in C++ is the *unordered* form and is true
		// for a NaN operand, which §11.4 does not provide.
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		return f.assign(b, in, orOf(vec, a[0].Lt(a[1]), a[0].Gt(a[1])))

	case gvir.OpOrd, gvir.OpUnord:
		a, err := f.args(in, t, 2)
		if err != nil {
			return err
		}
		na, nb := msl.Call("isnan", a[0]), msl.Call("isnan", a[1])
		if in.Op == gvir.OpUnord {
			return f.assign(b, in, orOf(vec, na, nb))
		}
		return f.assign(b, in, andOf(vec, na.Not(), nb.Not()))

	// --- Conversions (§11.5) ------------------------------------------------
	case gvir.OpTrunc, gvir.OpSext, gvir.OpFPTrunc, gvir.OpFPExt:
		src, err := f.srcType(in.Args[0])
		if err != nil {
			return err
		}
		a, err := f.args(in, src, 1)
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Cast(t, a[0]))

	case gvir.OpZext:
		src, err := f.srcType(in.Args[0])
		if err != nil {
			return err
		}
		a, err := f.args(in, src, 1)
		if err != nil {
			return err
		}
		if isBoolType(src) {
			return f.assign(b, in, msl.Cast(t, a[0]))
		}
		ua, _, err := f.unsigned(src, a[0])
		if err != nil {
			return err
		}
		return f.assign(b, in, msl.Cast(t, ua[0]))

	case gvir.OpStoint, gvir.OpUtoint:
		src, err := f.srcType(in.Args[0])
		if err != nil {
			return err
		}
		a, err := f.args(in, src, 1)
		if err != nil {
			return err
		}
		e, err := f.floatToInt(in.Op == gvir.OpUtoint, t, intBits(in.Suffix), vec, a[0])
		if err != nil {
			return err
		}
		return f.assign(b, in, e)

	case gvir.OpInttos, gvir.OpInttou:
		src, err := f.srcType(in.Args[0])
		if err != nil {
			return err
		}
		a, err := f.args(in, src, 1)
		if err != nil {
			return err
		}
		if in.Op == gvir.OpInttou {
			ua, _, err := f.unsigned(src, a[0])
			if err != nil {
				return err
			}
			return f.assign(b, in, msl.Cast(t, ua[0]))
		}
		return f.assign(b, in, msl.Cast(t, a[0]))

	case gvir.OpBitcast:
		src, err := f.srcType(in.Args[0])
		if err != nil {
			return err
		}
		a, err := f.args(in, src, 1)
		if err != nil {
			return err
		}
		return f.assign(b, in, reinterpret(t, a[0]))

	// --- Select and calls (§11.7) -------------------------------------------
	case gvir.OpSelect:
		a1, err := f.operand(in.Args[1], t)
		if err != nil {
			return err
		}
		a2, err := f.operand(in.Args[2], t)
		if err != nil {
			return err
		}
		condT := msl.Type(msl.Bool)
		if ct, err := f.srcType(in.Args[0]); err == nil {
			condT = ct
		}
		c, err := f.operand(in.Args[0], condT)
		if err != nil {
			return err
		}
		if isVector(condT) {
			// MSL's select(a, b, c) yields b where c is true, so the arms are
			// written in the opposite order from the ternary.
			return f.assign(b, in, msl.Call("select", a2, a1, c))
		}
		return f.assign(b, in, msl.Cond(c, a1, a2))

	case gvir.OpCall:
		callee := f.l.src.FuncByName(in.Args[0].Ident)
		if callee == nil {
			return fmt.Errorf("call to undeclared func %s (§6.4)", in.Args[0].Ident)
		}
		args := make([]msl.Expr, 0, len(in.Args)-1)
		for i, o := range in.Args[1:] {
			var pt msl.Type
			if i < len(callee.Params) {
				var err error
				if pt, err = f.l.mapType(callee.Params[i].Type); err != nil {
					return err
				}
			}
			e, err := f.operand(o, pt)
			if err != nil {
				return err
			}
			args = append(args, e)
		}
		call := msl.Call(f.l.names.ident(callee.Name), args...)
		if gvir.IsVoid(callee.Ret) || in.Result == "" {
			b.Do(call)
			return nil
		}
		return f.assign(b, in, call)
	}
	return fmt.Errorf("%s is not lowered by this backend", in.Op)
}

// srcType returns the MSL type an operand is already bound at, which the
// conversions need because their suffix names only the destination (§11.5).
func (f *fnLower) srcType(o gvir.Operand) (msl.Type, error) {
	if o.Kind != gvir.OperandIdent {
		return nil, fmt.Errorf("conversion source %s is not a value", o)
	}
	bd, ok := f.vals.lookup(o.Ident)
	if !ok {
		return nil, fmt.Errorf("%s is not bound (§7.3)", o.Ident)
	}
	return bd.typ, nil
}

func cmp(op gvir.Opcode, a, b msl.Expr) msl.Expr {
	switch op {
	case gvir.OpUlt, gvir.OpSlt, gvir.OpOlt:
		return a.Lt(b)
	case gvir.OpUle, gvir.OpSle, gvir.OpOle:
		return a.Le(b)
	case gvir.OpUgt, gvir.OpSgt, gvir.OpOgt:
		return a.Gt(b)
	case gvir.OpUge, gvir.OpSge, gvir.OpOge:
		return a.Ge(b)
	}
	return a.Eq(b)
}

// The logical operators do not apply elementwise to a bool vector; the
// bitwise ones do. Every place a predicate is combined picks between them.
func andOf(vec bool, a, b msl.Expr) msl.Expr {
	if vec {
		return a.BitAnd(b)
	}
	return a.And(b)
}

func orOf(vec bool, a, b msl.Expr) msl.Expr {
	if vec {
		return a.BitOr(b)
	}
	return a.Or(b)
}

func selectOn(vec bool, cond, then, els msl.Expr) msl.Expr {
	if vec {
		return msl.Call("select", els, then, cond)
	}
	return msl.Cond(cond, then, els)
}

func isBoolType(t msl.Type) bool {
	switch x := t.(type) {
	case msl.ScalarType:
		return x == msl.Bool
	case msl.VecType:
		return isBoolType(x.Elem)
	}
	return false
}

func isMaskOp(op gvir.Opcode) bool {
	switch op {
	case gvir.OpMaskCount, gvir.OpMaskTest, gvir.OpMaskFirst, gvir.OpMaskEmpty,
		gvir.OpMaskLt, gvir.OpMaskLe, gvir.OpMaskGt, gvir.OpMaskGe, gvir.OpMaskEq:
		return true
	}
	return false
}

// minInt spells the most negative value of a signed width. At 8 and 16 bits
// the literal fits; at 32 and 64 it does not, and the bit pattern is written
// through as_type instead.
func minInt(t msl.Type, bits int) (msl.Expr, error) {
	switch bits {
	case 8:
		return msl.Cast(t, msl.I(-128)), nil
	case 16:
		return msl.Cast(t, msl.I(-32768)), nil
	case 32:
		return reinterpret(t, msl.U(1 << 31)), nil
	case 64:
		return reinterpret(t, msl.U(1<<63)), nil
	}
	return msl.Expr{}, fmt.Errorf("no minimum for a %d-bit integer", bits)
}

// bswap has no MSL builtin. The byte permutation is generated rather than
// left as a todo because it is mechanical at every width.
func (f *fnLower) bswap(t msl.Type, bits int, x msl.Expr) msl.Expr {
	if bits == 8 {
		return x
	}
	u, _ := unsignedTwin(t)
	ux := reinterpret(u, x)
	n := bits / 8
	mask := msl.Cast(u, msl.I(0xFF))
	var acc msl.Expr
	for i := 0; i < n; i++ {
		lo := msl.Cast(u, msl.I(int64(8*i)))
		hi := msl.Cast(u, msl.I(int64(8*(n-1-i))))
		term := ux.Shr(lo).BitAnd(mask).Shl(hi)
		if acc.IsZero() {
			acc = term
		} else {
			acc = acc.BitOr(term)
		}
	}
	return reinterpret(t, acc)
}

// floatToInt realizes §11.5's saturating, total conversion. Metal's own
// float-to-int conversion is undefined out of range, so the clamp and the NaN
// case are emitted; the bounds are exact powers of two and therefore exactly
// representable in every float format.
func (f *fnLower) floatToInt(unsignedDst bool, t msl.Type, bits int, vec bool, x msl.Expr) (msl.Expr, error) {
	if bits == 0 {
		return msl.Expr{}, fmt.Errorf("destination is not an integer type")
	}
	dst := t
	if unsignedDst {
		u, ok := unsignedTwin(t)
		if !ok {
			return msl.Expr{}, fmt.Errorf("%s has no unsigned twin", t)
		}
		dst = u
	}

	var hiF, loF float64
	var hiI, loI msl.Expr
	if unsignedDst {
		hiF, loF = math.Ldexp(1, bits), 0
		hiI = msl.Cast(dst, msl.I(0)).BitNot() // all ones: the unsigned maximum
		loI = msl.Cast(dst, msl.I(0))
	} else {
		hiF = math.Ldexp(1, bits-1)
		loF = -hiF
		min, err := minInt(t, bits)
		if err != nil {
			return msl.Expr{}, err
		}
		loI = min
		hiI = min.BitNot() // ~INT_MIN == INT_MAX
	}

	zero := msl.Cast(dst, msl.I(0))
	conv := msl.Cast(dst, x)
	e := selectOn(vec, x.Ge(msl.F(hiF)), hiI,
		selectOn(vec, x.Le(msl.F(loF)), loI, conv))
	e = selectOn(vec, msl.Call("isnan", x), zero, e)
	if unsignedDst {
		return reinterpret(t, e), nil
	}
	return e, nil
}