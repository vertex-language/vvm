// target.go
package vvm

import (
	"fmt"
	"strings"
)

// Target is vvm's own (arch, os, abi[, tier]) triple — deliberately not
// shared with linker/elf.Target, linker/macho.Target, or linker/pe.Target,
// which each have their own shape for their own format's native naming
// (per the repo's "no shared types across format boundaries" principle,
// ir.md §10, README "Extended Design Principles"). dispatch.go is the one
// place that translates a vvm.Target into whichever format-specific Target
// the chosen backend actually wants.
type Target struct {
	Arch string   // canonical spelling, ir.md §10.1: x86, x86_64, arm, armeb, aarch64, aarch64_be, ...
	OS   string   // canonical spelling, ir.md §10.2: linux, macos, ios, windows, none, ...
	ABI  string   // canonical spelling, ir.md §10.3: gnu, musl, msvc, eabi, eabihf, ...
	Tier []string // optional feature tiers, ir.md §10.4 (e.g. "avx2")

	// MinOSVersion is required when OS selects the Mach-O family (macos,
	// ios, watchos, tvos, visionos) — Apple's triple grammar has no
	// "unversioned" form (linker/macho's ParseTarget: "<arch>-apple-<sdk><ver>").
	// Ignored for every other OS.
	MinOSVersion string
}

// ParseTarget parses vvm's own CLI-friendly triple spelling, matching the
// dash-joined shape linker/elf already uses for the same purpose:
//
//	x86_64-linux-gnu
//	aarch64-macos-none[avx2]     // MinOSVersion still has to be set separately
//	x86_64-windows-msvc
//
// No alias resolution happens here (ir.md §10.5 — aliases are a
// build-system-boundary concern); only canonical spellings are accepted.
func ParseTarget(s string) (Target, error) {
	tierStart := strings.IndexByte(s, '[')
	tiers := []string(nil)
	if tierStart != -1 {
		if !strings.HasSuffix(s, "]") {
			return Target{}, fmt.Errorf("vvm: malformed tier list in target %q", s)
		}
		tierStr := s[tierStart+1 : len(s)-1]
		s = s[:tierStart]
		for _, t := range strings.Split(tierStr, ",") {
			tiers = append(tiers, strings.TrimSpace(t))
		}
	}

	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return Target{}, fmt.Errorf("vvm: target %q must be arch-os-abi", s)
	}
	return Target{Arch: parts[0], OS: parts[1], ABI: parts[2], Tier: tiers}, nil
}

func (t Target) String() string {
	s := fmt.Sprintf("%s-%s-%s", t.Arch, t.OS, t.ABI)
	if len(t.Tier) > 0 {
		s += "[" + strings.Join(t.Tier, ",") + "]"
	}
	return s
}

// baseArch folds the endianness variants (armeb, aarch64_be) down to the
// arch family that picks the lower/<arch> and object/<arch> packages —
// endianness is threaded through as a bool argument to those packages'
// Lower, never a separate package (per lower/arm's and lower/aarch64's
// own READMEs).
func (t Target) baseArch() string {
	switch t.Arch {
	case "armeb":
		return "arm"
	case "aarch64_be":
		return "aarch64"
	default:
		return t.Arch
	}
}

func (t Target) bigEndian() bool {
	return t.Arch == "armeb" || t.Arch == "aarch64_be"
}

// objFormat is the container format family, derived from OS per the same
// table linker/README.md publishes.
type objFormat int

const (
	formatELF objFormat = iota
	formatMachO
	formatPE
)

func (t Target) objFormat() (objFormat, error) {
	switch t.OS {
	case "linux", "freebsd", "netbsd", "openbsd", "android", "none":
		return formatELF, nil
	case "macos", "ios", "watchos", "tvos", "visionos":
		return formatMachO, nil
	case "windows", "uefi":
		return formatPE, nil
	default:
		return 0, fmt.Errorf("vvm: unrecognized os %q", t.OS)
	}
}