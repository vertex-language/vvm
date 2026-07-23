// frame.go
package aarch64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// SlotBytes is one local slot. Every named value gets exactly one, whatever
// its width — i64 and ptr are native here, and a narrower value keeps its
// zero-extension invariant across the full eight bytes.
const SlotBytes = 8

// VarargsSaveBytes is the GP register save area: x0-x7, eight eightbytes.
const VarargsSaveBytes = NumIntArgRegs * ArgWordBytes

// MaxSlotOffset is the furthest a slot may sit from the frame pointer. Slots
// are reached as [x29, #off] through the *scaled* unsigned imm12 form, whose
// reach for an 8-byte access is 4095*8. A larger frame is a todo rather than
// a silently wrong offset.
const MaxSlotOffset = 4095 * SlotBytes

// Frame lays out one function. Unlike every sibling backend this one grows
// *upward* from the frame pointer:
//
//	[fp + FrameBytes + VarargsSaveBytes + …]  incoming stack args (variadic)
//	[fp + FrameBytes … +63]                   GP save area x0-x7  (variadic only)
//	[fp + FrameBytes + …]                     incoming stack args (non-variadic)
//	[fp + 16 …]                               local slots, Frame.Local bytes
//	[fp + 8]                                  saved lr  (x30)
//	[fp + 0]                                  saved fp  (x29)   == sp after the prologue
//
// The frame record sits at the *bottom* of the frame rather than the top.
// AAPCS64 fixes the record's contents and requires x29 to point at it, but
// explicitly does not specify where in the frame it lives — and putting it
// lowest is what makes every local offset positive. That matters here in a
// way it does not on x86: a negative [x29, #-off] can only use the unscaled
// signed imm9 (LDUR/STUR) form and would cap a frame at 256 bytes, where the
// positive scaled form reaches 32760.
//
// Every computation happens in x9-x12 and x16/x17 — all caller-saved or
// IP-scratch — so there is nothing to preserve beyond fp/lr. The x86
// backends' unconditional ebx/esi/edi save has no counterpart here, exactly
// as in lower/arm.
type Frame struct {
	Local      uint32 // bytes of local slots
	FrameBytes uint32 // 16 + Local, rounded to StackAlign: the whole record+locals block
	SaveArea   uint32 // VarargsSaveBytes, or 0 under the stack-varargs convention

	Params   ArgLayout
	ParamEnd uint32 // offset from fp at which the unnamed variadic tail begins
	Variadic bool

	// paramNames is indexed in lockstep with Params.Slots: paramNames[i] is
	// the declared name of the parameter that slot i places. The prologue
	// reads it to spill each register-passed parameter into its home slot;
	// keeping it here rather than re-walking f.Params at emit time is what
	// guarantees the two indexings cannot drift.
	paramNames []string

	slots map[string]uint32
	order []string
}

// checkValueType enforces the set of types that may live in a named slot.
// i64 *is* in the native set here — it is just a register — unlike the two
// 32-bit backends. i128 needs a register pair and floats/vectors need an
// FP/SIMD path the encoder does not have; both are todo.
func checkValueType(t vir.Type) error {
	switch x := t.(type) {
	case vir.IntType:
		switch x.Bits {
		case 1, 8, 16, 32, 64:
			return nil
		case 128:
			return todo("i128 values need a register pair")
		}
		return fmt.Errorf("i%d is not a legal value width", x.Bits)
	case vir.PtrType, vir.ValistType:
		return nil
	case vir.FloatType:
		return todo("%s values need an FP/SIMD path", t)
	case vir.VecType:
		return todo("%s values need an FP/SIMD path", t)
	}
	return fmt.Errorf("%s may not be the type of a named value", t)
}

// BuildFrame assigns every named value a slot and computes parameter offsets
// through the same LayoutArgs the call site uses.
func BuildFrame(l *Layout, f *vir.Function, types map[string]vir.Type, order []string, stackVarargs bool) (*Frame, error) {
	fr := &Frame{
		slots:      map[string]uint32{},
		order:      order,
		Variadic:   f.Variadic,
		paramNames: make([]string, len(f.Params)),
	}

	// Local slots start immediately above the frame record.
	off := uint32(16)
	for _, name := range order {
		t, ok := types[name]
		if !ok {
			return nil, fmt.Errorf("value %s has no fixed type", name)
		}
		if err := checkValueType(t); err != nil {
			return nil, fmt.Errorf("value %s: %w", name, err)
		}
		fr.slots[name] = off
		off += SlotBytes
	}
	fr.Local = off - 16

	// FrameBytes covers the record plus the locals and keeps sp 16-aligned,
	// at a cost of at most 8 dead bytes per frame.
	fr.FrameBytes = roundUp(16+fr.Local, StackAlign)

	if f.Variadic && !stackVarargs {
		fr.SaveArea = VarargsSaveBytes
	}

	// Recorded parallel to the slots LayoutArgs is about to produce: both are
	// in declaration order, one entry per declared parameter.
	for i, p := range f.Params {
		fr.paramNames[i] = p.Name
	}

	pl, err := LayoutArgs(l, argDescsForCallee(f.Params), stackVarargs)
	if err != nil {
		return nil, err
	}
	fr.Params = pl

	// Where the unnamed tail begins. Under the register convention the save
	// area sits directly below the incoming stack args, so argument eightbyte
	// i is at ArgBase + 8i whether it arrived in a register or not, and the
	// two formulas agree exactly at the boundary (8*8 == 64 + 0). That
	// uniformity is what buys the one-word valist in isel_va.go.
	//
	// It is computed from the actual layout, never from FrameBytes + 8*(i+1),
	// which is only correct when no preceding parameter is an sret or a
	// stack-passed argument.
	base := fr.FrameBytes
	switch {
	case fr.SaveArea != 0:
		fr.ParamEnd = base + uint32(pl.GPUsed)*ArgWordBytes + pl.StackBytes
	default:
		fr.ParamEnd = base + pl.StackBytes
	}

	if fr.FrameBytes+fr.SaveArea+pl.StackBytes > MaxSlotOffset {
		return nil, todo("frame of %d bytes exceeds the %d-byte scaled-offset reach", fr.FrameBytes, MaxSlotOffset)
	}
	return fr, nil
}

// Offset resolves a named value's slot to a byte offset from fp.
func (fr *Frame) Offset(name string) (int64, error) {
	off, ok := fr.slots[name]
	if !ok {
		return 0, fmt.Errorf("value %s has no frame slot", name)
	}
	return int64(off), nil
}

// ArgBase is the offset from fp of incoming argument eightbyte zero — the
// base of the save area when there is one, and of the incoming stack args
// otherwise.
func (fr *Frame) ArgBase() uint32 { return fr.FrameBytes }

// StackArgBase is the offset from fp at which stack-passed incoming
// arguments begin.
func (fr *Frame) StackArgBase() uint32 { return fr.FrameBytes + fr.SaveArea }