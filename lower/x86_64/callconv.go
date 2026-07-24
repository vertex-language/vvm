// callconv.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// System V AMD64 integer/pointer argument registers, in order. Once these
// six are exhausted, further INTEGER-class args go on the stack. This
// list is also relied on directly by encode.go's variadic GP register-save
// prologue and by selTailCall's indirect-tailcall path in isel_call.go —
// both are SysV-only mechanisms today (see BuildFrame's windows-variadic
// guard in frame.go), so this global stays the fixed six-register SysV
// list rather than becoming OS-aware itself; intArgRegs below is the
// OS-aware selector everything else should call through.
var IntArgRegs = []Reg{RRDI, RRSI, RRDX, RRCX, RR8, RR9}

// IntArgRegsWindows is the Microsoft x64 calling convention's argument
// register list: only four slots, shared positionally across integer and
// floating-point arguments alike (unlike SysV's separate INTEGER/SSE
// counters) — argument index 0 is always rcx (or xmm0), index 1 is always
// rdx (or xmm1), and so on, regardless of type. This backend's existing
// selCall already loads a float argument's bits into the ArgSlot's
// assigned register number and bit-moves them into the same-numbered XMM
// register (see opr.go's RXMM0=RRAX-style aliasing) — that mechanism was
// already positional-by-construction, so it needs no changes here; only
// the register list and its length (four, not six) differ for Windows.
var IntArgRegsWindows = []Reg{RRCX, RRDX, RR8, RR9}

// intArgRegs picks the argument-register list for a target OS. Every
// caller in this file goes through this rather than IntArgRegs directly,
// so a Windows target's four-slot convention is honored everywhere
// LayoutArgs is used — which is every register-argument decision in the
// package except the two SysV-only mechanisms called out on IntArgRegs's
// own doc comment above.
func intArgRegs(os string) []Reg {
	if os == "windows" {
		return IntArgRegsWindows
	}
	return IntArgRegs
}

// IntRetReg is where a scalar/pointer result is returned. Identical under
// both conventions (rax).
const IntRetReg = RRAX

// StackAlign is the ABI stack alignment: rsp must be 16-aligned at the
// point of a `call` (so callee entry sees rsp ≡ 8 mod 16). Identical under
// both conventions.
const StackAlign = 16

// ArgWordBytes: every stack-passed argument occupies a whole number of
// 8-byte eightbytes, in declaration order, no gaps. Identical under both
// conventions.
const ArgWordBytes = 8

// windowsShadowSpace is the 32 bytes of stack space the Microsoft x64
// calling convention requires a caller to reserve immediately above the
// return address on *every* call, regardless of how many arguments are
// actually passed — the callee is permitted (and, per real CRT-generated
// code, commonly does) spill its own register arguments there. SysV has
// no equivalent; a 0-argument or all-register SysV call reserves nothing.
const windowsShadowSpace = 32

// windowsIncomingArgBase is the rbp-relative offset where a Windows
// function's 5th-and-later (stack-passed) incoming argument begins.
// Derivation: at function entry (before this backend's own `push rbp`),
// rsp points at the return address, i.e. entry_rsp = rbp+8 once `push
// rbp; mov rbp,rsp` has run. The caller's shadow space occupies
// [entry_rsp+8, entry_rsp+40) — 32 bytes sitting just above the return
// address — so real stack arguments begin at entry_rsp+40 = rbp+48.
// SysV has no shadow space, so its equivalent base is just
// retaddr+saved-rbp = 16 (see BuildFrame in frame.go, which selects
// between the two via Layout.OS).
const windowsIncomingArgBase = 48

// sysvIncomingArgBase is SysV's equivalent of windowsIncomingArgBase:
// just past the pushed return address and saved rbp, with no shadow
// space to skip over.
const sysvIncomingArgBase = 16

// argClass says how one argument is passed. This backend implements the
// INTEGER and MEMORY classes only; SSE (floats/vectors) is a todo at the
// call sites, and small-struct-in-register classification (splitting a
// ≤16-byte struct into up to two INTEGER eightbytes) is deliberately NOT
// done — byval aggregates take the MEMORY class, i.e. a whole stack copy.
// That is ABI-correct for large structs and a documented non-conformance
// for small ones under SysV. Under the Windows convention, real byval
// structs of any size >8 bytes are actually always passed *by reference*
// (a hidden pointer), never copied inline onto the stack the way this
// MEMORY class does — that divergence isn't handled here either; this
// backend's byval path is a SysV-shaped approximation on both targets.
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
// bytes the outgoing (or incoming) area occupies before StackAlign/shadow-
// space rounding.
type ArgPlan struct {
	Slots      []ArgSlot
	StackBytes int64
}

// LayoutArgs is the single shared rule: assign each parameter to a register
// or a stack offset, using whichever convention l.OS selects. params is the
// CALLEE's declared list; nArgs may exceed len(params) for a variadic
// call's unnamed tail (each tail arg is one INTEGER eightbyte on the stack
// once registers are used up — this backend never passes an unnamed arg in
// an unclassifiable way because floats in the tail are a todo at the call
// site).
//
// Both PlanCall (caller) and BuildFrame (callee) go through here so the two
// sides can never disagree about which arg is in a register vs. on the
// stack — and, since both read l.OS, never disagree about *which*
// convention's registers/offsets to use either.
func (l *Layout) LayoutArgs(params []vir.Param, nArgs int) (ArgPlan, error) {
	regs := intArgRegs(l.OS)
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
		if nextReg < len(regs) {
			slot = ArgSlot{Class: classInteger, InReg: true, Reg: regs[nextReg]}
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
// the stack reservation a caller should `sub rsp, n` before the call and
// `add rsp, n` after, leaving rsp's alignment unchanged. Under the Windows
// convention this is clamped up to windowsShadowSpace even when
// plan.StackBytes is 0 — a ≤4-argument, all-register Windows call still
// must reserve the 32-byte shadow space; SysV has no such minimum.
func (l *Layout) PlanCall(params []vir.Param, nArgs int) (ArgPlan, int64, error) {
	plan, err := l.LayoutArgs(params, nArgs)
	if err != nil {
		return plan, 0, err
	}
	reserve := roundUp(plan.StackBytes, StackAlign)
	if l.OS == "windows" && reserve < windowsShadowSpace {
		reserve = windowsShadowSpace
	}
	return plan, reserve, nil
}