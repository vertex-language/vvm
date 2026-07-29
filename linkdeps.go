// linkdeps.go
package vvm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
	linkelf "github.com/vertex-language/vvm/linker/elf"
	linkmacho "github.com/vertex-language/vvm/linker/macho"
	linkpe "github.com/vertex-language/vvm/linker/pe"
)

// resolveELFLinkDependencies walks every module's own m.Links (§7.4) and
// resolves each into l via its search-path-aware Add* methods. seenFile
// dedupes across modules: two modules linking the same system library
// (most commonly "c") must only resolve it once.
func resolveELFLinkDependencies(l *linkelf.Linker, modules []*vir.Module, t Target) error {
	format := vir.FormatOf(t.OS)
	seenNamespace := false
	seenFile := map[string]bool{}

	for _, m := range modules {
		for _, link := range m.Links {
			switch link.Kind {
			case vir.LinkShared:
				// "c" is libc's own conventional short name (§7.4's worked
				// example). Route it through the registered default
				// namespace, which resolves the real per-(arch,os,abi)
				// runtime soname.
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
				// Fail loudly anyway, in case a caller ever hands
				// BuildModule/BuildModuleGraph an unverified *vir.Module.
				return fmt.Errorf("vvm: link framework %q: framework dependencies are not valid for an ELF target (§7.4)", link.Name)
			}
		}
	}
	return nil
}

// resolveMachOLinkDependencies is resolveELFLinkDependencies' Mach-O
// counterpart. linker/macho already has real dependency resolution
// (AddDynamicLibrary/AddCachedDylib, per its own README) — this is just
// vvm's own §7.4-declaration-to-those-calls wiring, the piece that was
// previously missing (every Mach-O link fell through to
// rejectUnresolvableLinkDependencies regardless of what it declared).
func resolveMachOLinkDependencies(l *linkmacho.Linker, modules []*vir.Module, t Target) error {
	format := vir.FormatOf(t.OS)
	seenSoname := map[string]bool{}

	for _, m := range modules {
		for _, link := range m.Links {
			switch link.Kind {
			case vir.LinkShared:
				// "System" is the conventional short name for Apple's
				// combined libc/runtime dylib — macOS ships no standalone
				// libc.dylib at all (§7.4's `link shared "c"` worked
				// example has no macOS equivalent; every libc symbol,
				// including exit, lives in libSystem instead). Route it
				// straight to the real dyld-shared-cache soname, matching
				// linker/macho's own quickstart (`AddDynamicLibrary(
				// "libSystem.B.dylib", nil)`), rather than the generic
				// short-name derivation below, which would only reach the
				// "libSystem.dylib" symlink name — a real path too, but
				// not the one findInstallPath's known-path table
				// special-cases, and not what real Apple toolchains emit
				// into LC_LOAD_DYLIB.
				if link.Name == "System" {
					const soname = "libSystem.B.dylib"
					if seenSoname[soname] {
						continue
					}
					if err := l.AddDynamicLibrary(soname, nil); err != nil {
						return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
					}
					seenSoname[soname] = true
					continue
				}
				file, err := vir.DeriveLinkFile(link, format)
				if err != nil {
					return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
				}
				if seenSoname[file] {
					continue
				}
				// nil data: a dyld-shared-cache-only stub, same tradeoff
				// the package's own README leads with. A caller wanting
				// real export-list validation against a parsed dylib
				// would need AddDynamicLibrary(name, realBytes) instead —
				// not available here, since vvm has no on-disk dylib to
				// read from at this point, only a §7.4 name.
				if err := l.AddDynamicLibrary(file, nil); err != nil {
					return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
				}
				seenSoname[file] = true

			case vir.LinkStatic:
				// linker/macho.Linker.AddArchive exists, but vvm has
				// nowhere to source static-archive *bytes* from a bare
				// §7.4 `link static "foo"` name (no search-path-aware
				// resolver wired up here, unlike resolveELFLinkDependencies's
				// AddSystemArchive) — fail loudly rather than silently
				// dropping the dependency, same stance
				// rejectUnresolvableLinkDependencies already takes.
				return fmt.Errorf(
					"vvm: link static %q: this package has no static-archive "+
						"search-path resolution for Mach-O yet — link it "+
						"manually via linker/macho directly, or remove the "+
						"`link` declaration", link.Name)

			case vir.LinkFramework:
				// linker/macho has no AddFramework convenience wrapper
				// either (its own README says so) — AddCachedDylib with
				// the <Name>.framework/<Name> install-name convention is
				// the documented workaround. Without a declared symbol
				// list to pass, this registers the dependency with no
				// pre-registered exports, the same stub-vs-real tradeoff
				// AddDynamicLibrary(name, nil) makes for an ordinary dylib.
				name := link.Name + ".framework/" + link.Name
				if seenSoname[name] {
					continue
				}
				l.AddCachedDylib(name, nil)
				seenSoname[name] = true
			}
		}
	}
	return nil
}

// resolvePELinkDependencies is linkdeps.go's third resolver, alongside
// resolveELFLinkDependencies and resolveMachOLinkDependencies.
//
// Which of the two PE-native dependency kinds ("real DLL" vs. "import
// library") a `link shared` line resolves as is decided by the *name
// written in source*, never by probing the filesystem to see what
// happens to exist. §7.4/README: a short name (no "." or path
// separator) is derived into the OS-conventional form — for PE that's
// always a ".dll" — so any short name is, by construction, a DLL
// dependency, resolved against the OS's own system directories
// (linkpe.SearchDirs), the same set every OS-shipped library
// (kernel32, user32, the NVIDIA driver's nvcuda.dll, ...) is guaranteed
// to be found in on any machine with that component installed.
//
// An *exact* name (vir.DeriveLinkFile emits it verbatim because it
// contains a "." or path separator — §7.4) is never re-derived, so
// writing `link shared "nvcuda.lib"` in the .vir source is what commits
// this dependency to the import-library path, resolved against
// importLibSearchDirs — an entirely different directory set (SDK/
// toolkit installs), because that's where an import library actually
// lives; the real nvcuda.dll on System32 is never even consulted for a
// name ending in ".lib".
//
// There is no fallback between the two paths in either direction. That
// is deliberate: which mechanism resolves a given `link shared` must be
// readable straight off the source line, not dependent on what a given
// build machine happens to have installed that day.
func resolvePELinkDependencies(l *linkpe.Linker, modules []*vir.Module, t Target) error {
	format := vir.FormatOf(t.OS)
	seenLib := map[string]bool{}
	dllDirs := linkpe.SearchDirs(peABI(t))
	libDirs := importLibSearchDirs(t)

	for _, m := range modules {
		for _, link := range m.Links {
			switch link.Kind {
			case vir.LinkShared:
				file, err := vir.DeriveLinkFile(link, format)
				if err != nil {
					return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
				}
				if seenLib[file] {
					continue
				}

				switch strings.ToLower(filepath.Ext(file)) {
				case ".lib":
					// Exact name explicitly ending in .lib, e.g.
					// `link shared "nvcuda.lib"` — always the
					// import-library path, always searched against SDK/
					// toolkit dirs, never against System32/SysWOW64/System.
					data, path, err := findAndReadFile(file, libDirs)
					if err != nil {
						return fmt.Errorf(
							"vvm: link shared %q: %w (searched: %v — set CUDA_PATH, "+
								"run from a Developer Command Prompt so LIB is set, "+
								"or pass --lib-path)", link.Name, err, libDirs)
					}
					if err := l.AddImportLibrary(file, data); err != nil {
						return fmt.Errorf("vvm: link shared %q (%s): %w", link.Name, path, err)
					}

				default:
					// Short name (derives to .dll) or an exact name given
					// as .dll — always the real-DLL path, always searched
					// against the OS's own system directories.
					data, path, err := findAndReadFile(file, dllDirs)
					if err != nil {
						return fmt.Errorf(
							"vvm: link shared %q: %w (searched: %v)", link.Name, err, dllDirs)
					}
					if err := l.AddDynamicLibrary(file, data); err != nil {
						return fmt.Errorf("vvm: link shared %q (%s): %w", link.Name, path, err)
					}
				}
				seenLib[file] = true

			case vir.LinkStatic:
				// linker/pe.ParseArchive reads plain GNU/SysV ar containers
				// (archive.go) — a real MSVC import library (.lib) adds
				// short-format import-descriptor members on top of that,
				// which this package's archive parser doesn't special-case.
				// Fail loudly rather than silently mis-link, same stance
				// resolveMachOLinkDependencies takes for its own
				// unresolvable case.
				return fmt.Errorf(
					"vvm: link static %q: PE static-archive resolution isn't "+
						"verified against real import-library (.lib) internals yet — "+
						"link it manually via linker/pe directly, or remove the "+
						"`link` declaration", link.Name)

			case vir.LinkFramework:
				return fmt.Errorf("vvm: link framework %q: framework dependencies are not valid for a PE target", link.Name)
			}
		}
	}
	return nil
}

// importLibSearchDirs is the .lib-resolution counterpart to
// linkpe.SearchDirs's .dll-resolution dirs — a disjoint directory set,
// since import libraries never live in System32/SysWOW64/System.
//
// Sources, checked in order (all are appended; findAndReadFile scans
// them in this order too, so an explicit --lib-path wins over $LIB,
// which wins over an auto-detected CUDA_PATH):
//
//  1. --lib-path CLI flag value(s), if the caller threaded any in via
//     the (not-yet-added) Target field / build option — left as a TODO
//     hook below.
//  2. The LIB environment variable, semicolon-separated, exactly as
//     vcvarsall.bat / a Developer Command Prompt populates it with the
//     Windows SDK's and MSVC's lib directories.
//  3. CUDA_PATH, if set (the NVIDIA CUDA installer sets this
//     unconditionally) — probes its conventional lib\x64 subdirectory.
//
// Deliberately does *not* fall back to scanning Program Files: an
// unbounded recursive search is slow and can silently pick up the wrong
// architecture's .lib. Every source here is either explicit or an
// environment variable a real toolchain already relies on.
func importLibSearchDirs(t Target) []string {
	var dirs []string

	if lib := os.Getenv("LIB"); lib != "" {
		for _, d := range strings.Split(lib, ";") {
			if d = strings.TrimSpace(d); d != "" {
				dirs = append(dirs, d)
			}
		}
	}

	if cuda := os.Getenv("CUDA_PATH"); cuda != "" {
		sub := "lib\\x64"
		if t.baseArch() != "x86_64" {
			sub = "lib\\Win32"
		}
		dirs = append(dirs, filepath.Join(cuda, sub))
	}

	return dirs
}

// peABI converts vvm.Target's own string ABI ("msvc", "gnu", or "") into
// linker/pe's ABI enum. vvm.Target and linker/pe.Target deliberately don't
// share a type here (§10.3/"no shared types across format boundaries" —
// see target.go and linker/pe/README.md), so every call site that crosses
// that boundary does its own small conversion; dispatch.go's newLinker
// does the equivalent triple-string round-trip for the rest of pe.Target.
// An empty ABI defaults to msvc, matching dispatch.go's own default.
func peABI(t Target) linkpe.ABI {
	switch t.ABI {
	case "gnu":
		return linkpe.ABIGNU
	default:
		return linkpe.ABIMSVC
	}
}

// findAndReadFile is the PE resolver's search-path scan, shared by both
// the DLL and import-library lookups above — same linear "first dir
// that has it wins" semantics either way, just against different dir
// sets and different filenames.
func findAndReadFile(name string, dirs []string) (data []byte, path string, err error) {
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		if b, err := os.ReadFile(p); err == nil {
			return b, p, nil
		}
	}
	return nil, "", fmt.Errorf("%q not found in any registered search directory", name)
}

// hasAnyLinks reports whether any module in the set declares a §7.4 link
// dependency at all, and names the first one found for the error message.
func hasAnyLinks(modules []*vir.Module) (name string, found bool) {
	for _, m := range modules {
		if len(m.Links) > 0 {
			return m.Links[0].Name, true
		}
	}
	return "", false
}

// rejectUnresolvableLinkDependencies is kept for any other format that may
// later need the same "fail loudly, no resolver yet" stance PE used to
// take before resolvePELinkDependencies was added above.
func rejectUnresolvableLinkDependencies(format string, modules []*vir.Module) error {
	if name, found := hasAnyLinks(modules); found {
		return fmt.Errorf(
			"vvm: %s: this module declares `link %q`, but this package's %s "+
				"dependency resolution isn't implemented yet — link the dependency "+
				"manually via linker/%s directly, or remove the `link` declaration",
			format, name, format, format)
	}
	return nil
}