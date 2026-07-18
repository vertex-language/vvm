package aarch64

import "github.com/vertex-language/vvm/linker/pe"

func init() {
	pe.RegisterPatcher(pe.ArchAArch64, func(t pe.Target) pe.Patcher {
		return &arm64Patcher{}
	})
	pe.RegisterPLTPatcher(pe.ArchAArch64, func(t pe.Target) pe.PLTPatcher {
		return &arm64PLTPatcher{}
	})
	pe.RegisterDefaultEntryPoint(pe.ArchAArch64, func(t pe.Target) string {
		return "mainCRTStartup"
	})
	// Registered here too (not just in x64) so aarch64 works standalone
	// without blank-importing x64 as well.
	pe.RegisterSearchDirs(pe.ABIMSVC, func() []string {
		return []string{`C:\Windows\System32`, `C:\Windows\SysWOW64`, `C:\Windows\System`}
	})
	pe.RegisterSearchDirs(pe.ABIGNU, func() []string {
		return []string{`C:\Windows\System32`, `C:\Windows\SysWOW64`, `C:\Windows\System`}
	})
}