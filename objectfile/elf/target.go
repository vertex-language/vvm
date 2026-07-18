// objectfile/elf/target.go
package elf

// Package elf produces ELF64/ELF32 relocatable object files (ET_REL).
// This package is fully self-contained: it does not import any shared
// "object" package. Every type below — Target, Section, Symbol, Reloc,
// RelocKind, and so on — is elf's own, sized to exactly what ELF's layout
// needs. Nothing here is shared with coff, macho, or flat, even where the
// concepts have the same name; that's deliberate, not an oversight.

// Arch identifies the target instruction set.
type Arch int

const (
	ArchAMD64 Arch = iota
	ArchARM64
	ArchRISCV64
	ArchX86 // 32-bit; forces ELFCLASS32 output
)

func (a Arch) String() string {
	switch a {
	case ArchAMD64:
		return "amd64"
	case ArchARM64:
		return "arm64"
	case ArchRISCV64:
		return "riscv64"
	case ArchX86:
		return "x86"
	}
	return "arch?"
}

// OS identifies the target operating environment. ELF is only ever
// produced for Linux and freestanding targets in this repo; the enum is
// still named OS (rather than, say, a bool) so that a future addition
// (e.g. a BSD-specific section quirk) has somewhere to hang without
// reshaping Target.
type OS int

const (
	OSLinux OS = iota
	OSFreestanding
)

func (o OS) String() string {
	switch o {
	case OSLinux:
		return "linux"
	case OSFreestanding:
		return "freestanding"
	}
	return "os?"
}

// Target is an (Arch, OS) pair. OS is informational for ELF output today —
// e_machine and section layout are driven entirely by Arch — but it stays
// on Target so OSABI selection (Linux vs. bare-metal defaults) has a
// natural home if it grows past what SetOSABI covers.
type Target struct {
	Arch Arch
	OS   OS
}

// Predefined targets — names follow GOARCH + GOOS conventions.
var (
	TargetLinuxAMD64          = Target{ArchAMD64, OSLinux}
	TargetLinuxARM64          = Target{ArchARM64, OSLinux}
	TargetLinuxRISCV64        = Target{ArchRISCV64, OSLinux}
	TargetLinuxX86            = Target{ArchX86, OSLinux}
	TargetFreestandingAMD64   = Target{ArchAMD64, OSFreestanding}
	TargetFreestandingARM64   = Target{ArchARM64, OSFreestanding}
	TargetFreestandingRISCV64 = Target{ArchRISCV64, OSFreestanding}
	TargetFreestandingX86     = Target{ArchX86, OSFreestanding}
)