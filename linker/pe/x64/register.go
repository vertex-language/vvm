package x64

import "github.com/vertex-language/vvm/linker/pe"

func init() {
	pe.RegisterPatcher(pe.ArchX86_64, func(t pe.Target) pe.Patcher {
		return &amd64Patcher{}
	})
	pe.RegisterPLTPatcher(pe.ArchX86_64, func(t pe.Target) pe.PLTPatcher {
		return &amd64PLTPatcher{}
	})
	pe.RegisterDefaultEntryPoint(pe.ArchX86_64, func(t pe.Target) string {
		return "mainCRTStartup"
	})
	// Shared across ABIs for now — mingw-w64's actual sysroot convention
	// differs and should replace the ABIGNU entry once that's wired up.
	pe.RegisterSearchDirs(pe.ABIMSVC, func() []string {
		return []string{`C:\Windows\System32`, `C:\Windows\SysWOW64`, `C:\Windows\System`}
	})
	pe.RegisterSearchDirs(pe.ABIGNu(), func() []string {
		return []string{`C:\Windows\System32`, `C:\Windows\SysWOW64`, `C:\Windows\System`}
	})
}