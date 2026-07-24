// frame.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// Calleeavesaved registers this backend preserves, in push order (high to
// low address). rbp is handled separately by the push rbp / mov rbp,rsp
// prologue; these are the rest. Identical under both calling conventions
// — callee-saved register sets happen to coincide between SysV and
// Microsoft x64 for the GPRs this backend uses (rbx, r12-r15).
var CalleeSaved = []Reg{RRBX, RR12, RR13, RR14, RR15}

// SavedRegBytes is the space below saved-rbp occupied by CalleeSaved.
var SavedRegBytes = int64(len(CalleeSaved)) * 8

// Frame lays out one function from high to low address:
//
//	[rbp+48+…]  (windows) or [rbp+16+…] (sysv): incoming stack arguments
//	            (5th+ args on windows, 7th+ on sysv), via LayoutArgs
//	[rbp+8]     return address
//	[rbp+0]     saved rbp
//	[rbp-8]     saved rbx
//	[rbp-16..]  saved r12..r15
//	[rbp-48..]  register save area (variadic functions only, SysV target
//	            only — see the windows-variadic guard in BuildFrame below)
//	[rbp-…]     local slots, Frame.Local bytes, one 8-byte slot per value
type Frame struct {
	slots     map[string]int // value name -> local slot index
	slotType  map[string]vir.Type
	Local     int64          // total local bytes; always ≡ 8 (mod 16)
	paramOff  map[string]int64
	ParamEnd  int64 // offset one past the last named INTEGER stack param (va_start)
	Variadic  bool
	SaveArea  int64 // rbp-relative offset of the GP register save area (variadic, sysv only)
	NamedGP   int   // number of named args that consumed an integer arg reg
	incoming  ArgPlan
}

// BuildFrame assigns every named IR value an 8-byte slot and computes
// parameter offsets via the same LayoutArgs the call site uses. Register
// params are spilled to their slots in the prologue (see encode.go), so the
// rest of isel can read every value uniformly out of a slot.
func BuildFrame(l *Layout, f *vir.Function, order []string, types map[string]vir.Type) (*Frame, error) {
	if f.Variadic && l.OS == "windows" {
		// The variadic mechanism below (48-byte SysV GP register save
		// area, va_start/va_arg's gp_offset/reg_save_area layout in
		// isel_va.go, and the "mov rax, xmmCount" caller-side hint this
		// package emits before a variadic call) is entirely a SysV
		// convention. Windows varargs use the same fixed four-register
		// convention as an ordinary call, with no register-count hint and
		// no callee-side register save area to define — a real
		// implementation is a separate, not-yet-written mechanism, not a
		// variant of this one. Fail loudly rather than silently emit
		// SysV-shaped code that misreads whatever's actually on the stack.
		return nil, todo("variadic functions on windows (needs its own va_list/prologue convention, not the SysV one this backend implements)")
	}

	fr := &Frame{
		slots:    map[string]int{},
		slotType: map[string]vir.Type{},
		paramOff: map[string]int64{},
		Variadic: f.Variadic,
	}

	plan, err := l.LayoutArgs(f.Params, len(f.Params))
	if err != nil {
		return nil, err
	}
	fr.incoming = plan

	// windowsIncomingArgBase/sysvIncomingArgBase (callconv.go) account for
	// the Windows convention's 32-byte caller-reserved shadow space sitting
	// between the return address and the first real stack argument, which
	// SysV has no equivalent of.
	base := int64(sysvIncomingArgBase)
	if l.OS == "windows" {
		base = windowsIncomingArgBase
	}

	// Incoming stack params live above the frame at [rbp+base+off]. Register
	// params get a home local slot they're spilled into by the prologue.
	var namedStackEnd int64
	for i, p := range f.Params {
		s := plan.Slots[i]
		if s.InReg {
			fr.NamedGP++
			continue // spilled into its value slot below
		}
		fr.paramOff[p.Name] = base + s.StackOff
		if end := s.StackOff + s.Bytes; end > namedStackEnd {
			namedStackEnd = end
		}
	}
	// ParamEnd is where the unnamed variadic tail begins on the stack.
	fr.ParamEnd = base + namedStackEnd

	// One 8-byte local slot per named value (params included: a register
	// param's slot is its spill home).
	next := 0
	assign := func(name string, t vir.Type) {
		if _, ok := fr.slots[name]; ok {
			return
		}
		fr.slots[name] = next
		fr.slotType[name] = t
		next++
	}
	for _, p := range f.Params {
		assign(p.Name, types[p.Name])
	}
	for _, name := range order {
		assign(name, types[name])
	}

	localBytes := int64(next) * 8

	// Variadic functions reserve a 48-byte GP register save area below the
	// callee-saved block. (XMM save area omitted — floats are a todo.)
	// Unreachable for windows targets — the guard at the top of this
	// function already rejected f.Variadic && l.OS == "windows".
	if f.Variadic {
		fr.SaveArea = -(8 + SavedRegBytes + 48)
		localBytes += 48
	}

	// Frame.Local must be ≡ 8 (mod 16): after push rbp (rsp→0 mod16) and
	// five callee-saved pushes (−40 → 8 mod16), `sub rsp, Local` lands rsp
	// on a 16-byte boundary iff Local ≡ 8 (mod 16). Identical requirement
	// under both conventions.
	fr.Local = roundUpTo8Mod16(localBytes)
	return fr, nil
}

// Offset returns the [rbp+off] displacement of a value's local slot. Slots
// grow downward starting just below the callee-saved (and save-area) block.
func (fr *Frame) Offset(slot int) int32 {
	base := -(8 + SavedRegBytes)
	if fr.Variadic {
		base -= 48
	}
	return int32(base - int64(slot+1)*8)
}

func (fr *Frame) SlotOf(name string) (int, bool) { s, ok := fr.slots[name]; return s, ok }
func (fr *Frame) ParamStackOff(name string) (int64, bool) {
	o, ok := fr.paramOff[name]
	return o, ok
}

func roundUpTo8Mod16(v int64) int64 {
	// Smallest w ≥ v with w ≡ 8 (mod 16).
	if v <= 8 {
		return 8
	}
	r := v % 16
	switch {
	case r == 8:
		return v
	case r < 8:
		return v + (8 - r)
	default:
		return v + (24 - r)
	}
}