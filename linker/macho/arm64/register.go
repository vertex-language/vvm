package arm64

import "github.com/vertex-language/vvm/linker/macho"

func init() {
	macho.RegisterPatcher(macho.ArchARM64, func(t macho.Target) macho.Patcher {
		return macho.PatchFunc(applyARM64)
	})
	macho.RegisterPLTPatcher(macho.ArchARM64, func(t macho.Target) macho.PLTPatcher {
		return pltPatcher{}
	})
	macho.RegisterDefaultInterp(macho.ArchARM64, func(t macho.Target) string {
		if t.Environment == macho.EnvSimulator {
			return "/usr/lib/dyld_sim"
		}
		return "/usr/lib/dyld"
	})
}