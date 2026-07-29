// isel.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Instruction selection for the value-producing opcodes.
//
// Every mnemonic here is a v_* form writing a VGPR, an AGPR, %vcc or a
// .lanemask, which is what V7 requires; nothing in this file can produce a V6
// violation because nothing in this file writes a scalar destination.

// form names the mnemonic for each shape an elementwise opcode takes. An
// empty string means the shape has no single-instruction lowering and falls to
// a special case or an error.
type form struct {
	i32, i64, f32, f64 string
	rev                bool // v_*rev_* forms take (count, value)
	perDword           bool // bitwise ops apply dword by dword
}

var aluForms = map[gvir.Opcode]form{
	gvir.OpAdd: {i32: "v_add_u32", f32: "v_add_f32", f64: "v_add_f64"},
	gvir.OpSub: {i32: "v_sub_u32", f32: "v_sub_f32"},
	gvir.OpMul: {i32: "v_mul_lo_u32", f32: "v_mul_f32", f64: "v_mul_f64"},

	gvir.OpAnd: {i32: "v_and_b32", i64: "v_and_b32", perDword: true},
	gvir.OpOr:  {i32: "v_or_b32", i64: "v_or_b32", perDword: true},
	gvir.OpXor: {i32: "v_xor_b32", i64: "v_xor_b32", perDword: true},
	gvir.OpNot: {i32: "v_not_b32", i64: "v_not_b32", perDword: true},

	gvir.OpShl:  {i32: "v_lshlrev_b32", i64: "v_lshlrev_b64", rev: true},
	gvir.OpLShr: {i32: "v_lshrrev_b32", i64: "v_lshrrev_b64", rev: true},
	gvir.OpAShr: {i32: "v_ashrrev_i32", i64: "v_ashrrev_i64", rev: true},

	gvir.OpUMin:  {i32: "v_min_u32"},
	gvir.OpUMax:  {i32: "v_max_u32"},
	gvir.OpSMin:  {i32: "v_min_i32"},
	gvir.OpSMax:  {i32: "v_max_i32"},
	gvir.OpUMulH: {i32: "v_mul_hi_u32"},
	gvir.OpSMulH: {i32: "v_mul_hi_i32"},

	gvir.OpMin:      {f32: "v_min_f32", f64: "v_min_f64"},
	gvir.OpMax:      {f32: "v_max_f32", f64: "v_max_f64"},
	gvir.OpFma:      {f32: "v_fma_f32", f64: "v_fma_f64"},
	gvir.OpFloor:    {f32: "v_floor_f32", f64: "v_floor_f64"},
	gvir.OpCeil:     {f32: "v_ceil_f32", f64: "v_ceil_f64"},
	gvir.OpTruncF:   {f32: "v_trunc_f32", f64: "v_trunc_f64"},
	gvir.OpRoundEven: {f32: "v_rndne_f32", f64: "v_rndne_f64"},

	gvir.OpRcp:   {f32: "v_rcp_f32", f64: "v_rcp_f64"},
	gvir.OpRsqrt: {f32: "v_rsq_f32", f64: "v_rsq_f64"},
	gvir.OpExp2:  {f32: "v_exp_f32"},
	gvir.OpLog2:  {f32: "v_log_f32"},
}

// cmpForms names the v_cmp mnemonic per comparison and shape. The destination
// is a .lanemask, which V7 admits for vector comparisons.
var cmpForms = map[gvir.Opcode]form{
	gvir.OpEq:  {i32: "v_cmp_eq_u32", i64: "v_cmp_eq_u64"},
	gvir.OpNe:  {i32: "v_cmp_ne_u32", i64: "v_cmp_ne_u64"},
	gvir.OpUlt: {i32: "v_cmp_lt_u32", i64: "v_cmp_lt_u64"},
	gvir.OpUle: {i32: "v_cmp_le_u32", i64: "v_cmp_le_u64"},
	gvir.OpUgt: {i32: "v_cmp_gt_u32", i64: "v_cmp_gt_u64"},
	gvir.OpUge: {i32: "v_cmp_ge_u32", i64: "v_cmp_ge_u64"},
	gvir.OpSlt: {i32: "v_cmp_lt_i32", i64: "v_cmp_lt_i64"},
	gvir.OpSle: {i32: "v_cmp_le_i32", i64: "v_cmp_le_i64"},
	gvir.OpSgt: {i32: "v_cmp_gt_i32", i64: "v_cmp_gt_i64"},
	gvir.OpSge: {i32: "v_cmp_ge_i32", i64: "v_cmp_ge_i64"},

	// v_cmp_lg is ordered not-equal; v_cmp_neq is the unordered form, which
	// §11.4 does not provide (every unordered predicate is `not` of an
	// ordered one).
	gvir.OpOeq:   {f32: "v_cmp_eq_f32", f64: "v_cmp_eq_f64"},
	gvir.OpOne:   {f32: "v_cmp_lg_f32", f64: "v_cmp_lg_f64"},
	gvir.OpOlt:   {f32: "v_cmp_lt_f32", f64: "v_cmp_lt_f64"},
	gvir.OpOle:   {f32: "v_cmp_le_f32", f64: "v_cmp_le_f64"},
	gvir.OpOgt:   {f32: "v_cmp_gt_f32", f64: "v_cmp_gt_f64"},
	gvir.OpOge:   {f32: "v_cmp_ge_f32", f64: "v_cmp_ge_f64"},
	gvir.OpOrd:   {f32: "v_cmp_o_f32", f64: "v_cmp_o_f64"},
	gvir.OpUnord: {f32: "v_cmp_u_f32", f64: "v_cmp_u_f64"},
}

func (f form) pick(t gvir.Type) string {
	e := gvir.ElemOrSelf(t)
	if n := intBits(e); n != 0 {
		if n == 64 {
			return f.i64
		}
		return f.i32
	}
	switch floatBits(e) {
	case 32:
		return f.f32
	case 64:
		return f.f64
	}
	return ""
}

func (b *bodyLowerer) alu(o *amdtx.Body, in *gvir.Instruction) error {
	switch in.Op {
	case gvir.OpUDiv, gvir.OpSDiv, gvir.OpURem, gvir.OpSRem:
		return fmt.Errorf("amdgcn has no integer divide instruction; the reciprocal sequence plus " +
			"§11.1's division-by-zero and INT_MIN/-1 guards is unimplemented")
	case gvir.OpDiv:
		return fmt.Errorf("IEEE float division needs the v_div_scale / v_div_fmas / v_div_fixup " +
			"sequence; v_rcp_f32 alone is a ~1 ULP approximation and §11.3 requires IEEE")
	case gvir.OpSqrt:
		return fmt.Errorf("v_sqrt_f32 is a ~1 ULP approximation; the IEEE result §11.3 requires " +
			"needs a Newton refinement this backend does not emit")
	case gvir.OpTanh:
		return fmt.Errorf("amdgcn has no tanh instruction; the §11.6 8.0 ULP bound needs a polynomial")
	case gvir.OpSelect:
		return b.selectOp(o, in)
	case gvir.OpCall:
		return b.call(o, in)
	case gvir.OpNeg:
		return b.neg(o, in)
	case gvir.OpAbs:
		return b.abs(o, in)
	case gvir.OpRotl, gvir.OpRotr:
		return b.rotate(o, in)
	case gvir.OpCtlz, gvir.OpCttz:
		return b.countZeros(o, in)
	case gvir.OpPopcnt:
		return b.simple1(o, in, "v_bcnt_u32_b32", amdtx.Imm(0))
	case gvir.OpBrev:
		return b.brev(o, in)
	case gvir.OpBSwap:
		return b.bswap(o, in)
	case gvir.OpCopysign:
		return b.copysign(o, in)
	case gvir.OpIsNaN:
		return b.compare(o, in, form{f32: "v_cmp_u_f32", f64: "v_cmp_u_f64"}, true)
	case gvir.OpIsInf:
		return b.isinf(o, in)
	case gvir.OpRound:
		return b.round(o, in)
	case gvir.OpSin, gvir.OpCos:
		return b.trig(o, in)
	}
	if f, ok := cmpForms[in.Op]; ok {
		return b.compare(o, in, f, false)
	}
	if isConversion(in.Op) {
		return b.convert(o, in)
	}
	f, ok := aluForms[in.Op]
	if !ok {
		return fmt.Errorf("%s is not yet lowered", in.Op)
	}
	return b.elementwise(o, in, f)
}

// elementwise emits one mnemonic per lane, or per dword per lane for the
// bitwise forms.
func (b *bodyLowerer) elementwise(o *amdtx.Body, in *gvir.Instruction, f form) error {
	t := in.Suffix
	mn := f.pick(t)
	if mn == "" {
		if in.Op == gvir.OpSub && floatBits(gvir.ElemOrSelf(t)) == 64 {
			return b.subF64(o, in)
		}
		if intBits(gvir.ElemOrSelf(t)) == 64 && (in.Op == gvir.OpAdd || in.Op == gvir.OpSub) {
			return b.addSub64(o, in)
		}
		if in.Op == gvir.OpMul && intBits(gvir.ElemOrSelf(t)) == 64 {
			return fmt.Errorf("64-bit integer multiply needs the split lo/hi sequence; unimplemented")
		}
		return fmt.Errorf("%s has no lowering for %s", in.Op, t)
	}
	dst, err := b.define(in.Result, t)
	if err != nil {
		return err
	}
	lanes := laneCount(t)
	elem := gvir.ElemOrSelf(t)
	n := dwordsOf(elem)
	if f.perDword {
		for lane := 0; lane < lanes; lane++ {
			for d := 0; d < n; d++ {
				args := make([]amdtx.Operand, 0, 2)
				for _, a := range in.Args {
					s, err := b.src(o, a, elem, lane)
					if err != nil {
						return err
					}
					args = append(args, dwordOperand(s, d, n))
				}
				o.Inst(mn, dword(dst.regs[lane], d, n), args...)
			}
		}
		return b.fixup(o, dst, in)
	}
	for lane := 0; lane < lanes; lane++ {
		args := make([]amdtx.Operand, 0, len(in.Args))
		for _, a := range in.Args {
			s, err := b.src(o, a, elem, lane)
			if err != nil {
				return err
			}
			args = append(args, s)
		}
		if f.rev && len(args) == 2 {
			// Shift counts mask to the operand width on both sides (§11.2
			// and the ISA agree), except below 32 bits where the register is
			// wider than the type.
			if subDword(elem) {
				m, err := b.maskedCount(o, args[1], intBits(elem))
				if err != nil {
					return err
				}
				args[1] = m
			}
			args[0], args[1] = args[1], args[0]
		}
		o.Inst(mn, dst.regs[lane], args...)
	}
	return b.fixup(o, dst, in)
}

// fixup restores the zero-extension invariant after a wrapping operation on a
// sub-dword type, so that §11.1's "wrapping is modulo 2^N" is literally true
// at 8 and 16 bits.
func (b *bodyLowerer) fixup(o *amdtx.Body, dst *value, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if !subDword(elem) || !wrapping(in.Op) {
		return nil
	}
	mask := amdtx.Imm(subDwordMask(intBits(elem)))
	for _, r := range dst.regs {
		o.Inst("v_and_b32", r, mask, r)
	}
	return nil
}

func wrapping(op gvir.Opcode) bool {
	switch op {
	case gvir.OpAdd, gvir.OpSub, gvir.OpMul, gvir.OpNeg, gvir.OpNot,
		gvir.OpShl, gvir.OpRotl, gvir.OpRotr, gvir.OpBrev, gvir.OpBSwap:
		return true
	}
	return false
}

func (b *bodyLowerer) maskedCount(o *amdtx.Body, count amdtx.Operand, bits int) (amdtx.Operand, error) {
	t, err := b.temp(gvir.I32)
	if err != nil {
		return nil, err
	}
	o.Inst("v_and_b32", t.regs[0], amdtx.Imm(int64(bits-1)), count)
	return t.regs[0], nil
}

func dwordOperand(o amdtx.Operand, d, n int) amdtx.Operand {
	if n <= 1 {
		return o
	}
	if r, ok := o.(amdtx.Reg); ok {
		return r.Dword(d)
	}
	return o
}

// addSub64 emits the carry-chained 64-bit add and subtract. amdgcn has no
// single 64-bit integer add, which is why the pair is spelled out.
func (b *bodyLowerer) addSub64(o *amdtx.Body, in *gvir.Instruction) error {
	lo, hi := "v_add_co_u32", "v_addc_co_u32"
	if in.Op == gvir.OpSub {
		lo, hi = "v_sub_co_u32", "v_subb_co_u32"
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], gvir.I64, lane)
		if err != nil {
			return err
		}
		y, err := b.src(o, in.Args[1], gvir.I64, lane)
		if err != nil {
			return err
		}
		o.Inst(lo, dst.regs[lane].Dword(0), dwordOperand(x, 0, 2), dwordOperand(y, 0, 2))
		o.Inst(hi, dst.regs[lane].Dword(1), dwordOperand(x, 1, 2), dwordOperand(y, 1, 2))
	}
	return nil
}

// subF64 flips the sign bit of the subtrahend and adds: amdgcn has no
// v_sub_f64, and the sign flip is exact for every input including NaN and the
// zeroes.
func (b *bodyLowerer) subF64(o *amdtx.Body, in *gvir.Instruction) error {
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], gvir.F64, lane)
		if err != nil {
			return err
		}
		y, err := b.src(o, in.Args[1], gvir.F64, lane)
		if err != nil {
			return err
		}
		t, err := b.temp(gvir.F64)
		if err != nil {
			return err
		}
		o.Inst("v_mov_b32", t.regs[0].Dword(0), dwordOperand(y, 0, 2))
		o.Inst("v_xor_b32", t.regs[0].Dword(1), amdtx.Imm(int64(int32(-1<<31))), dwordOperand(y, 1, 2))
		o.Inst("v_add_f64", dst.regs[lane], x, t.regs[0])
	}
	return nil
}

func (b *bodyLowerer) neg(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if floatBits(elem) != 0 {
		return b.signFlip(o, in)
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		if intBits(elem) == 64 {
			o.Inst("v_sub_co_u32", dst.regs[lane].Dword(0), amdtx.Imm(0), dwordOperand(x, 0, 2))
			o.Inst("v_subb_co_u32", dst.regs[lane].Dword(1), amdtx.Imm(0), dwordOperand(x, 1, 2))
			continue
		}
		o.Inst("v_sub_u32", dst.regs[lane], amdtx.Imm(0), x)
	}
	return b.fixup(o, dst, in)
}

// signFlip implements float neg and is also the shape abs and copysign use.
func (b *bodyLowerer) signFlip(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	n := dwordsOf(elem)
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	signMask := amdtx.Imm(int64(int32(-1 << 31)))
	if floatBits(elem) == 16 {
		signMask = amdtx.Imm(0x8000)
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		if n == 2 {
			o.Inst("v_mov_b32", dst.regs[lane].Dword(0), dwordOperand(x, 0, 2))
		}
		o.Inst("v_xor_b32", dword(dst.regs[lane], n-1, n), signMask, dwordOperand(x, n-1, n))
	}
	return nil
}

func (b *bodyLowerer) abs(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	n := dwordsOf(elem)
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		if floatBits(elem) != 0 {
			mask := amdtx.Imm(0x7fffffff)
			if floatBits(elem) == 16 {
				mask = amdtx.Imm(0x7fff)
			}
			if n == 2 {
				o.Inst("v_mov_b32", dst.regs[lane].Dword(0), dwordOperand(x, 0, 2))
			}
			o.Inst("v_and_b32", dword(dst.regs[lane], n-1, n), mask, dwordOperand(x, n-1, n))
			continue
		}
		if intBits(elem) == 64 {
			return fmt.Errorf("abs.i64 needs the negate-and-select pair; unimplemented")
		}
		s, err := b.signExtend(o, x, elem)
		if err != nil {
			return err
		}
		neg, err := b.temp(gvir.I32)
		if err != nil {
			return err
		}
		o.Inst("v_sub_u32", neg.regs[0], amdtx.Imm(0), s)
		o.Inst("v_max_i32", dst.regs[lane], s, neg.regs[0])
	}
	return b.fixup(o, dst, in)
}

// signExtend materializes a sign-extended scratch copy of a sub-dword operand.
// It is never written back to the value's own register: the invariant is that
// a named i8 or i16 register holds the zero-extended value.
func (b *bodyLowerer) signExtend(o *amdtx.Body, x amdtx.Operand, t gvir.Type) (amdtx.Operand, error) {
	bits := intBits(t)
	if bits == 0 || bits >= 32 {
		return x, nil
	}
	tmp, err := b.temp(gvir.I32)
	if err != nil {
		return nil, err
	}
	o.Inst("v_bfe_i32", tmp.regs[0], x, amdtx.Imm(0), amdtx.Imm(int64(bits)))
	return tmp.regs[0], nil
}

func (b *bodyLowerer) rotate(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	bits := intBits(elem)
	if bits != 32 {
		return fmt.Errorf("rotate is implemented for i32 only; %s needs the shift pair", elem)
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	mn := "v_alignbit_b32" // rotr: (src, src) >> count
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		c, err := b.src(o, in.Args[1], gvir.I32, lane)
		if err != nil {
			return err
		}
		if in.Op == gvir.OpRotl {
			neg, err := b.temp(gvir.I32)
			if err != nil {
				return err
			}
			o.Inst("v_sub_u32", neg.regs[0], amdtx.Imm(32), c)
			c = neg.regs[0]
		}
		o.Inst(mn, dst.regs[lane], x, x, c)
	}
	return nil
}

// countZeros implements ctlz and cttz, including §11.2's "of zero yields the
// operand bit width". v_ffbh and v_ffbl return -1 for a zero input, so
// clamping with v_min_u32 produces N without a branch.
func (b *bodyLowerer) countZeros(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	bits := intBits(elem)
	if bits == 64 {
		return fmt.Errorf("ctlz/cttz on i64 need the split sequence; unimplemented")
	}
	mn := b.l.pick("v_ffbh_u32", "v_clz_i32_u32")
	if in.Op == gvir.OpCttz {
		mn = b.l.pick("v_ffbl_b32", "v_ctz_i32_b32")
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		o.Inst(mn, dst.regs[lane], x)
		if in.Op == gvir.OpCtlz {
			// The value is zero-extended, so clz32 counts the padding too.
			o.Inst("v_min_u32", dst.regs[lane], dst.regs[lane], amdtx.Imm(32))
			if bits < 32 {
				o.Inst("v_sub_u32", dst.regs[lane], dst.regs[lane], amdtx.Imm(int64(32-bits)))
			}
			continue
		}
		o.Inst("v_min_u32", dst.regs[lane], dst.regs[lane], amdtx.Imm(int64(bits)))
	}
	return nil
}

func (b *bodyLowerer) brev(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	bits := intBits(elem)
	if bits == 64 {
		return fmt.Errorf("brev.i64 needs the dword swap and pair; unimplemented")
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		o.Inst("v_bfrev_b32", dst.regs[lane], x)
		if bits < 32 {
			o.Inst("v_lshrrev_b32", dst.regs[lane], amdtx.Imm(int64(32-bits)), dst.regs[lane])
		}
	}
	return nil
}

func (b *bodyLowerer) bswap(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	var sel int64
	switch intBits(elem) {
	case 8:
		return b.copyValue(o, dst, in) // identity
	case 16:
		sel = 0x00000405
	case 32:
		sel = 0x00010203
	default:
		return fmt.Errorf("bswap.i64 needs the split/perm/join sequence; unimplemented")
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		o.Inst("v_perm_b32", dst.regs[lane], x, x, amdtx.Imm(sel))
	}
	return nil
}

func (b *bodyLowerer) copysign(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	n := dwordsOf(elem)
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		y, err := b.src(o, in.Args[1], elem, lane)
		if err != nil {
			return err
		}
		if n == 2 {
			o.Inst("v_mov_b32", dst.regs[lane].Dword(0), dwordOperand(x, 0, 2))
		}
		o.Inst("v_bfi_b32", dword(dst.regs[lane], n-1, n),
			amdtx.Imm(0x7fffffff), dwordOperand(x, n-1, n), dwordOperand(y, n-1, n))
	}
	return nil
}

// isinf tests the float class directly: bit 9 is +inf and bit 2 is -inf.
func (b *bodyLowerer) isinf(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	mn := "v_cmp_class_f32"
	if floatBits(elem) == 64 {
		mn = "v_cmp_class_f64"
	}
	dst, err := b.define(in.Result, boolResult(in.Suffix))
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		o.Inst(mn, dst.regs[lane], x, amdtx.Imm(0x204))
	}
	return nil
}

// round is half-away-from-zero, which has no amdgcn rounding mode:
// t = trunc(x); |x - t| >= 0.5 ? t + copysign(1, x) : t.
func (b *bodyLowerer) round(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if floatBits(elem) != 32 {
		return fmt.Errorf("round is implemented for f32 only; %s needs the same expansion at its width", elem)
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		t, _ := b.temp(gvir.F32)
		r, _ := b.temp(gvir.F32)
		a, _ := b.temp(gvir.F32)
		one, _ := b.temp(gvir.F32)
		p, _ := b.temp(gvir.I1)
		bump, _ := b.temp(gvir.F32)
		o.Inst("v_trunc_f32", t.regs[0], x)
		o.Inst("v_sub_f32", r.regs[0], x, t.regs[0])
		o.Inst("v_and_b32", a.regs[0], amdtx.Imm(0x7fffffff), r.regs[0])
		o.Inst("v_cmp_ge_f32", p.regs[0], a.regs[0], amdtx.FImm(0.5))
		o.Inst("v_bfi_b32", one.regs[0], amdtx.Imm(0x7fffffff), amdtx.FImm(1.0), x)
		o.Inst("v_add_f32", bump.regs[0], t.regs[0], one.regs[0])
		o.Inst("v_cndmask_b32", dst.regs[lane], t.regs[0], bump.regs[0], p.regs[0])
	}
	return nil
}

// trig scales into revolutions before v_sin/v_cos: the amdgcn transcendental
// unit takes its argument divided by 2*pi, and 1/(2*pi) is an inline constant
// on GFX9 and later (§8.1), so the scaling costs one instruction and no
// literal.
func (b *bodyLowerer) trig(o *amdtx.Body, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	if floatBits(elem) != 32 {
		return fmt.Errorf("sin/cos are implemented for f32 only")
	}
	mn := "v_sin_f32"
	if in.Op == gvir.OpCos {
		mn = "v_cos_f32"
	}
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		t, _ := b.temp(gvir.F32)
		o.Inst("v_mul_f32", t.regs[0], amdtx.Inv2Pi, x)
		o.Inst(mn, dst.regs[lane], t.regs[0])
	}
	return nil
}

func (b *bodyLowerer) simple1(o *amdtx.Body, in *gvir.Instruction, mn string, extra ...amdtx.Operand) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	dst, err := b.define(in.Result, in.Suffix)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		o.Inst(mn, dst.regs[lane], append([]amdtx.Operand{x}, extra...)...)
	}
	return nil
}

func (b *bodyLowerer) copyValue(o *amdtx.Body, dst *value, in *gvir.Instruction) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	n := dwordsOf(elem)
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		x, err := b.src(o, in.Args[0], elem, lane)
		if err != nil {
			return err
		}
		for d := 0; d < n; d++ {
			o.Inst("v_mov_b32", dword(dst.regs[lane], d, n), dwordOperand(x, d, n))
		}
	}
	return nil
}

// compare writes a .lanemask. Signed comparisons on a sub-dword type work on
// sign-extended scratch copies; unsigned ones need none, because the register
// already holds the zero-extended value.
func (b *bodyLowerer) compare(o *amdtx.Body, in *gvir.Instruction, f form, unary bool) error {
	elem := gvir.ElemOrSelf(in.Suffix)
	mn := f.pick(in.Suffix)
	if mn == "" {
		if _, ok := elem.(gvir.PtrType); ok {
			mn = f.i64 // pointer comparisons are 64-bit unsigned ones
		}
	}
	if mn == "" {
		return fmt.Errorf("%s has no comparison form for %s", in.Op, in.Suffix)
	}
	dst, err := b.define(in.Result, boolResult(in.Suffix))
	if err != nil {
		return err
	}
	signed := isSignedCompare(in.Op)
	for lane := 0; lane < laneCount(in.Suffix); lane++ {
		args := make([]amdtx.Operand, 0, 2)
		count := 2
		if unary {
			count = 1
		}
		for i := 0; i < count; i++ {
			s, err := b.src(o, in.Args[i], elem, lane)
			if err != nil {
				return err
			}
			if signed {
				if s, err = b.signExtend(o, s, elem); err != nil {
					return err
				}
			}
			args = append(args, s)
		}
		if unary {
			args = append(args, args[0])
		}
		o.Inst(mn, dst.regs[lane], args...)
	}
	return nil
}

func isSignedCompare(op gvir.Opcode) bool {
	switch op {
	case gvir.OpSlt, gvir.OpSle, gvir.OpSgt, gvir.OpSge:
		return true
	}
	return false
}

// boolResult mirrors ruleBool: a comparison on a vector yields vec[i1,N].
func boolResult(suffix gvir.Type) gvir.Type {
	if v, ok := suffix.(gvir.VecType); ok {
		return gvir.VecType{Elem: gvir.I1, Len: v.Len}
	}
	return gvir.I1
}

// selectOp lowers select. v_cndmask takes the false arm first, and both arms
// are always evaluated (§11.7), so there is nothing to predicate.
func (b *bodyLowerer) selectOp(o *amdtx.Body, in *gvir.Instruction) error {
	t := in.Suffix
	elem := gvir.ElemOrSelf(t)
	n := dwordsOf(elem)
	dst, err := b.define(in.Result, t)
	if err != nil {
		return err
	}
	condVec := false
	if c, ok := b.lookup(identOf(in.Args[0])); ok {
		condVec = gvir.IsPredicateVector(c.typ)
	}
	for lane := 0; lane < laneCount(t); lane++ {
		cl := 0
		if condVec {
			cl = lane
		}
		c, err := b.src(o, in.Args[0], gvir.I1, cl)
		if err != nil {
			return err
		}
		a, err := b.src(o, in.Args[1], elem, lane)
		if err != nil {
			return err
		}
		e, err := b.src(o, in.Args[2], elem, lane)
		if err != nil {
			return err
		}
		for d := 0; d < n; d++ {
			o.Inst("v_cndmask_b32", dword(dst.regs[lane], d, n),
				dwordOperand(e, d, n), dwordOperand(a, d, n), c)
		}
	}
	return nil
}

func identOf(o gvir.Operand) string {
	if o.Kind == gvir.OperandIdent {
		return o.Ident
	}
	return ""
}

func isConversion(op gvir.Opcode) bool {
	switch op {
	case gvir.OpTrunc, gvir.OpSext, gvir.OpZext, gvir.OpFPTrunc, gvir.OpFPExt,
		gvir.OpStoint, gvir.OpUtoint, gvir.OpInttos, gvir.OpInttou, gvir.OpBitcast:
		return true
	}
	return false
}

// convert lowers §11.5. Float-to-int is saturating and total on amdgcn's
// v_cvt_*, mapping NaN to zero and clamping the infinities, which is the §11.5
// table exactly — no clamp sequence is emitted.
func (b *bodyLowerer) convert(o *amdtx.Body, in *gvir.Instruction) error {
	dstType := in.Suffix
	dstElem := gvir.ElemOrSelf(dstType)
	srcVal, err := b.srcValue(o, in.Args[0], dstType)
	if err != nil {
		return err
	}
	srcElem := gvir.ElemOrSelf(srcVal.typ)
	dst, err := b.define(in.Result, dstType)
	if err != nil {
		return err
	}
	for lane := 0; lane < laneCount(dstType); lane++ {
		x := amdtx.Operand(srcVal.reg(lane))
		switch in.Op {
		case gvir.OpTrunc:
			bits := intBits(dstElem)
			if bits >= 32 {
				o.Inst("v_mov_b32", dst.regs[lane], dwordOperand(x, 0, dwordsOf(srcElem)))
				continue
			}
			o.Inst("v_and_b32", dst.regs[lane], amdtx.Imm(subDwordMask(bits)), dwordOperand(x, 0, dwordsOf(srcElem)))

		case gvir.OpZext:
			if intBits(dstElem) == 64 {
				o.Inst("v_mov_b32", dst.regs[lane].Dword(0), x)
				o.Inst("v_mov_b32", dst.regs[lane].Dword(1), amdtx.Imm(0))
				continue
			}
			o.Inst("v_mov_b32", dst.regs[lane], x)

		case gvir.OpSext:
			s, err := b.signExtend(o, x, srcElem)
			if err != nil {
				return err
			}
			if intBits(dstElem) == 64 {
				o.Inst("v_mov_b32", dst.regs[lane].Dword(0), s)
				o.Inst("v_ashrrev_i32", dst.regs[lane].Dword(1), amdtx.Imm(31), s)
				continue
			}
			if intBits(dstElem) < 32 {
				o.Inst("v_and_b32", dst.regs[lane], amdtx.Imm(subDwordMask(intBits(dstElem))), s)
				continue
			}
			o.Inst("v_mov_b32", dst.regs[lane], s)

		case gvir.OpBitcast:
			n := dwordsOf(dstElem)
			for d := 0; d < n; d++ {
				o.Inst("v_mov_b32", dword(dst.regs[lane], d, n), dwordOperand(x, d, n))
			}

		default:
			mn, err := convMnemonic(in.Op, srcElem, dstElem)
			if err != nil {
				return err
			}
			o.Inst(mn, dst.regs[lane], x)
		}
	}
	return nil
}

func convMnemonic(op gvir.Opcode, src, dst gvir.Type) (string, error) {
	sf, df := floatBits(src), floatBits(dst)
	switch op {
	case gvir.OpFPTrunc, gvir.OpFPExt:
		switch {
		case sf == 32 && df == 16:
			return "v_cvt_f16_f32", nil
		case sf == 16 && df == 32:
			return "v_cvt_f32_f16", nil
		case sf == 64 && df == 32:
			return "v_cvt_f32_f64", nil
		case sf == 32 && df == 64:
			return "v_cvt_f64_f32", nil
		}
	case gvir.OpStoint:
		if sf == 32 && intBits(dst) <= 32 {
			return "v_cvt_i32_f32", nil
		}
		if sf == 64 && intBits(dst) <= 32 {
			return "v_cvt_i32_f64", nil
		}
	case gvir.OpUtoint:
		if sf == 32 && intBits(dst) <= 32 {
			return "v_cvt_u32_f32", nil
		}
		if sf == 64 && intBits(dst) <= 32 {
			return "v_cvt_u32_f64", nil
		}
	case gvir.OpInttos:
		if intBits(src) <= 32 && df == 32 {
			return "v_cvt_f32_i32", nil
		}
		if intBits(src) <= 32 && df == 64 {
			return "v_cvt_f64_i32", nil
		}
	case gvir.OpInttou:
		if intBits(src) <= 32 && df == 32 {
			return "v_cvt_f32_u32", nil
		}
		if intBits(src) <= 32 && df == 64 {
			return "v_cvt_f64_u32", nil
		}
	}
	return "", fmt.Errorf("%s from %s to %s is not yet lowered", op, src, dst)
}

// call emits a direct call. AMDTX 1.0 inlines every call site (§3.2), so the
// synthetic result parameter is exact rather than an ABI.
func (b *bodyLowerer) call(o *amdtx.Body, in *gvir.Instruction) error {
	if len(in.Args) == 0 || in.Args[0].Kind != gvir.OperandIdent {
		return fmt.Errorf("call has no callee ident")
	}
	name := in.Args[0].Ident
	callee := b.l.src.FuncByName(name)
	out, ok := b.l.funcs[name]
	if callee == nil || !ok {
		return fmt.Errorf("call to undeclared func %s", name)
	}
	var args []amdtx.Operand
	for i, p := range callee.Params {
		if i+1 >= len(in.Args) {
			return fmt.Errorf("call to %s has too few arguments", name)
		}
		v, err := b.srcValue(o, in.Args[i+1], p.Type)
		if err != nil {
			return err
		}
		for _, r := range v.regs {
			args = append(args, r)
		}
	}
	if !gvir.IsVoid(callee.Ret) && in.Result != "" {
		dst, err := b.define(in.Result, callee.Ret)
		if err != nil {
			return err
		}
		for _, r := range dst.regs {
			args = append(args, r)
		}
	}
	o.Call(out, args...)
	return nil
}