package x86_64

import "github.com/vertex-language/vvm/linker/macho"

func init() {
	macho.RegisterPatcher(macho.ArchX86_64, func(t macho.Target) macho.Patcher {
		return macho.PatchFunc(applyAMD64)
	})
	macho.RegisterPLTPatcher(macho.ArchX86_64, func(t macho.Target) macho.PLTPatcher {
		return pltPatcher{}
	})
	macho.RegisterDefaultInterp(macho.ArchX86_64, func(t macho.Target) string {
		if t.Environment == macho.EnvSimulator {
			return "/usr/lib/dyld_sim"
		}
		return "/usr/lib/dyld"
	})
}