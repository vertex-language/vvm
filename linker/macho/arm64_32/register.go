package arm64_32

import "github.com/vertex-language/vvm/linker/macho"

func init() {
	macho.RegisterPatcher(macho.ArchARM64_32, func(t macho.Target) macho.Patcher {
		return macho.PatchFunc(applyARM64_32)
	})
	macho.RegisterPLTPatcher(macho.ArchARM64_32, func(t macho.Target) macho.PLTPatcher {
		return pltPatcher{}
	})
	macho.RegisterDefaultInterp(macho.ArchARM64_32, func(t macho.Target) string {
		return "/usr/lib/dyld" // watchOS device only; no simulator variant ships arm64_32
	})
}