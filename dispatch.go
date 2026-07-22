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

	ofcoff "github.com/vertex-language/vvm/objectfile/coff"
	ofelf "github.com/vertex-language/vvm/objectfile/elf"
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
		_ = secs

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

// newLinker now takes every module participating in the build (not just
// one) — a multi-module graph build (buildgraph.go) needs every module's
// own §7.4 link section resolved, not only the root's. The single-module
// path (build.go) simply passes a one-element slice.
func newLinker(modules []*vir.Module, t Target, entryPoint string) (linker interface {
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

		// Resolve every module's own §7.4 link section into real bytes.
		// vir.Verify already confirmed each dependency is well-formed
		// (kind/extension agreement, no post-derivation duplicates) and
		// that an ELF target never carries a `framework` dependency —
		// this only has to load what Verify already approved, not
		// re-validate it.
		if err := addELFLinkDependencies(l, modules, t); err != nil {
			return nil, err
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
			archMacho = "arm64"
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
		// TODO: same as before — entrythunk.go has no registered Mach-O
		// thunk, and this package's Mach-O linker dependency resolution
		// (frameworks / shared libs from m.Links) isn't wired yet either;
		// only the ELF path resolves §7.4 dependencies so far.
		l.SetEntryPoint("_main")

		return l, nil

	case formatPE:
		archPE := t.Arch
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
		// TODO: same as Mach-O — no entry thunk, and PE dependency
		// resolution from m.Links isn't wired yet either.

		return l, nil
	}

	return nil, fmt.Errorf("vvm: unreachable format for %s", t)
}

// addELFLinkDependencies walks every module's m.Links — the self-contained
// dependency list (ir.md §7.4) — and resolves each into l via its
// search-path-aware Add* methods. seen dedupes across modules: two
// modules linking the same system library (most commonly "c") must only
// resolve it once.
//
// Links carrying crossModuleLinkPrefix (importrewrite.go) are skipped
// outright — they're bookkeeping synthesized by Stage B to satisfy
// vir.Verify's link/extern-group pairing rule, not real system
// dependencies; the actual symbol lives in another module's own object,
// already attached via a sibling AddObject call (buildgraph.go).
func addELFLinkDependencies(l *linkelf.Linker, modules []*vir.Module, t Target) error {
	format := vir.FormatOf(t.OS)
	seenNamespace := false
	seenFile := map[string]bool{}

	for _, m := range modules {
		for _, link := range m.Links {
			if len(link.Name) >= len(crossModuleLinkPrefix) && link.Name[:len(crossModuleLinkPrefix)] == crossModuleLinkPrefix {
				continue
			}

			switch link.Kind {
			case vir.LinkShared:
				// "c" is libc's own conventional short name (ir.md §7.4's
				// worked example). Route it through the registered default
				// namespace, which resolves the real per-(arch,os,abi)
				// runtime soname — see linker/elf/x86_64/register.go.
				if link.Name == "c" {
					if seenNamespace {
						continue
					}
					if err := l.AddDefaultNamespace(); err != nil {
						return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
					}
					seenNamespace = true
					continue
				}
				file, err := vir.DeriveLinkFile(link, format)
				if err != nil {
					return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
				}
				if seenFile[file] {
					continue
				}
				if err := l.AddSystemLibrary(file); err != nil {
					return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
				}
				seenFile[file] = true

			case vir.LinkStatic:
				file, err := vir.DeriveLinkFile(link, format)
				if err != nil {
					return fmt.Errorf("vvm: link static %q: %w", link.Name, err)
				}
				if seenFile[file] {
					continue
				}
				if err := l.AddSystemArchive(file); err != nil {
					return fmt.Errorf("vvm: link static %q: %w", link.Name, err)
				}
				seenFile[file] = true

			case vir.LinkFramework:
				// Unreachable in practice: vir.Verify rejects `framework` on
				// any non-Mach-O target (§7.4/§9.8) before this ever runs.
				// Fail loudly rather than silently ignoring it, in case a
				// caller ever hands BuildModule/BuildModuleGraph an
				// unverified *vir.Module directly.
				return fmt.Errorf("vvm: link framework %q: framework dependencies are not valid for an ELF target (§7.4)", link.Name)
			}
		}
	}
	return nil
}