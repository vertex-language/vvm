package x86

import isax86 "github.com/vertex-language/vvm/isa/x86"

type SyscallConvention struct {
	RegisterFor                     func(i int) (r isax86.Reg, ok bool)
	StackArgsPushRetAddrPlaceholder bool
	Trap                            Inst
	Result                          isax86.Reg
}

var linuxSyscallRegs = [...]isax86.Reg{
	isax86.REAX, isax86.REBX, isax86.RECX, isax86.REDX, isax86.RESI, isax86.REDI, isax86.REBP,
}

var syscallConventions = map[string]SyscallConvention{
	"linux": {
		RegisterFor: func(i int) (isax86.Reg, bool) {
			if i < 0 || i >= len(linuxSyscallRegs) {
				return isax86.RNone, false
			}
			return linuxSyscallRegs[i], true
		},
		Trap:   Inst{Op: "int", Imm: 0x80},
		Result: isax86.REAX,
	},
	"freebsd": {
		RegisterFor: func(i int) (isax86.Reg, bool) {
			if i == 0 {
				return isax86.REAX, true
			}
			return isax86.RNone, false
		},
		StackArgsPushRetAddrPlaceholder: true,
		Trap:                            Inst{Op: "int", Imm: 0x80},
		Result:                          isax86.REAX,
	},
}

func syscallConventionFor(os string) (SyscallConvention, bool) {
	c, ok := syscallConventions[os]
	return c, ok
}