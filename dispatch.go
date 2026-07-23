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
	objwarm "github.com/vertex-language/vvm/objectwriter/arm"
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
// any cell the coverage matrix (objectwriter/README.md) doesn't fill,
// rather than approximating.
//
// arm is the one arch that only reaches a format after checking it can:
// object/arm's own Lower runs *inside* the formatFlat case now, not
// before the format switch — a non-flat arm target fails without ever
// lowering, instead of lowering, building a full Program, and discarding
// it (the old code's dead-work bug).
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
		case formatFlat:
			return objwx86.ToFlat(secs, t.FlatBaseAddress)
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
		case formatFlat:
			return objwx86_64.ToFlat(secs, t.FlatBaseAddress)
		}

	case "arm":
		if f != formatFlat {
			return nil, fmt.Errorf(
				"vvm: arm has no objectwriter/elf yet (objectfile/elf lacks an ARM " +
					"e_machine entry and BE support) — only flat is reachable today; " +
					"see objectwriter/README.md 'Why arm has no elf.go'")
		}
		arch := lowerarm.ArchARM
		if t.bigEndian() {
			arch = lowerarm.ArchARMEB
		}
		p, err := lowerarm.Lower(m, arch)
		if err != nil {
			return nil, fmt.Errorf("vvm: lower/arm: %w", err)
		}
		secs := objarm.FromProgram(p)
		return objwarm.ToFlat(secs, t.FlatBaseAddress)

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
		case formatFlat:
			return objwaarch64.ToFlat(secs, t.FlatBaseAddress)
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

// linkerBackend is the minimal surface vvm needs from whichever
// linker/<format> backend a Target selects — named so build.go/graph.go
// don't repeat an inline method set. All three real backends
// (linker/elf.Linker, linker/macho.Linker, linker/pe.Linker) satisfy it,
// per each package's own quickstart shape.
type linkerBackend interface {
	SetEntryPoint(string)
	AddObject(name string, data []byte) error
	Link() ([]byte, error)
	Supported() bool
}

var machoSDKNames = map[string]string{
	"macos": "macosx", "ios": "iphoneos", "watchos": "watchos",
	"tvos": "appletvos", "visionos": "xros",
}

// newLinker takes every module participating in the build (not just
// one) — a multi-module graph build (graph.go) needs every module's own
// §7.4 link section resolved, not only the root's. The single-module
// path (build.go) simply passes a one-element slice.
func newLinker(modules []*vir.Module, t Target, entryPoint string) (linkerBackend, error) {
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
			// No process entry point for a shared library — elf.Linker.Link()
			// only defaults its entry to "_start" when outputType != OutputShared,
			// so leaving SetEntryPoint uncalled here is exactly "no entry, none assumed."
		} else {
			l.SetEntryPoint(entryPoint)
		}
		if err := resolveELFLinkDependencies(l, modules, t); err != nil {
			return nil, err
		}
		return l, nil

	case formatMachO:
		if t.MinOSVersion == "" {
			return nil, fmt.Errorf(
				"vvm: %s: Target.MinOSVersion is required for Mach-O targets "+
					"(Apple's triple grammar has no unversioned form)", t)
		}
		archMacho := t.baseArch()
		if archMacho == "aarch64" {
			archMacho = "arm64"
		}
		sdk, ok := machoSDKNames[t.OS]
		if !ok {
			return nil, fmt.Errorf("vvm: %s: no Mach-O SDK name known for os %q", t, t.OS)
		}
		triple := fmt.Sprintf("%s-apple-%s%s", archMacho, sdk, t.MinOSVersion)
		lt, err := linkmacho.ParseTarget(triple)
		if err != nil {
			return nil, fmt.Errorf("vvm: linker/macho.ParseTarget(%q): %w", triple, err)
		}
		l := linkmacho.NewLinker(lt)
		if !l.Supported() {
			return nil, fmt.Errorf("vvm: %s: no macho codegen backend registered", t)
		}
		if t.Kind == OutputSharedLibrary {
			l.SetOutputType(linkmacho.OutputShared)
		} else {
			l.SetEntryPoint(entryPoint)
		}
		if err := rejectUnresolvableLinkDependencies("macho", modules); err != nil {
			return nil, err
		}
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
		if t.Kind == OutputSharedLibrary {
			l.SetOutputType(linkpe.OutputShared)
		} else {
			// BUG FIX: the previous version of this file never called
			// SetEntryPoint on the PE path at all. Per linker/pe's own
			// README, emitPE silently sets AddressOfEntryPoint = 0 when no
			// entry symbol resolves — every PE binary built by the old code
			// had a zero entry point.
			l.SetEntryPoint(entryPoint)
		}
		if err := rejectUnresolvableLinkDependencies("pe", modules); err != nil {
			return nil, err
		}
		return l, nil

	case formatFlat:
		return nil, fmt.Errorf("vvm: unreachable: flat targets never call newLinker (build.go/graph.go return toObjectBytes's output directly)")
	}

	return nil, fmt.Errorf("vvm: unreachable format for %s", t)
}