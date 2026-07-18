// objectfile/coff/target.go
package coff

// Package coff produces COFF relocatable object files (raw .obj, no MS-DOS
// stub) for Windows targets. This package is fully self-contained: it does
// not import any shared "object" package. Every type below — Target,
// Section, Symbol, Reloc, RelocKind, and so on — is coff's own, sized to
// exactly what COFF's layout needs. Nothing here is shared with elf, macho,
// or flat, even where the concepts have the same name; that's deliberate,
// not an oversight.

// Arch identifies the target instruction set.
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

// OS identifies the target operating environment. COFF, as produced by this
// package, is only ever emitted for Windows. The enum is still named OS
// (rather than dropped entirely) so Target's shape matches elf.Target,
// coff.Target, and their siblings — that symmetry is what lets link/ reason
// about all four format packages the same way, even though OS carries only
// one value here today.
type OS int

const (
	OSWindows OS = iota
)

func (o OS) String() string {
	switch o {
	case OSWindows:
		return "windows"
	}
	return "os?"
}

// Target is an (Arch, OS) pair. OS is informational for COFF output today —
// machine and section layout are driven entirely by Arch — but it stays on
// Target for the same reason elf.Target keeps OS around: a future format
// quirk that depends on the OS has somewhere to hang.
type Target struct {
	Arch Arch
	OS   OS
}

// Predefined targets — names follow GOARCH + GOOS conventions.
var (
	TargetWindowsAMD64 = Target{ArchAMD64, OSWindows}
	TargetWindowsARM64 = Target{ArchARM64, OSWindows}
)