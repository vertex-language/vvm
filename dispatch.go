// dispatch.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"

	objaarch64 "github.com/vertex-language/vvm/object/aarch64"
	objarm "github.com/vertex-language/vvm/object/arm"
	objx86 "github.com/vertex-language/vvm/object/x86"
	objx86_64 "github.com/vertex-language/vvm/object/x86_64"

	loweraarch64 "github.com/vertex-language/vvm/lower/aarch64"
	lowerarm "github.com/vertex-language/vvm/lower/arm"
	lowerx86 "github.com/vertex-language/vvm/lower/x86"
	lowerx86_64 "github.com/vertex-language/vvm/lower/x86_64"

	objwaarch64 "github.com/vertex-language/vvm/objectwriter/aarch64"
	objwx86 "github.com/vertex-language/vvm/objectwriter/x86"
	objwx86_64 "github.com/vertex-language/vvm/objectwriter/x86_64"

	ofelf "github.com/vertex-language/vvm/objectfile/elf"
	ofcoff "github.com/vertex-language/vvm/objectfile/coff"
	ofmacho "github.com/vertex-language/vvm/objectfile/macho"

	linkelf "github.com/vertex-language/vvm/linker/elf"
	linkmacho "github.com/vertex-language/vvm/linker/macho"
	linkpe "github.com/vertex-language/vvm/linker/pe"
)

// toObjectBytes runs stages 3-5 (lower -> object -> objectwriter) for
// whatever (arch, format) cell t selects, and reports a clear error for
// any cell the coverage matrix (objectwriter/README.md) doesn't fill yet,
// rather than approximating.
func toObjectBytes(m *vir.Module, t Target, f objFormat) ([]byte, error) {
	switch t.baseArch() {

	case "x86":
		p, err := lowerx86.Lower(m)
		if err != nil {
			return nil, fmt.Errorf("vvm: lower/x86: %w", err)
		}
		secs := objx86.FromProgram(p)
		switch f {
		case formatELF:
			return objwx86.ToELF(secs, elfObjTarget(t))
		default:
			return nil, fmt.Errorf("vvm: x86 has no objectwriter for this format (coverage: elf, flat only)")
		}

	case "x86_64":
		p, err := lowerx86_64.Lower(m)
		if err != nil {
			return nil, fmt.Errorf("vvm: lower/x86_64: %w", err)
		}
		secs := objx86_64.FromProgram(p)
		switch f {
		case formatELF:
			return objwx86_64.ToELF(secs, elfObjTarget(t))
		case formatMachO:
			return objwx86_64.ToMachO(secs, machoObjTarget(t))
		case formatPE:
			return objwx86_64.ToCOFF(secs, coffObjTarget(t))
		}

	case "arm":
		arch := lowerarm.ArchARM
		if t.bigEndian() {
			arch = lowerarm.ArchARMEB
		}
		p, err := lowerarm.Lower(m, arch)
		if err != nil {
			return nil, fmt.Errorf("vvm: lower/arm: %w", err)
		}
		secs := objarm.FromProgram(p)
		switch f {
		default:
			return nil, fmt.Errorf(
				"vvm: arm has no objectwriter/elf yet (objectfile/elf lacks an ARM " +
					"e_machine entry and BE support) — only flat is reachable today; " +
					"see objectwriter/README.md 'Why arm has no elf.go'")
		}
		_ = secs // reachable once objectwriter/arm grows elf/coff/macho

	case "aarch64":
		arch := loweraarch64.ArchAArch64
		if t.bigEndian() {
			arch = loweraarch64.ArchAArch64BE
		}
		p, err := loweraarch64.Lower(m, arch)
		if err != nil {
			return nil, fmt.Errorf("vvm: lower/aarch64: %w", err)
		}
		secs := objaarch64.FromProgram(p)
		switch f {
		case formatELF:
			return objwaarch64.ToELF(secs, elfObjTarget(t))
		case formatMachO:
			return objwaarch64.ToMachO(secs, machoObjTarget(t))
		case formatPE:
			return objwaarch64.ToCOFF(secs, coffObjTarget(t))
		}
	}

	return nil, fmt.Errorf("vvm: unsupported (arch=%s) for the requested format", t.Arch)
}

// --- objectfile/<format>.Target construction ---------------------------
//
// These are objectfile's own Target types (elf.Target{Arch,OS}, etc.) —
// NOT linker/elf.Target and friends, which is a separate, richer type per
// package by design (README "Design: no shared types across format
// boundaries"). objectwriter's To<Format> calls take these.

func elfObjTarget(t Target) ofelf.Target {
	arch := ofelf.ArchAMD64
	switch t.baseArch() {
	case "x86":
		arch = ofelf.ArchX86
	case "aarch64":
		arch = ofelf.ArchARM64
	}
	os := ofelf.OSLinux
	if t.OS == "none" {
		os = ofelf.OSFreestanding
	}
	// NOTE: objectfile/elf's documented predefined targets only distinguish
	// Linux vs. Freestanding — freebsd/netbsd/openbsd/android currently
	// fold onto the Linux-shaped ELF encoding here, since the docs don't
	// show a distinct OS value for them at this layer (only linker/elf,
	// one stage later, actually varies default-interpreter/search-dirs by
	// the full OS). Flag this if that assumption turns out wrong.
	return ofelf.Target{Arch: arch, OS: os}
}

func coffObjTarget(t Target) ofcoff.Target {
	arch := ofcoff.ArchAMD64
	if t.baseArch() == "aarch64" {
		arch = ofcoff.ArchARM64
	}
	return ofcoff.Target{Arch: arch}
}

func machoObjTarget(t Target) ofmacho.Target {
	arch := ofmacho.ArchAMD64
	if t.baseArch() == "aarch64" {
		arch = ofmacho.ArchARM64
	}
	return ofmacho.Target{Arch: arch}
}

// --- linker/<format> construction ---------------------------------------

// newLinker builds the right linker/<format>.Linker for t, translating
// vvm.Target into that package's own native triple grammar and string
// spelling before calling its ParseTarget — exactly the translation step
// each format's README says is this package's job, not theirs.
//
// entryPoint is what resolveEntryPoint (entrythunk.go) decided the actual
// process entry symbol should be — either "_start" (raw, or the
// newly-synthesized libc-style wrapper) or the `entry`-attributed fn's own
// name (raw wiring on os=none/uefi, unrecognized signatures, or a fn
// literally named "_start" already). For a shared-library build
// (t.Kind == OutputSharedLibrary) no entry point is wired at all — shared
// libraries have no process entry, only init/fini, which this package
// doesn't yet synthesize.
//
// m is no longer consulted for a "default symbol namespace" concept: every
// extern group must declare an explicit, non-empty Dependency matching a
// prior `link` line (§1.2 rule 9, enforced in vir.Verify) — there is no
// anonymous/default-namespace extern group left to detect, on any target,
// so this package has nothing to auto-resolve here.
func newLinker(m *vir.Module, t Target, entryPoint string) (linker interface {
	SetEntryPoint(string)
	AddObject(name string, data []byte) error
	Link() ([]byte, error)
	Supported() bool
}, err error) {
	f, err := t.objFormat()
	if err != nil {
		return nil, err
	}

	switch f {
	case formatELF:
		lt, err := linkelf.ParseTarget(t.String())
		if err != nil {
			return nil, fmt.Errorf("vvm: linker/elf.ParseTarget: %w", err)
		}
		l := linkelf.NewLinker(lt)
		if !l.Supported() {
			return nil, fmt.Errorf("vvm: %s: no elf codegen backend registered", t)
		}

		if t.Kind == OutputSharedLibrary {
			l.SetOutputType(linkelf.OutputShared)
			// No process entry point for a shared library — deliberately
			// skip SetEntryPoint. elf.Linker.Link() only defaults l.entry
			// to "_start" when outputType != OutputShared, so leaving it
			// unset here is exactly "no entry, and none assumed."
		} else {
			l.SetEntryPoint(entryPoint)
		}

		return l, nil

	case formatMachO:
		if t.MinOSVersion == "" {
			return nil, fmt.Errorf(
				"vvm: %s: Target.MinOSVersion is required for Mach-O targets " +
					"(Apple's triple grammar has no unversioned form)", t)
		}
		archMacho := t.baseArch()
		if archMacho == "aarch64" {
			archMacho = "arm64" // linker/macho spells it arm64, not aarch64
		}
		sdk := map[string]string{
			"macos": "macosx", "ios": "iphoneos", "watchos": "watchos",
			"tvos": "appletvos", "visionos": "xros",
		}[t.OS]
		triple := fmt.Sprintf("%s-apple-%s%s", archMacho, sdk, t.MinOSVersion)
		lt, err := linkmacho.ParseTarget(triple)
		if err != nil {
			return nil, fmt.Errorf("vvm: linker/macho.ParseTarget(%q): %w", triple, err)
		}
		l := linkmacho.NewLinker(lt)
		if !l.Supported() {
			return nil, fmt.Errorf("vvm: %s: no macho codegen backend registered", t)
		}
		// TODO: entrythunk.go has no registered thunk for any Mach-O
		// (arch, os) yet, so entryPoint here is always just the raw
		// `entry`-attributed fn's name (or "_start" with no `entry` fn at
		// all) — same as before this change. Kept as the pre-existing
		// hardcoded "_main" for now rather than wiring entryPoint through
		// half-heartedly; revisit once a real macOS CRT thunk lands.
		l.SetEntryPoint("_main")

		return l, nil

	case formatPE:
		archPE := t.Arch // pe spells aarch64 as-is; x86 -> i686
		if archPE == "x86" {
			archPE = "i686"
		}
		abi := t.ABI
		var triple string
		if t.OS == "uefi" {
			triple = fmt.Sprintf("%s-unknown-uefi", archPE)
		} else {
			if abi == "" {
				abi = "msvc"
			}
			triple = fmt.Sprintf("%s-pc-windows-%s", archPE, abi)
		}
		lt, err := linkpe.ParseTarget(triple)
		if err != nil {
			return nil, fmt.Errorf("vvm: linker/pe.ParseTarget(%q): %w", triple, err)
		}
		l := linkpe.NewLinker(lt)
		if !l.Supported() {
			return nil, fmt.Errorf("vvm: %s: no pe codegen backend registered", t)
		}
		// TODO: same as Mach-O above — entrythunk.go has nothing registered
		// for PE yet, so entry point is left at the arch's registered
		// default (mainCRTStartup, etc.), same as before this change.

		return l, nil
	}

	return nil, fmt.Errorf("vvm: unreachable format for %s", t)
}