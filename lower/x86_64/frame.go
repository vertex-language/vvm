package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// Frame layout (grows down):
//
//	[rbp+16+8k] incoming stack arguments (args 7+; 8-byte slots,
//	            callee may write them — SysV permits it)
//	[rbp+8]     return address
//	[rbp]       saved RBP
//	[rbp-8-8k]  one 8-byte home slot per vir value (register-passed
//	            parameters are spilled here by isel's prologue movs)
//
// The spill-everything baseline uses only caller-saved scratch registers
// (RAX/RCX/RDX/RSI/RDI/R8–R11), so no callee-saved register area exists yet;
// a real allocator adds one when RBX/R12–R15 join the allocatable set.
//
// RSP stays 16-byte aligned: entry has (rsp+8) ≡ 0 mod 16, `push rbp` makes
// rsp ≡ 0 mod 16, and local sizes are rounded to 16. Everything is
// RBP-relative and the epilogue restores RSP from RBP, which is what makes
// per-iteration alloca (§4) safe.

type frame struct {
	off   map[string]int32 // value name -> RBP-relative offset
	local int32            // bytes to subtract in the prologue (16-aligned)
}

func buildFrame(f *vir.Func, insts []minst) *frame {
	fr := &frame{off: map[string]int32{}}
	// Stack-passed parameters (7th onward) live where the caller put them.
	for i, p := range f.Params {
		if i >= len(argRegs) {
			fr.off[p.Name] = int32(16 + 8*(i-len(argRegs)))
		}
	}
	// Everything else — including register-passed parameters, which isel
	// spills at function entry — gets a fresh 8-byte slot below RBP.
	n := int32(0)
	for _, in := range insts {
		for _, o := range []opr{in.d, in.s} {
			if o.k == oSlot {
				if _, ok := fr.off[o.slot]; !ok {
					fr.off[o.slot] = -(8 + 8*n)
					n++
				}
			}
		}
	}
	fr.local = (8*n + 15) &^ 15 // keep RSP 16-aligned after the prologue
	return fr
}