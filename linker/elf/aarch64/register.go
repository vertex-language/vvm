// register.go — AArch64 codegen registration.
package aarch64

import "github.com/vertex-language/vvm/linker/elf"

func init() {
	elf.RegisterPatcher(elf.ArchARM64, func(t elf.Target) elf.Patcher {
		be := t.BigEndian
		return elf.PatchFunc(func(data []byte, off int, relType uint32, P, S uint64, A int64) error {
			return patchAArch64(data, off, relType, P, S, A, be)
		})
	})
	elf.RegisterPLTPatcher(elf.ArchARM64, func(t elf.Target) elf.PLTPatcher {
		return pltPatcher{bigEndian: t.BigEndian}
	})
	elf.RegisterDefaultInterp(elf.ArchARM64, func(t elf.Target) string {
		if t.ABI == elf.ABIMusl {
			if t.BigEndian {
				return "/lib/ld-musl-aarch64_be.so.1"
			}
			return "/lib/ld-musl-aarch64.so.1"
		}
		if t.BigEndian {
			return "/lib/ld-linux-aarch64_be.so.1"
		}
		return "/lib/ld-linux-aarch64.so.1"
	})
	elf.RegisterSearchDirs(elf.ArchARM64, func(t elf.Target) []string {
		return []string{
			"/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu",
			"/lib64", "/usr/lib64", "/usr/lib", "/lib",
		}
	})
	// Default namespace (§7.4) for anonymous extern groups (`extern :`).
	// Mirrors RegisterDefaultInterp's ABI/endianness branching above:
	// glibc publishes one unversioned soname regardless of endianness
	// ("libc.so.6" — the ELF e_machine/EI_DATA in the .so itself carries
	// the endianness, not the soname string), but musl's soname is
	// arch-qualified and does vary by endianness, same as its ld.so path
	// above. Only linux is covered; other OSes this arch's elfMatrix
	// allows (freebsd, android) return nil until verified rather than
	// guessed.
	elf.RegisterDefaultNamespace(elf.ArchARM64, func(t elf.Target) []string {
		if t.OS != elf.OSLinux {
			return nil
		}
		switch t.ABI {
		case elf.ABIGNU:
			return []string{"libc.so.6"}
		case elf.ABIMusl:
			if t.BigEndian {
				return []string{"libc.musl-aarch64_be.so.1"}
			}
			return []string{"libc.musl-aarch64.so.1"}
		}
		return nil
	})
}