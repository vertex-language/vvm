// objectfile/macho/target.go
package macho

// Package macho produces Mach-O relocatable object files (MH_OBJECT).
// This package is fully self-contained: it does not import any shared
// "object" package. Every type below — Target, Section, Symbol, Reloc,
// RelocKind, and so on — is macho's own, sized to exactly what Mach-O's
// layout needs. Nothing here is shared with elf, coff, or flat, even where
// the concepts have the same name; that's deliberate, not an oversight.

// Arch identifies the target instruction set. Mach-O (MH_OBJECT) is only
// ever produced for these two 64-bit architectures in this repo.
type Arch int

const (
	ArchAMD64 Arch = iota
	ArchARM64
)

func (a Arch) String() string {
	switch a {
	case ArchAMD64:
		return "amd64"
	case ArchARM64:
		return "arm64"
	}
	return "arch?"
}

// OS identifies the target operating environment. Mach-O (MH_OBJECT) is
// only ever produced for Darwin in this repo; the enum is still named OS
// (rather than a bare constant) so it stays symmetric with elf.OS and
// coff.OS, and so a future addition has somewhere to hang.
type OS int

const (
	OSDarwin OS = iota
)

func (o OS) String() string {
	switch o {
	case OSDarwin:
		return "darwin"
	}
	return "os?"
}

// Target is an (Arch, OS) pair. OS is informational today — cpu_type and
// section layout are driven entirely by Arch. The fine-grained Darwin
// platform (macOS/iOS/tvOS/watchOS/visionOS) is a separate axis handled by
// SetMinOS's Platform argument, not by Target — those two things vary
// independently (an arm64 object can target any of them).
type Target struct {
	Arch Arch
	OS   OS
}

// Predefined targets — names follow GOARCH + GOOS conventions.
var (
	TargetDarwinAMD64 = Target{ArchAMD64, OSDarwin}
	TargetDarwinARM64 = Target{ArchARM64, OSDarwin}
)