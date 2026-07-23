// frame.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// SavedRegBytes is what the prologue's push {fp, lr} costs. This backend
// keeps every named value in a frame slot and computes in r0-r3/r12, all
// of which are caller-saved, so there is nothing else to preserve — the
// x86 backends' unconditional save of ebx/esi/edi has no counterpart here.
const SavedRegBytes = 8

// VarargSaveBytes is the r0-r3 spill area a variadic function's prologue
// pushes *before* saving fp/lr. Placing it there is the whole trick behind
// this backend's one-word va_list: the four saved words end up directly
// below the incoming stack arguments, so argument word i lives at
// fp+8+4i whether it arrived in a register or on the stack, and va_start
// is a single add.
const VarargSaveBytes = 16

// Frame lays out one function from high to low address:
//
//	[fp+8+16+…]  incoming stack arguments      (variadic)
//	[fp+8 … +23] r0-r3 vararg save area        (variadic only)
//	[fp+8+…]     incoming stack arguments      (non-variadic)
//	[fp+4]       saved lr
//	[fp+0]       saved fp
//	[fp-4 …]     local slots, Frame.Local bytes, one 4-byte slot per value
type Frame struct {
	Variadic bool
	Local    uint32
	ArgBase  int32 // fp-relative offset of the first incoming stack-arg word
	ParamEnd int32 // fp-relative offset one past the last named argument word
	Params   []ArgSlot
	slots    map[string]int32
	order    []string
}

// Offset returns a named value's fp-relative slot offset.
func (f *Frame) Offset(name string) (int32, error) {
	off, ok := f.slots[name]
	if !ok {
		return 0, fmt.Errorf("value %q has no frame slot", name)
	}
	return off, nil
}

// BuildFrame assigns every named value a 4-byte slot and computes the
// incoming-argument offsets via the same LayoutArgs a call site uses.
func BuildFrame(l *Layout, f *vir.Function, order []string, types map[string]vir.Type) (*Frame, error) {
	args, err := callArgs(l, f.Params, nil)
	if err != nil {
		return nil, err
	}
	slots, regWords, stackBytes, err := LayoutArgs(l, args)
	if err != nil {
		return nil, err
	}

	fr := &Frame{
		Variadic: f.Variadic,
		Params:   slots,
		slots:    map[string]int32{},
		order:    order,
	}
	fr.ArgBase = SavedRegBytes
	if f.Variadic {
		fr.ArgBase += VarargSaveBytes
	}
	// Named and unnamed argument words are contiguous from fp+8, so the
	// end of the named ones is a single formula rather than
	// ArgBase + 4*(i+1) — which would be wrong the moment a preceding
	// parameter is byval or 8-byte aligned.
	fr.ParamEnd = SavedRegBytes + int32(regWords*ArgWordBytes) + int32(stackBytes)

	for i, name := range order {
		if err := checkValueType(types[name]); err != nil {
			return nil, fmt.Errorf("value %s: %w", name, err)
		}
		fr.slots[name] = -4 * int32(i+1)
	}
	fr.Local = roundUp(uint32(len(order))*4, StackAlign)
	// Every slot is reached as [fp, #-off], whose immediate field is 12
	// bits of magnitude plus a sign bit.
	if fr.Local > 4092 {
		return nil, todo("frame of %d bytes exceeds the 12-bit [fp, #-off] displacement", fr.Local)
	}
	return fr, nil
}

// checkValueType is the set of types this backend can hold in a slot.
func checkValueType(t vir.Type) error {
	switch x := t.(type) {
	case vir.IntType:
		switch x.Bits {
		case 1, 8, 16, 32:
			return nil
		}
		return todo("i%d values (needs a register pair)", x.Bits)
	case vir.PtrType, vir.ValistType:
		return nil
	case vir.FloatType:
		return todo("f%d values (needs a VFP path)", x.Bits)
	case vir.VecType:
		return todo("vector values (needs a NEON path)")
	case nil:
		return fmt.Errorf("value has no fixed type")
	}
	return fmt.Errorf("%s cannot name a value", t)
}