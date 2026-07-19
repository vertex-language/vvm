package abi

import (
	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/lower/x86_64/mcode"
)

// Frame layout (grows down):
//
//	[rbp+16+8k] incoming stack arguments (args 7+; 8-byte slots,
//	            callee may write them — SysV permits it)
//	[rbp+8]     return address
//	[rbp]       saved RBP
//	[rbp-8-8k]  one 8-byte home slot per vir value (register-passed
//	            parameters are spilled here by isel's prologue movs)
//
// The spill-everything baseline uses only caller-saved scratch registers,
// so no callee-saved register area exists yet; a real allocator adds one
// when RBX/R12–R15 join the allocatable set.
//
// RSP stays 16-byte aligned: entry has (rsp+8) ≡ 0 mod 16, `push rbp` makes
// rsp ≡ 0 mod 16, and local sizes are rounded to 16. Everything is
// RBP-relative and the epilogue restores RSP from RBP, which is what makes
// per-iteration alloca (§4) safe.
type Frame struct {
	Off   map[string]int32 // value name -> RBP-relative offset
	Local int32            // bytes to subtract in the prologue (16-aligned)
}

func BuildFrame(f *vir.Function, insts []mcode.Inst) *Frame {
	fr := &Frame{Off: map[string]int32{}}
	// Stack-passed parameters (7th onward) live where the caller put them.
	for i, p := range f.Params {
		if i >= len(ArgRegs) {
			fr.Off[p.Name] = int32(16 + 8*(i-len(ArgRegs)))
		}
	}
	// Everything else — including register-passed parameters, which isel
	// spills at function entry — gets a fresh 8-byte slot below RBP.
	n := int32(0)
	for _, in := range insts {
		for _, o := range []mcode.Opr{in.D, in.S} {
			if o.K == mcode.KSlot {
				if _, ok := fr.Off[o.Slot]; !ok {
					fr.Off[o.Slot] = -(8 + 8*n)
					n++
				}
			}
		}
	}
	fr.Local = (8*n + 15) &^ 15 // keep RSP 16-aligned after the prologue
	return fr
}