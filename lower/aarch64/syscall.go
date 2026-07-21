// syscall.go is the per-target-OS user-mode syscall trap convention on
// aarch64: which registers carry the syscall number and arguments, and
// which register receives the result. The trap instruction itself
// (svc #0) is architecturally fixed, so it isn't part of the convention.
package aarch64

import "github.com/vertex-language/vvm/isa/aarch64/encoder"

type syscallConvention struct {
	NumberReg encoder.Reg
	ArgRegs   []encoder.Reg
	ResultReg encoder.Reg
}

// syscallConventions is keyed by target OS. Linux and FreeBSD/aarch64
// share the same x8/x0-x5/x0 svc convention. The deliberate absence of a
// windows/none entry means Lookup reports ok == false there, and the
// caller (isel.go's selSyscall) must surface that as an explicit
// lowering error.
var syscallConventions = map[string]syscallConvention{
	"linux": {
		NumberReg: encoder.X8,
		ArgRegs:   []encoder.Reg{encoder.X0, encoder.X1, encoder.X2, encoder.X3, encoder.X4, encoder.X5},
		ResultReg: encoder.X0,
	},
	"freebsd": {
		NumberReg: encoder.X8,
		ArgRegs:   []encoder.Reg{encoder.X0, encoder.X1, encoder.X2, encoder.X3, encoder.X4, encoder.X5},
		ResultReg: encoder.X0,
	},
}

func lookupSyscall(os string) (syscallConvention, bool) {
	c, ok := syscallConventions[os]
	return c, ok
}