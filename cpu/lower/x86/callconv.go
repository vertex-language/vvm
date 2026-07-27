// callconv.go
package x86

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// The layout facts both sides of a call agree on.
const (
	// ArgWordBytes is the granularity every stack argument is padded to.
	// An i8, an i16, an i32 and a ptr each take exactly one word
	// regardless of declared width, which is what lets the call site and
	// the callee agree on offsets without sharing per-argument type
	// information.
	ArgWordBytes = 4

	// ParamBase is the ebp-relative offset of the incoming argument
	// area: [ebp+0] holds the saved ebp and [ebp+4] the return address.
	ParamBase = 8

	// StackAlign is the boundary esp must sit on immediately before a
	// call instruction executes. The Intel386 psABI states it as
	// "(%esp + 4) is a multiple of 16 when control is transferred to the
	// function entry point" — i.e. the caller aligns to 16, then the call
	// pushes four bytes of return address.
	//
	// Nothing this backend emits for itself needs the alignment: there is
	// no SSE codegen here and no value wider than four bytes in a slot.
	// It matters entirely for what this code *calls*. A libc built with
	// any modern compiler will use movaps on its own stack frame, and
	// movaps on a misaligned address faults. Getting this wrong produces
	// a crash inside printf that looks nothing like a codegen bug.
	StackAlign = 16
)

// ArgSlot describes where one argument sits in the argument area.
type ArgSlot struct {
	// Offset is the byte offset from the base of the argument area.
	Offset int
	// Size is how many bytes the argument occupies — always a multiple
	// of ArgWordBytes. A byval copy should move exactly the struct's
	// real size and may leave the tail padding untouched.
	Size int
	// ByVal names the struct when this argument is passed by value,
	// otherwise "".
	ByVal string
}

// LayoutArgs assigns argument-area offsets to the first n arguments of a
// call, or to a callee's whole parameter list.
//
// Every argument occupies a whole number of 4-byte words, in declaration
// order, with no gaps. A byval[S] argument is the sole exception to the
// flat-one-word rule: it takes its struct's real size rounded up to 4,
// matching the Intel386 psABI's treatment of structs passed on the stack,
// and making the copy a plain rep movsb into a word-aligned destination.
//
// params supplies the byval information. Arguments past len(params) — a
// variadic call's unnamed tail — have no vir.Param to consult and get one
// flat word each, which is correct for every type this backend can hold
// in a value slot.
//
// This is the single implementation of the layout. PlanCall (caller side)
// and BuildFrame (callee side) both route through it, so the two cannot
// drift apart. They had: PlanCall expanded byval to its real size while
// BuildFrame gave every parameter a flat four bytes at [ebp+8+4*i], which
// silently misplaced every parameter declared after a byval one.
func LayoutArgs(params []vir.Param, n int, structSize func(name string) (int, error)) ([]ArgSlot, int, error) {
	if n < 0 {
		return nil, 0, fmt.Errorf("callconv: negative argument count %d", n)
	}
	slots := make([]ArgSlot, n)
	total := 0
	for i := 0; i < n; i++ {
		byval := ""
		if i < len(params) {
			byval = params[i].ByVal
		}
		size := ArgWordBytes
		if byval != "" {
			sz, err := structSize(byval)
			if err != nil {
				return nil, 0, err
			}
			if sz < 0 {
				return nil, 0, fmt.Errorf("callconv: byval struct %q has negative size %d", byval, sz)
			}
			size = roundUp(sz, ArgWordBytes)
		}
		slots[i] = ArgSlot{Offset: total, Size: size, ByVal: byval}
		total += size
	}
	return slots, total, nil
}

// PlanCall lays out a call's outgoing arguments.
//
// The returned byte count is the size of the area to reserve, rounded up
// to StackAlign — not the sum of the slot sizes. A caller that reserves
// it with one `sub esp, n` before the call and releases it with the
// matching `add esp, n` afterwards therefore leaves esp's alignment
// exactly as it found it, which combined with BuildFrame's aligned Local
// keeps esp on a 16-byte boundary at every call site in the function.
//
// Argument positions come from the slots, never from the total, so the
// padding is invisible to anything that writes an argument.
func PlanCall(params []vir.Param, argCount int, structSize func(name string) (int, error)) ([]ArgSlot, int, error) {
	slots, total, err := LayoutArgs(params, argCount, structSize)
	if err != nil {
		return nil, 0, err
	}
	return slots, roundUp(total, StackAlign), nil
}