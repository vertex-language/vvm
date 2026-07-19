package syscallabi

import "github.com/vertex-language/vvm/lower/x86/mcode"

// Linux i386 syscalls trap via `int 0x80`. The seven slots (sysno plus up
// to six arguments) are all register-passed: eax, ebx, ecx, edx, esi, edi,
// ebp — the classic i386 Linux kernel entry convention (arch/x86/entry).
func init() {
	regs := [...]mcode.Reg{mcode.REAX, mcode.REBX, mcode.RECX, mcode.REDX, mcode.RESI, mcode.REDI, mcode.REBP}
	register("linux", Convention{
		RegisterFor: func(i int) (mcode.Reg, bool) {
			if i < 0 || i >= len(regs) {
				return mcode.RNone, false
			}
			return regs[i], true
		},
		Trap:   mcode.Inst{Op: "int", Imm: 0x80},
		Result: mcode.REAX,
	})
}