package arm64ec

import "github.com/vertex-language/vvm/linker/pe"

func init() {
	pe.RegisterPatcher(pe.ArchARM64EC, func(t pe.Target) pe.Patcher {
		return &arm64ecPatcher{}
	})
	pe.RegisterPLTPatcher(pe.ArchARM64EC, func(t pe.Target) pe.PLTPatcher {
		return &arm64ecPLTPatcher{}
	})
	pe.RegisterDefaultEntryPoint(pe.ArchARM64EC, func(t pe.Target) string {
		return "mainCRTStartup"
	})
	pe.RegisterSearchDirs(pe.ABIMSVC, func() []string {
		return []string{`C:\Windows\System32`, `C:\Windows\SysWOW64`, `C:\Windows\System`}
	})
}