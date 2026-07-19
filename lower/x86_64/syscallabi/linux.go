package syscallabi

import "github.com/vertex-language/vvm/lower/x86_64/mcode"

// Linux x86_64 syscall convention: NR in RAX, args in RDI/RSI/RDX/R10/R8/R9
// (R10, not RCX, since the `syscall` instruction itself clobbers RCX/R11),
// result in RAX, trapped via the `syscall` instruction.
func init() {
	register("linux", Convention{
		NR:     mcode.RAX,
		Args:   []mcode.Reg{mcode.RDI, mcode.RSI, mcode.RDX, mcode.R10, mcode.R8, mcode.R9},
		Result: mcode.RAX,
		Trap:   "syscall",
	})
}