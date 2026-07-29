// types.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/amdtx"
	"github.com/vertex-language/vvm/ir/gvir"
)

// The .gvir type system carries semantic types; AMDTX carries bit widths and
// leaves interpretation to the mnemonic (§5). This file is the whole of that
// translation, plus the helpers the zero-extension invariant needs.

// laneCount is the number of registers a value occupies. Vectors are lane
// vectors: amdgcn has no vector registers, so vec[T,N] is N registers and the
// padding lane of a width-3 vector is never materialized (§4.4).
func laneCount(t gvir.Type) int {
	if v, ok := t.(gvir.VecType); ok {
		return v.Len
	}
	return 1
}

// regWidth is the register width one lane of t occupies. A .lanemask carries
// no width of its own — it takes one from .wave (§7.4) — so i1 and submask
// report NoWidth.
func regWidth(t gvir.Type) (amdtx.Width, error) {
	switch x := gvir.ElemOrSelf(t).(type) {
	case gvir.IntType:
		switch {
		case x.Bits == 1:
			return amdtx.NoWidth, nil
		case x.Bits <= 32:
			return amdtx.B32, nil
		case x.Bits == 64:
			return amdtx.B64, nil
		}
	case gvir.FloatType:
		if x.Bits == 64 {
			return amdtx.B64, nil
		}
		return amdtx.B32, nil
	case gvir.PtrType:
		if x.Space == "" {
			return amdtx.NoWidth, fmt.Errorf("the bare ptr suffix word is not a value type")
		}
		// Every pointer is .b64 in a register, including group and private:
		// §6.3 makes a stored pointer 8 bytes on every backend, and the LDS
		// and scratch address operands take the low dword slice.
		return amdtx.B64, nil
	case gvir.SubmaskType:
		return amdtx.NoWidth, nil
	}
	return amdtx.NoWidth, fmt.Errorf("%s has no register representation", t)
}

// laneClass is the declared register class of one lane of t. Everything is a
// VGPR; i1 and submask are .lanemask, which lowers to .sgpr.b32 under .wave 32
// and .sgpr.b64 under .wave 64 (§7.4).
func laneClass(t gvir.Type) (amdtx.RegClass, error) {
	e := gvir.ElemOrSelf(t)
	if gvir.IsBool(e) || gvir.IsSubmask(e) {
		return amdtx.Lane, nil
	}
	w, err := regWidth(e)
	if err != nil {
		return amdtx.RegClass{}, err
	}
	return amdtx.Vgpr(w), nil
}

// dwordsOf is the register-tuple size of one lane.
func dwordsOf(t gvir.Type) int {
	w, err := regWidth(t)
	if err != nil || w == amdtx.NoWidth {
		return 1
	}
	return w.Dwords()
}

// intBits reports the declared width of an integer type, or 0.
func intBits(t gvir.Type) int {
	if x, ok := gvir.ElemOrSelf(t).(gvir.IntType); ok {
		return x.Bits
	}
	return 0
}

func floatBits(t gvir.Type) int {
	if x, ok := gvir.ElemOrSelf(t).(gvir.FloatType); ok {
		return x.Bits
	}
	return 0
}

// subDword reports whether t needs the zero-extension fixups: an i8 or i16
// lives zero-extended in a .b32 register, so a wrapping operation on it must
// be masked back down.
func subDword(t gvir.Type) bool {
	n := intBits(t)
	return n == 8 || n == 16
}

func subDwordMask(bits int) int64 { return int64(1)<<uint(bits) - 1 }

// spaceOf returns the address space of a pointer type.
func spaceOf(t gvir.Type) (gvir.AddrSpace, bool) {
	p, ok := t.(gvir.PtrType)
	if !ok || p.Space == "" {
		return "", false
	}
	return p.Space, true
}

// amdSpace maps a .gvir address space onto an AMDTX one. There is no
// .generic: .gvir has no generic pointer, so no aperture dispatch is ever
// needed (§1, §5).
func amdSpace(s gvir.AddrSpace) (amdtx.Space, error) {
	switch s {
	case gvir.SpaceGlobal:
		return amdtx.Global, nil
	case gvir.SpaceConstant:
		return amdtx.Constant, nil
	case gvir.SpaceGroup:
		return amdtx.Shared, nil
	case gvir.SpacePrivate:
		return amdtx.Private, nil
	}
	return amdtx.NoSpace, fmt.Errorf("unknown address space %q", s)
}

// memPrefix is the AMDTX mnemonic family that addresses a space. Constant
// space uses the vector path here: scalarizing uniform loads onto s_load is a
// todo, and s_load appears only in the kernarg prologue.
func memPrefix(s gvir.AddrSpace) (string, error) {
	switch s {
	case gvir.SpaceGlobal, gvir.SpaceConstant:
		return "global", nil
	case gvir.SpaceGroup:
		return "ds", nil
	case gvir.SpacePrivate:
		return "scratch", nil
	}
	return "", fmt.Errorf("unknown address space %q", s)
}

// accessWidth is the AMDTX width suffix a load or store of t carries. It is
// derived from the data register, which is what makes V9 hold by
// construction; sub-dword accesses carry no _bN suffix at all and use the
// typed spellings instead.
func accessWidth(t gvir.Type) (amdtx.Width, error) { return regWidth(t) }

// memSuffix names the sub-dword mnemonic tail for a load or a store. amdgcn
// spells sub-dword loads by signedness (global_load_u8) and sub-dword stores
// by width (global_store_b8); neither carries a _bN suffix, so V9 does not
// apply to them.
func memSuffix(t gvir.Type, store bool) string {
	bits := intBits(t)
	if bits == 0 {
		bits = floatBits(t)
	}
	switch {
	case bits == 8 && store:
		return "b8"
	case bits == 8:
		return "u8"
	case bits == 16 && store:
		return "b16"
	case bits == 16:
		return "u16"
	}
	return ""
}

// pick chooses between a GFX9 and a GFX11 mnemonic spelling. AMDTX P4 makes
// the bit-width naming generation-neutral, but the bit-counting opcodes were
// renamed outright rather than re-suffixed, so those pick per family.
func (l *lowerer) pick(gfx9, gfx11 string) string {
	if l.target.Family().GTE(amdtx.GFX11) {
		return gfx11
	}
	return gfx9
}

// maskWidth is the width of a .lanemask under this module's .wave, which is
// what the scalar mask opcodes are suffixed with.
func (l *lowerer) maskSuffix() string {
	if l.wave == amdtx.Wave32 {
		return "b32"
	}
	return "b64"
}

func (l *lowerer) maskCmpSuffix() string {
	if l.wave == amdtx.Wave32 {
		return "u32"
	}
	return "u64"
}