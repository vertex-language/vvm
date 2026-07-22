// frame.go
package x86

import "github.com/vertex-language/vvm/ir/vir"

// SavedRegBytes is how much the prologue pushes below the saved ebp:
// ebx, esi and edi, in that order. Local slots start below them.
const SavedRegBytes = 12

// Frame describes one function's stack layout, from high address to low:
//
//	[ebp+8+…]  incoming arguments   (the caller's, laid out by LayoutArgs)
//	[ebp+4]    return address       (the caller's)
//	[ebp+0]    saved ebp
//	[ebp-4]    saved ebx
//	[ebp-8]    saved esi
//	[ebp-12]   saved edi
//	[ebp-16…]  local slots, Local bytes, one per named value
type Frame struct {
	off      map[string]int32
	paramEnd map[string]int32

	// Local is the byte count the prologue subtracts from esp. It is
	// always ≡ 12 (mod 16) — see alignLocal — so it is generally larger
	// than the slots strictly need.
	Local int32

	// ArgBytes is the unpadded size of the incoming named-argument area.
	// A tailcall may reuse this frame only if its own outgoing arguments
	// fit within it.
	ArgBytes int32
}

func (fr *Frame) Offset(name string) (int32, bool) {
	off, ok := fr.off[name]
	return off, ok
}

// ParamEnd returns the ebp-relative offset one byte past the named
// incoming parameter — where the next argument begins.
//
// va_start's lowering wants exactly this for its last_named operand, and
// must not recompute it as ParamBase+4*(i+1): that formula is only
// correct when no preceding parameter is byval, and silently wrong when
// one is.
func (fr *Frame) ParamEnd(name string) (int32, bool) {
	off, ok := fr.paramEnd[name]
	return off, ok
}

// VarargsBase is the ebp-relative offset of the first unnamed argument of
// a variadic function — equivalently, ParamEnd of its final parameter.
func (fr *Frame) VarargsBase() int32 { return ParamBase + fr.ArgBytes }

// BuildFrame assigns a stack slot to every named value the instruction
// stream references and computes incoming-parameter offsets.
//
// The parameter half goes through LayoutArgs, the same routine PlanCall
// uses at the call site, so a byval parameter's real size shifts every
// later parameter identically on both sides. structSize resolves a
// byval[S] struct name to its size; pass Layout.ByValSize.
func BuildFrame(f *vir.Function, insts []Inst, structSize func(name string) (int, error)) (*Frame, error) {
	fr := &Frame{off: map[string]int32{}, paramEnd: map[string]int32{}}

	slots, argBytes, err := LayoutArgs(f.Params, len(f.Params), structSize)
	if err != nil {
		return nil, err
	}
	fr.ArgBytes = int32(argBytes)
	for i, p := range f.Params {
		fr.off[p.Name] = ParamBase + int32(slots[i].Offset)
		fr.paramEnd[p.Name] = ParamBase + int32(slots[i].Offset+slots[i].Size)
	}

	n := int32(0)
	assign := func(o Opr) {
		if o.Kind != OSlot {
			return
		}
		if _, ok := fr.off[o.Slot]; ok {
			return
		}
		n++
		fr.off[o.Slot] = -(SavedRegBytes + ArgWordBytes*n)
	}
	for _, in := range insts {
		assign(in.D)
		assign(in.S)
	}
	fr.Local = alignLocal(ArgWordBytes * n)
	return fr, nil
}

// alignLocal rounds the local area up so esp lands on a StackAlign
// boundary at the bottom of the frame.
//
// The Intel386 psABI leaves esp ≡ 12 (mod 16) at a function's entry
// point, since the caller aligned to 16 and the call pushed four bytes.
// The prologue then pushes ebp plus three callee-saved registers — 16
// bytes, no net change mod 16 — so esp is still ≡ 12 when `sub esp,
// Local` executes. Local ≡ 12 (mod 16) therefore lands the frame bottom
// on ≡ 0, which is where esp has to be when a call instruction executes.
//
// The cost is at most 12 bytes of dead stack per frame, including in leaf
// functions that never call anything. Making it conditional on "does this
// function contain a call" would save that, at the price of a rule whose
// correctness depends on a fact computed elsewhere — a bad trade for
// twelve bytes.
func alignLocal(n int32) int32 {
	return int32(roundUp(int(n)+ArgWordBytes, StackAlign)) - ArgWordBytes
}