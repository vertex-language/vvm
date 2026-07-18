package pe

import (
	"fmt"
	"strings"
)

// Arch identifies a target CPU architecture, spelled the way clang-cl
// / rustc spell LLVM triples (aarch64, not arm64).
type Arch uint8

const (
	ArchX86_64 Arch = iota + 1
	ArchI686
	ArchARM
	ArchAArch64
	ArchARM64EC
)

func (a Arch) String() string {
	switch a {
	case ArchX86_64:
		return "x86_64"
	case ArchI686:
		return "i686"
	case ArchARM:
		return "arm"
	case ArchAArch64:
		return "aarch64"
	case ArchARM64EC:
		return "arm64ec"
	default:
		return fmt.Sprintf("Arch(%d)", uint8(a))
	}
}

// machine returns the final-image IMAGE_FILE_MACHINE_* value for a. Note
// arm64ec collapses to AMD64 here — see README "arm64ec is not a distinct
// final-image machine type".
func (a Arch) machine() uint16 {
	switch a {
	case ArchX86_64, ArchARM64EC:
		return imageMachineAMD64
	case ArchI686:
		return imageMachineI386
	case ArchARM:
		return imageMachineARMNT
	case ArchAArch64:
		return imageMachineARM64
	default:
		return 0
	}
}

type OS uint8

const (
	OSWindows OS = iota + 1
	OSUEFI
)

func (o OS) String() string {
	switch o {
	case OSWindows:
		return "windows"
	case OSUEFI:
		return "uefi"
	default:
		return fmt.Sprintf("OS(%d)", uint8(o))
	}
}

// ABI is meaningless (zero value) under OSUEFI.
type ABI uint8

const (
	ABINone ABI = iota
	ABIMSVC
	ABIGNU
)

func (a ABI) String() string {
	switch a {
	case ABIMSVC:
		return "msvc"
	case ABIGNU:
		return "gnu"
	default:
		return ""
	}
}

// Target is the arch-pc-windows-abi / arch-unknown-uefi triple this
// package's Linker builds for.
type Target struct {
	Arch Arch
	OS   OS
	ABI  ABI
}

// ParseTarget parses an LLVM-style triple, e.g. "aarch64-pc-windows-msvc"
// or "x86_64-unknown-uefi".
func ParseTarget(s string) (Target, error) {
	parts := strings.Split(s, "-")
	if len(parts) < 2 {
		return Target{}, fmt.Errorf("pe: invalid target triple %q", s)
	}
	arch, err := parseArch(parts[0])
	if err != nil {
		return Target{}, fmt.Errorf("pe: %q: %w", s, err)
	}
	t := Target{Arch: arch}
	for _, p := range parts[1:] {
		switch p {
		case "windows":
			t.OS = OSWindows
		case "uefi":
			t.OS = OSUEFI
		case "msvc":
			t.ABI = ABIMSVC
		case "gnu":
			t.ABI = ABIGNU
		}
	}
	if t.OS == 0 {
		return Target{}, fmt.Errorf("pe: %q: no recognized OS (windows/uefi)", s)
	}
	if err := t.Valid(); err != nil {
		return Target{}, fmt.Errorf("pe: %q: %w", s, err)
	}
	return t, nil
}

func parseArch(s string) (Arch, error) {
	switch s {
	case "x86_64":
		return ArchX86_64, nil
	case "i686":
		return ArchI686, nil
	case "arm":
		return ArchARM, nil
	case "aarch64":
		return ArchAArch64, nil
	case "arm64ec":
		return ArchARM64EC, nil
	default:
		return 0, fmt.Errorf("unrecognized arch %q", s)
	}
}

// String round-trips ParseTarget.
func (t Target) String() string {
	if t.OS == OSUEFI {
		return fmt.Sprintf("%s-unknown-uefi", t.Arch)
	}
	return fmt.Sprintf("%s-pc-windows-%s", t.Arch, t.ABI)
}

// Valid reports whether t is a real arch×os×abi combination. It does not
// check whether a codegen backend is registered for it — see Linker.Supported.
func (t Target) Valid() error {
	switch t.OS {
	case OSUEFI:
		switch t.Arch {
		case ArchX86_64, ArchI686, ArchAArch64:
			return nil
		default:
			return fmt.Errorf("arch %s has no UEFI convention", t.Arch)
		}
	case OSWindows:
		switch t.Arch {
		case ArchX86_64, ArchI686, ArchAArch64:
			if t.ABI != ABIMSVC && t.ABI != ABIGNU {
				return fmt.Errorf("arch %s requires msvc or gnu abi", t.Arch)
			}
		case ArchARM:
			if t.ABI != ABIMSVC {
				return fmt.Errorf("arm (legacy WoA32) only supports msvc abi")
			}
		case ArchARM64EC:
			if t.ABI != ABIMSVC {
				return fmt.Errorf("arm64ec requires msvc abi — no mingw-w64 arm64ec convention exists")
			}
		default:
			return fmt.Errorf("unrecognized arch")
		}
		return nil
	default:
		return fmt.Errorf("unrecognized OS")
	}
}