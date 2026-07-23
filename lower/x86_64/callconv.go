// callconv.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// System V AMD64 integer/pointer argument registers, in order. Once these
// six are exhausted, further INTEGER-class args go on the stack.
var IntArgRegs = []Reg{RRDI, RRSI, RRDX, RRCX, RR8, RR9}

// IntRetReg is where a scalar/pointer result is returned.
const IntRetReg = RRAX

// StackAlign is the ABI stack alignment: rsp must be 16-aligned at the
// point of a `call` (so callee entry sees rsp ≡ 8 mod 16).
const StackAlign = 16

// ArgWordBytes: every stack-passed argument occupies a whole number of
// 8-byte eightbytes, in declaration order, no gaps.
const ArgWordBytes = 8

// argClass says how one argument is passed. This backend implements the
// INTEGER and MEMORY classes only; SSE (floats/vectors) is a todo at the
// call sites, and small-struct-in-register classification (splitting a
// ≤16-byte struct into up to two INTEGER eightbytes) is deliberately NOT
// done — byval aggregates take the MEMORY class, i.e. a whole stack copy.
// That is ABI-correct for large structs and a documented non-conformance
// for small ones.
type argClass int

const (
	classInteger argClass = iota // one eightbyte in an int register (or stack)
	classMemory                  // byval struct: real size, passed on the stack
)

// ArgSlot describes where one argument lands.
type ArgSlot struct {
	Class    argClass
	Reg      Reg   // classInteger, if InReg
	InReg    bool  // classInteger passed in a register vs. on the stack
	StackOff int64 // offset from the start of the outgoing arg area (stack cases)
	Bytes    int64 // stack footprint (0 for a register arg)
	ByValOf  string // non-empty: struct name for a classMemory byval copy
}

// ArgPlan is the placement of a whole argument list plus the total stack
// bytes the outgoing (or incoming) area occupies before StackAlign rounding.
type ArgPlan struct {
	Slots      []ArgSlot
	StackBytes int64
}

// LayoutArgs is the single shared rule: assign each parameter to a register
// or a stack offset. params is the CALLEE's declared list; nArgs may exceed
// len(params) for a variadic call's unnamed tail (each tail arg is one
// INTEGER eightbyte on the stack once registers are used up — this backend
// never passes an unnamed arg in an unclassifiable way because floats in the
// tail are a todo at the call site).
//
// Both PlanCall (caller) and BuildFrame (callee) go through here so the two
// sides can never disagree about which arg is in %rdi vs. [rsp+16].
func (l *Layout) LayoutArgs(params []vir.Param, nArgs int) (ArgPlan, error) {
	var plan ArgPlan
	nextReg := 0
	var stackOff int64

	place := func(bytes int64, byval string) error {
		var slot ArgSlot
		if byval != "" {
			// MEMORY class: on the stack, real size rounded to eightbytes.
			slot = ArgSlot{Class: classMemory, ByValOf: byval,
				StackOff: stackOff, Bytes: roundUp(bytes, ArgWordBytes)}
			stackOff += slot.Bytes
			plan.Slots = append(plan.Slots, slot)
			return nil
		}
		if nextReg < len(IntArgRegs) {
			slot = ArgSlot{Class: classInteger, InReg: true, Reg: IntArgRegs[nextReg]}
			nextReg++
		} else {
			slot = ArgSlot{Class: classInteger, StackOff: stackOff, Bytes: ArgWordBytes}
			stackOff += ArgWordBytes
		}
		plan.Slots = append(plan.Slots, slot)
		return nil
	}

	for i := 0; i < nArgs; i++ {
		if i < len(params) && params[i].ByVal != "" {
			s, err := l.Size(vir.StructType{Name: params[i].ByVal})
			if err != nil {
				return plan, err
			}
			if err := place(s, params[i].ByVal); err != nil {
				return plan, err
			}
			continue
		}
		// INTEGER-class scalar/pointer, or an unnamed variadic tail arg.
		if err := place(ArgWordBytes, ""); err != nil {
			return plan, err
		}
	}
	plan.StackBytes = stackOff
	return plan, nil
}

// PlanCall lays out one call's outgoing arguments and returns the plan plus
// the stack reservation rounded up to StackAlign, so a caller doing
// `sub rsp, n` / `call` / `add rsp, n` leaves rsp's alignment unchanged.
func (l *Layout) PlanCall(params []vir.Param, nArgs int) (ArgPlan, int64, error) {
	plan, err := l.LayoutArgs(params, nArgs)
	if err != nil {
		return plan, 0, err
	}
	reserve := roundUp(plan.StackBytes, StackAlign)
	return plan, reserve, nil
}