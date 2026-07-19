// Package syscallabi is the per-target-OS syscall trap convention for
// x86_64: which register carries the syscall number, which registers carry
// the (up to six) arguments, which register carries the result, and which
// mcode op is the trap instruction.
package syscallabi

import "github.com/vertex-language/vvm/lower/x86_64/mcode"

type Convention struct {
	NR     mcode.Reg   // register carrying the syscall number
	Args   []mcode.Reg // registers carrying args 1..6, in order
	Result mcode.Reg   // register carrying the return value
	Trap   string      // mcode op name for the trap instruction
}

var registry = map[string]Convention{}

func register(os string, c Convention) { registry[os] = c }

func Lookup(os string) (Convention, bool) {
	c, ok := registry[os]
	return c, ok
}