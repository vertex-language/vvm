// callconv.go
package aarch64

import (
	"github.com/vertex-language/vvm/ir/vir"
	encoder "github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// AAPCS64's shape, as far as this backend implements it.
const (
	// ArgWordBytes is one eightbyte: every stack-passed argument occupies a
	// whole number of these.
	ArgWordBytes = 8

	// StackAlign is 16. Unlike x86's 16 this is not a concession to vector
	// loads: AArch64 faults on a sp-relative access when sp is misaligned,
	// so it is an invariant of the machine, not of the callee.
	StackAlign = 16

	// NumIntArgRegs is the eight-register integer argument window.
	NumIntArgRegs = 8

	// NumFPArgRegs is the eight-register FP argument window.
	NumFPArgRegs = 8
)

// IntArgRegs is the integer/pointer argument window, in declaration order.
var IntArgRegs = [NumIntArgRegs]encoder.Reg{
	encoder.R0, encoder.R1, encoder.R2, encoder.R3,
	encoder.R4, encoder.R5, encoder.R6, encoder.R7,
}

// FPArgRegs is the float/SIMD argument window, in declaration order.
var FPArgRegs = [NumFPArgRegs]encoder.Reg{
	encoder.R0, encoder.R1, encoder.R2, encoder.R3,
	encoder.R4, encoder.R5, encoder.R6, encoder.R7,
}

const (
	// IntRetReg carries a scalar/pointer result back.
	IntRetReg = encoder.R0

	// FPRetReg carries a float result back.
	FPRetReg = encoder.R0

	// IndirectResultReg is x8, AAPCS64's dedicated indirect-result register:
	// an sret[S] pointer travels there and consumes no argument register.
	IndirectResultReg = encoder.R8
)

// ArgClass says where one argument lives.
type ArgClass byte

const (
	ClassReg      ArgClass = iota // in Reg
	ClassFPReg                    // in an FP/SIMD Reg
	ClassStack                    // at Off in the argument area
	ClassIndirect                 // the sret pointer, in x8
)

// ArgSlot describes one argument's placement.
type ArgSlot struct {
	Class ArgClass
	Reg   encoder.Reg
	Off   uint32 // ClassStack: byte offset from the argument area's base
	Bytes uint32
}

// ArgDesc is one argument as either side sees it. The callee builds these
// from its declared params; the caller builds them from the callee's params
// plus the actual operand types of any unnamed variadic tail.
type ArgDesc struct {
	Type  vir.Type
	ByVal string // struct name for byval[S], "" otherwise
	SRet  string // struct name for sret[S], "" otherwise
	Named bool   // false for a variadic call's unnamed tail
}

// ArgLayout is the whole placement decision for one signature.
type ArgLayout struct {
	Slots      []ArgSlot
	StackBytes uint32 // unrounded footprint of the stack-passed arguments
	GPUsed     int    // integer argument registers consumed
	FPUsed     int    // FP argument registers consumed
}

// LayoutArgs is the single, shared argument-placement rule, used by PlanCall
// (caller side) and BuildFrame (callee side) so the two can never drift apart
// about which argument lives in x3 versus [sp, #8].
//
// Two conventions live here, selected by index.stackVarargs and passed in as
// stackVarargs:
//
//   - The base standard passes named and unnamed arguments identically, in
//     x0-x7 then on the stack. va_start then needs a register save area.
//   - The `aapcs64` ABI token (§7.1: "AArch64 variant with stack-passed
//     variadics") and every Mach-O target pass the *unnamed* tail entirely on
//     the stack. Named parameters are unaffected, and no save area exists.
//
// Both are real conventions on real hardware, so this is a target fact rather
// than a choice, and it is settled once, here.
func LayoutArgs(l *Layout, args []ArgDesc, stackVarargs bool) (ArgLayout, error) {
	var out ArgLayout
	out.Slots = make([]ArgSlot, len(args))

	for i, a := range args {
		switch {
		case a.SRet != "":
			out.Slots[i] = ArgSlot{Class: ClassIndirect, Reg: IndirectResultReg, Bytes: ArgWordBytes}
			continue

		case a.ByVal != "":
			// AAPCS64 splits a composite two ways: <=16 bytes goes in up to
			// two consecutive argument registers, and anything larger is
			// copied to caller-allocated memory and *replaced by a pointer*
			// to the copy — not laid flat on the stack the way SysV x86-64
			// does it. Neither path is implemented; the hook is here so
			// adding them does not reshape the layout.
			sz, err := l.Size(vir.StructType{Name: a.ByVal})
			if err != nil {
				return out, err
			}
			if sz <= 16 {
				return out, todo("byval[%s] (%d bytes) needs AAPCS64 register classification", a.ByVal, sz)
			}
			return out, todo("byval[%s] (%d bytes) needs a caller-allocated copy passed indirectly", a.ByVal, sz)

		case vir.IsFloat(a.Type):
			toStack := !a.Named && stackVarargs
			if !toStack && out.FPUsed < NumFPArgRegs {
				out.Slots[i] = ArgSlot{Class: ClassFPReg, Reg: FPArgRegs[out.FPUsed], Bytes: ArgWordBytes}
				out.FPUsed++
				continue
			}
			out.Slots[i] = ArgSlot{Class: ClassStack, Off: out.StackBytes, Bytes: ArgWordBytes}
			out.StackBytes += ArgWordBytes
			continue

		case vir.IsVec(a.Type):
			return out, todo("%s argument needs the SIMD&FP register class", a.Type)
		}

		toStack := !a.Named && stackVarargs
		if !toStack && out.GPUsed < NumIntArgRegs {
			out.Slots[i] = ArgSlot{Class: ClassReg, Reg: IntArgRegs[out.GPUsed], Bytes: ArgWordBytes}
			out.GPUsed++
			continue
		}
		out.Slots[i] = ArgSlot{Class: ClassStack, Off: out.StackBytes, Bytes: ArgWordBytes}
		out.StackBytes += ArgWordBytes
	}
	return out, nil
}

// CallPlan is a call site's placement decision plus the stack it must reserve.
type CallPlan struct {
	ArgLayout
	Reserve uint32 // StackBytes rounded to StackAlign
}

// PlanCall rounds the outgoing reservation up to StackAlign — not the sum of
// slot sizes — so a caller doing `sub sp, #n` / `bl` / `add sp, #n` leaves
// sp's alignment exactly as it found it. On this machine that is not a
// courtesy to the callee: sp is unusable as a base address while misaligned.
func PlanCall(l *Layout, args []ArgDesc, stackVarargs bool) (CallPlan, error) {
	al, err := LayoutArgs(l, args, stackVarargs)
	if err != nil {
		return CallPlan{}, err
	}
	return CallPlan{ArgLayout: al, Reserve: roundUp(al.StackBytes, StackAlign)}, nil
}

// argDescsForCallee builds the callee-side descriptor list from a declared
// parameter list.
func argDescsForCallee(params []vir.Param) []ArgDesc {
	out := make([]ArgDesc, len(params))
	for i, p := range params {
		out[i] = ArgDesc{Type: p.Type, ByVal: p.ByVal, SRet: p.SRet, Named: true}
	}
	return out
}