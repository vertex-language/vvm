package x64

import "github.com/vertex-language/vvm/linker/pe"

func init() {
	pe.RegisterPatcher(pe.ArchX86_64, func(t pe.Target) pe.Patcher {
		return &amd64Patcher{}
	})
	pe.RegisterImportPatcher(pe.ArchX86_64, func(t pe.Target) pe.ImportPatcher {
		return &amd64ImportPatcher{}
	})
	pe.RegisterDefaultEntryPoint(pe.ArchX86_64, func(t pe.Target) string {
		return "mainCRTStartup"
	})
	pe.RegisterSearchDirs(pe.ABIMSVC, func() []string {
		return []string{`C:\Windows\System32`, `C:\Windows\SysWOW64`, `C:\Windows\System`}
	})
	pe.RegisterSearchDirs(pe.ABIGNU, func() []string {
		return []string{`C:\Windows\System32`, `C:\Windows\SysWOW64`, `C:\Windows\System`}
	})
}