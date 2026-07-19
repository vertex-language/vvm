package syscallabi

import "github.com/vertex-language/vvm/lower/x86/mcode"

// FreeBSD i386 syscalls also trap via `int 0x80`, but pass every argument
// on the stack, cdecl-style, with only sysno in a register (eax). The
// int 0x80 entry point reads arguments as though it were a `call`, so the
// caller must push a dummy return-address slot beneath the arguments.
func init() {
	register("freebsd", Convention{
		RegisterFor: func(i int) (mcode.Reg, bool) {
			if i == 0 {
				return mcode.REAX, true // sysno only; args 1..6 go on the stack
			}
			return mcode.RNone, false
		},
		StackArgsPushRetAddrPlaceholder: true,
		Trap:                            mcode.Inst{Op: "int", Imm: 0x80},
		Result:                          mcode.REAX,
	})
}