// Package syscallabi describes, per target OS, how Vertex IR's
// `syscall.<type> sysno, args...` (§4, §9.33) lowers to a 32-bit x86 trap:
// which physical registers (if any) carry the syscall number and each
// argument, how the trap itself is invoked, and where the kernel leaves
// its result.
package syscallabi

import "github.com/vertex-language/vvm/lower/x86/mcode"

// Convention is one OS's 32-bit syscall trap ABI.
type Convention struct {
	// Name is the OS name this convention was registered under (diagnostics only).
	Name string

	// RegisterFor reports the physical register that should hold operand i
	// (i == 0 is sysno, i == 1..6 are the syscall's arguments), or
	// ok == false if that operand must instead be pushed on the stack.
	RegisterFor func(i int) (r mcode.Reg, ok bool)

	// StackArgsPushRetAddrPlaceholder is true when the kernel's int 0x80
	// entry point expects the same stack shape as a `call` — a bogus
	// return-address slot beneath the stacked arguments — as on FreeBSD.
	StackArgsPushRetAddrPlaceholder bool

	// Trap is the instruction that executes the trap itself.
	Trap mcode.Inst

	// Result is the physical register the trap leaves its return value in.
	Result mcode.Reg
}

var registry = map[string]Convention{}

func register(os string, c Convention) {
	c.Name = os
	registry[os] = c
}

// Lookup returns the syscall convention for a target OS, or ok == false if
// this backend has no wired convention for it (§4: "Unsupported natively on
// os = none/uefi without an explicitly enabled feature-tier flag").
func Lookup(os string) (Convention, bool) {
	c, ok := registry[os]
	return c, ok
}