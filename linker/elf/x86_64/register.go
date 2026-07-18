// register.go — x86_64 codegen registration.
package x86_64

import "github.com/vertex-language/vvm/linker/elf"

func init() {
	elf.RegisterPatcher(elf.ArchX86_64, func(t elf.Target) elf.Patcher {
		return elf.PatchFunc(patchX86_64)
	})
	elf.RegisterPLTPatcher(elf.ArchX86_64, func(t elf.Target) elf.PLTPatcher {
		return pltPatcher{}
	})
	elf.RegisterDefaultInterp(elf.ArchX86_64, func(t elf.Target) string {
		if t.ABI == elf.ABIMusl {
			return "/lib/ld-musl-x86_64.so.1"
		}
		return "/lib64/ld-linux-x86-64.so.2"
	})
	elf.RegisterSearchDirs(elf.ArchX86_64, func(t elf.Target) []string {
		return []string{
			"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu",
			"/lib64", "/usr/lib64", "/usr/lib", "/lib",
		}
	})
}