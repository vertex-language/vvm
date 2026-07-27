// callconv.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

const (
	// ArgWordBytes is the granule of the argument area: every argument
	// occupies a whole number of 4-byte words.
	ArgWordBytes = 4
	// StackAlign is AAPCS's requirement that sp be 8-byte aligned at every
	// public interface. Unlike x86-64's 16, this is 8 — and unlike IA-32's
	// 16-for-SSE's-sake, it is a real requirement of the standard rather
	// than a concession to what we might call.
	StackAlign = 8
	// NumArgRegs is how many core registers carry arguments before the
	// rest spill to the stack.
	NumArgRegs = 4
)

// IntArgRegs is the core argument sequence, in declaration order.
var IntArgRegs = [NumArgRegs]Reg{R0, R1, R2, R3}

// IntRetReg carries a scalar/pointer result back.
const IntRetReg = R0

// Arg describes one argument to be placed: its type, and whether it is a
// byval aggregate (in which case Type is the struct type).
type Arg struct {
	Type  vir.Type
	ByVal bool
}

// ArgSlot is where one argument lives. A byval aggregate can legally
// straddle the register/stack boundary (AAPCS splits it word-wise), so
// both Regs and Stack may be non-empty at once; callers that cannot
// handle that check Split.
type ArgSlot struct {
	Bytes uint32 // total footprint, a multiple of ArgWordBytes
	Regs  []Reg  // core registers holding the leading words
	Off   uint32 // stack offset of the trailing words, within the arg area
	Stack uint32 // bytes passed on the stack
	ByVal bool
}

func (s ArgSlot) Split() bool { return len(s.Regs) > 0 && s.Stack > 0 }

// LayoutArgs is the single argument-placement rule, used by PlanCall on
// the caller side and BuildFrame on the callee side so the two can never
// disagree about whether argument 3 is in r3 or at [sp+0]. It returns the
// slots, the number of core-register words consumed, and the stack bytes
// consumed (unrounded).
func LayoutArgs(l *Layout, args []Arg) (slots []ArgSlot, regWords uint32, stackBytes uint32, err error) {
	slots = make([]ArgSlot, len(args))
	for i, a := range args {
		size, err := l.Size(a.Type)
		if err != nil {
			return nil, 0, 0, err
		}
		align, err := l.Align(a.Type)
		if err != nil {
			return nil, 0, 0, err
		}
		if !a.ByVal && size > ArgWordBytes {
			// i64/f64/vec by value in a register pair or on the stack:
			// the footprint below is right, but nothing lowers such a
			// value yet, so the use sites reject it.
			size = roundUp(size, ArgWordBytes)
		}
		words := roundUp(size, ArgWordBytes) / ArgWordBytes
		if words == 0 {
			words = 1
		}
		// An 8-byte-aligned fundamental type starts at an even register
		// and an 8-byte stack offset (AAPCS §6.1.2 stage C.3/C.4).
		if align >= 8 {
			regWords = roundUp(regWords, 2)
			stackBytes = roundUp(stackBytes, 8)
		}

		s := ArgSlot{Bytes: words * ArgWordBytes, ByVal: a.ByVal}
		for w := uint32(0); w < words; w++ {
			if regWords < NumArgRegs && stackBytes == 0 {
				s.Regs = append(s.Regs, IntArgRegs[regWords])
				regWords++
				continue
			}
			if s.Stack == 0 {
				s.Off = stackBytes
			}
			s.Stack += ArgWordBytes
			stackBytes += ArgWordBytes
		}
		slots[i] = s
	}
	return slots, regWords, stackBytes, nil
}

// PlanCall lays out a call site's arguments and returns the stack
// reservation, rounded up to StackAlign — not the sum of the slot sizes —
// so a caller doing sub sp / bl / add sp leaves sp's alignment exactly as
// it found it.
func PlanCall(l *Layout, args []Arg) ([]ArgSlot, uint32, error) {
	slots, _, stack, err := LayoutArgs(l, args)
	if err != nil {
		return nil, 0, err
	}
	return slots, roundUp(stack, StackAlign), nil
}

// callArgs builds the Arg list for a call from a callee's declared
// parameters plus any unnamed variadic tail. Each unnamed argument gets
// one flat word: AAPCS passes variadic arguments in the core registers and
// on the stack under the base standard, never in VFP registers, so there
// is no separate variadic placement rule to implement.
func callArgs(l *Layout, params []vir.Param, tail []vir.Type) ([]Arg, error) {
	out := make([]Arg, 0, len(params)+len(tail))
	for _, p := range params {
		if p.ByVal != "" {
			out = append(out, Arg{Type: vir.StructType{Name: p.ByVal}, ByVal: true})
			continue
		}
		out = append(out, Arg{Type: p.Type})
	}
	for _, t := range tail {
		if t == nil {
			return nil, fmt.Errorf("variadic argument has no fixed type")
		}
		out = append(out, Arg{Type: t})
	}
	return out, nil
}