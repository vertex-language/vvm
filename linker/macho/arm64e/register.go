package arm64e

import "github.com/vertex-language/vvm/linker/macho"

func init() {
	macho.RegisterPatcher(macho.ArchARM64E, func(t macho.Target) macho.Patcher {
		return macho.PatchFunc(applyARM64E)
	})
	macho.RegisterPLTPatcher(macho.ArchARM64E, func(t macho.Target) macho.PLTPatcher {
		return pltPatcher{}
	})
	macho.RegisterDefaultInterp(macho.ArchARM64E, func(t macho.Target) string {
		return "/usr/lib/dyld" // arm64e has no simulator variant
	})
}