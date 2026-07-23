// target.go
package vvm

import (
	"fmt"
	"strings"
)

// Target is vvm's own (arch, os, abi[, tier]) triple — deliberately not
// shared with linker/elf.Target, linker/macho.Target, or linker/pe.Target,
// which each have their own shape for their own format's native naming
// (the repo's "no shared types across format boundaries" principle).
// dispatch.go is the one place that translates a vvm.Target into
// whichever format-specific Target the chosen backend actually wants.
type Target struct {
	Arch string   // canonical spelling, §10.1: x86, x86_64, arm, armeb, aarch64, aarch64_be, ...
	OS   string   // canonical spelling, §10.2: linux, macos, ios, windows, none, ...
	ABI  string   // canonical spelling, §10.3: gnu, musl, msvc, eabi, eabihf, ...
	Tier []string // optional feature tiers, §10.4 (e.g. "avx2")

	// MinOSVersion is required when OS selects the Mach-O family (macos,
	// ios, watchos, tvos, visionos) — Apple's triple grammar has no
	// "unversioned" form. Ignored for every other OS.
	MinOSVersion string

	// Kind selects what BuildModule/BuildModuleGraph actually produce.
	Kind OutputKind

	// Flat selects objectwriter's ToFlat path directly instead of a real
	// linker/<format> backend. Only legal when OS == "none": a flat
	// image has no loader, so it only makes sense paired with the
	// "no os" convention — requesting Flat against, say, os=linux would
	// produce bytes no linux loader can run. objFormat() enforces this.
	//
	// flat.Section has no Relocs field at all (objectfile forbids
	// relocations in flat output by construction) — which is also why
	// BuildModuleGraph refuses Flat outright: a multi-module build's
	// entire reason for existing is cross-object symbol resolution, and
	// flat has nowhere to put the relocations that requires.
	Flat            bool
	FlatBaseAddress uint64
}

// OutputKind is not part of the (arch, os, abi) triple string; it's set
// programmatically, the same way real toolchains take a separate
// "-shared" flag alongside a target triple.
type OutputKind int

const (
	OutputExecutable OutputKind = iota
	OutputSharedLibrary
)

// isHostedProcessOS reports whether t.OS implies a hosted process image
// with a libc-style argc/argv/exit(3) convention worth auto-wiring —
// i.e. everything except os=none (bare-metal/kernel) and os=uefi.
func (t Target) isHostedProcessOS() bool {
	return t.OS != "none" && t.OS != "uefi"
}

// ParseTarget parses vvm's own CLI-friendly triple spelling:
//
//	x86_64-linux-gnu
//	aarch64-macos-none[avx2]   // MinOSVersion still has to be set separately
//	x86_64-windows-msvc
//
// No alias resolution happens here (§10.5 — aliases are a build-system-
// boundary concern); only canonical spellings are accepted.
func ParseTarget(s string) (Target, error) {
	tierStart := strings.IndexByte(s, '[')
	var tiers []string
	if tierStart != -1 {
		if !strings.HasSuffix(s, "]") {
			return Target{}, fmt.Errorf("vvm: malformed tier list in target %q", s)
		}
		tierStr := s[tierStart+1 : len(s)-1]
		s = s[:tierStart]
		for _, tier := range strings.Split(tierStr, ",") {
			tiers = append(tiers, strings.TrimSpace(tier))
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
// Lower, never a separate package.
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

// objFormat is the container-format family, derived from OS (or from
// Flat, which bypasses the OS table entirely).
type objFormat int

const (
	formatELF objFormat = iota
	formatMachO
	formatPE
	formatFlat
)

func (t Target) objFormat() (objFormat, error) {
	if t.Flat {
		if t.OS != "none" {
			return 0, fmt.Errorf("vvm: Target.Flat is only valid for os=\"none\", got %q (§10.2)", t.OS)
		}
		return formatFlat, nil
	}
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