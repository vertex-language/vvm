// value.go
package amdtx

import (
	"fmt"
	"math"
	"strconv"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// The §7.3 Join Convention, realized as virtual registers.
//
// A name binds to a register on first assignment and to the same register on
// every later assignment, in any block. That is the whole of the convention:
// no phi nodes, no loop-carried special case, and no dominance bookkeeping,
// because §7.3 rule 3 already guarantees every read is dominated by an
// assignment.

type value struct {
	name    string
	typ     gvir.Type
	regs    []amdtx.Reg // one per lane; vectors are lane vectors
	space   gvir.AddrSpace
	pointee gvir.Type // best effort, for field.ptr
}

func (v *value) reg(lane int) amdtx.Reg {
	if lane < len(v.regs) {
		return v.regs[lane]
	}
	return v.regs[0]
}

// dword returns the i-th dword of a lane, unsliced when the lane is a single
// register so that %p prints as %p and not %p[0].
func dword(r amdtx.Reg, i, n int) amdtx.Operand {
	if n <= 1 {
		return r
	}
	return r.Dword(i)
}

// define binds name to registers, or returns the existing binding. Rebinding
// at a different type is rejected rather than silently reallocated: the first
// assignment permanently fixes the type, and address space is part of a
// pointer's type (§7.3 rule 2).
func (b *bodyLowerer) define(name string, t gvir.Type) (*value, error) {
	if v, ok := b.vals[name]; ok {
		if !gvir.Equal(v.typ, t) {
			return nil, fmt.Errorf("%s is bound as %s and reassigned as %s; the first assignment fixes "+
				"the type (§7.3)", name, v.typ, t)
		}
		return v, nil
	}
	if !gvir.IsValueType(t) {
		return nil, fmt.Errorf("%s is not a value type and cannot name a binding (§2)", t)
	}
	c, err := laneClass(t)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	n := laneCount(t)
	regs := make([]amdtx.Reg, n)
	if n == 1 {
		regs[0] = b.regs.New(c, name)
	} else {
		for i := 0; i < n; i++ {
			regs[i] = b.regs.New(c, name+"."+strconv.Itoa(i))
		}
	}
	v := &value{name: name, typ: t, regs: regs}
	if sp, ok := spaceOf(t); ok {
		v.space = sp
	}
	b.vals[name] = v
	return v, nil
}

// temp allocates an unnamed value. Temp names carry a '$', which .gvir
// identifiers cannot contain and AMDTX identifiers can, so a temp can never
// collide with a lowered name or with a lane of one.
func (b *bodyLowerer) temp(t gvir.Type) (*value, error) {
	b.ntmp++
	return b.define("t$"+strconv.Itoa(b.ntmp), t)
}

func (b *bodyLowerer) lookup(name string) (*value, bool) {
	v, ok := b.vals[name]
	return v, ok
}

// src materializes one lane of an operand. Literals become registers unless
// they fit an inline constant slot in a 32-bit position: an inline constant is
// free and consumes no instruction-stream space (§8.1), while anything wider
// would occupy the instruction's single literal dword (V18) and is cheaper to
// hold in a register than to reason about.
func (b *bodyLowerer) src(o *amdtx.Body, arg gvir.Operand, t gvir.Type, lane int) (amdtx.Operand, error) {
	switch arg.Kind {
	case gvir.OperandIdent:
		v, ok := b.lookup(arg.Ident)
		if !ok {
			return nil, fmt.Errorf("use of unbound name %s (§7.3)", arg.Ident)
		}
		return v.reg(lane), nil

	case gvir.OperandInt:
		w, err := regWidth(t)
		if err != nil {
			return nil, err
		}
		if w == amdtx.B32 && amdtx.Imm(arg.Int).IsInline() {
			return amdtx.Imm(arg.Int), nil
		}
		v, err := b.materializeInt(o, arg.Int, t)
		if err != nil {
			return nil, err
		}
		return v.reg(0), nil

	case gvir.OperandFloat:
		v, err := b.materializeFloat(o, arg.Float, t)
		if err != nil {
			return nil, err
		}
		return v.reg(0), nil

	case gvir.OperandBool:
		v, err := b.materializeBool(o, arg.Bool)
		if err != nil {
			return nil, err
		}
		return v.reg(0), nil

	case gvir.OperandNull:
		v, err := b.materializeInt(o, 0, t)
		if err != nil {
			return nil, err
		}
		return v.reg(0), nil
	}
	return nil, fmt.Errorf("operand %s is not usable in a value position", arg)
}

// srcValue is src for the whole value rather than one lane, for the cases
// that need every lane of an operand at once.
func (b *bodyLowerer) srcValue(o *amdtx.Body, arg gvir.Operand, t gvir.Type) (*value, error) {
	if arg.Kind == gvir.OperandIdent {
		v, ok := b.lookup(arg.Ident)
		if !ok {
			return nil, fmt.Errorf("use of unbound name %s (§7.3)", arg.Ident)
		}
		return v, nil
	}
	switch arg.Kind {
	case gvir.OperandInt:
		return b.materializeInt(o, arg.Int, t)
	case gvir.OperandFloat:
		return b.materializeFloat(o, arg.Float, t)
	case gvir.OperandBool:
		return b.materializeBool(o, arg.Bool)
	case gvir.OperandNull:
		return b.materializeInt(o, 0, t)
	}
	return nil, fmt.Errorf("operand %s is not usable in a value position", arg)
}

func (b *bodyLowerer) materializeInt(o *amdtx.Body, x int64, t gvir.Type) (*value, error) {
	scalar := gvir.ElemOrSelf(t)
	v, err := b.temp(scalar)
	if err != nil {
		return nil, err
	}
	n := dwordsOf(scalar)
	o.Inst("v_mov_b32", dword(v.regs[0], 0, n), amdtx.Imm(int64(uint32(x))))
	if n == 2 {
		o.Inst("v_mov_b32", dword(v.regs[0], 1, n), amdtx.Imm(int64(uint32(uint64(x)>>32))))
	}
	return v, nil
}

func (b *bodyLowerer) materializeFloat(o *amdtx.Body, f float64, t gvir.Type) (*value, error) {
	scalar := gvir.ElemOrSelf(t)
	v, err := b.temp(scalar)
	if err != nil {
		return nil, err
	}
	switch floatBits(scalar) {
	case 16:
		// f16 and bf16 are 16-bit patterns in the low half of a .b32
		// register, so the literal is the pattern, not a float.
		bits, err := halfBits(f, gvir.ElemOrSelf(scalar))
		if err != nil {
			return nil, err
		}
		o.Inst("v_mov_b32", v.regs[0], amdtx.Imm(int64(bits)))
	case 32:
		o.Inst("v_mov_b32", v.regs[0], amdtx.FImm(f))
	case 64:
		bits := math.Float64bits(f)
		o.Inst("v_mov_b32", v.regs[0].Dword(0), amdtx.Imm(int64(uint32(bits))))
		o.Inst("v_mov_b32", v.regs[0].Dword(1), amdtx.Imm(int64(uint32(bits>>32))))
	default:
		return nil, fmt.Errorf("%s is not a float type", scalar)
	}
	return v, nil
}

// materializeBool builds an i1 constant. An i1 is a .lanemask, so true is the
// all-ones mask and false is zero; the width follows .wave.
func (b *bodyLowerer) materializeBool(o *amdtx.Body, x bool) (*value, error) {
	v, err := b.temp(gvir.I1)
	if err != nil {
		return nil, err
	}
	imm := amdtx.Imm(0)
	if x {
		imm = amdtx.Imm(-1)
	}
	o.Inst("s_mov_"+b.l.maskSuffix(), v.regs[0], imm)
	return v, nil
}

// halfBits is the 16-bit pattern of a float literal. f16 and bf16 are both 16
// bits with different exponent and mantissa splits, so Brain is part of the
// question (§4.1).
func halfBits(f float64, t gvir.Type) (uint32, error) {
	ft, ok := t.(gvir.FloatType)
	if !ok || ft.Bits != 16 {
		return 0, fmt.Errorf("%s is not a 16-bit float type", t)
	}
	if ft.Brain {
		// bf16 is the high half of the f32 pattern, round-to-nearest-even.
		bits := math.Float32bits(float32(f))
		round := (bits >> 16) & 1
		return (bits + 0x7fff + round) >> 16, nil
	}
	return 0, fmt.Errorf("f16 literals are not yet lowered; the bit pattern needs an f32->f16 " +
		"round-to-nearest-even conversion this package does not implement")
}