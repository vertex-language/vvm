package syscallabi

import "github.com/vertex-language/vvm/lower/x86_64/mcode"

// FreeBSD x86_64 shares Linux's `syscall`-instruction register assignment.
func init() {
	register("freebsd", Convention{
		NR:     mcode.RAX,
		Args:   []mcode.Reg{mcode.RDI, mcode.RSI, mcode.RDX, mcode.R10, mcode.R8, mcode.R9},
		Result: mcode.RAX,
		Trap:   "syscall",
	})
}