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
// Unlike a direct DLL parse, an import library (`.lib`) carries
// author-chosen DLL names per symbol independent of whether the real DLL
// is ever read at all (see linker/pe's README, "Linking against DLLs:
// three sources, one priority order" — an explicit import library always
// wins over a same-named direct DLL parse, since SymbolTable.Ingest only
// falls through to a later source if a symbol is still undefined). This
// resolver now goes straight to that preferred source: it locates and
// reads the *.lib* off disk and hands it to l.AddImportLibrary, rather
// than parsing the real `.dll`'s export directory via AddDynamicLibrary.
//
// This only resolves on a machine that actually has the target import
// libraries available (i.e. building for windows on windows with an SDK/
// mingw-w64 lib directory populated, or with a manually populated search
// dir) — cross-compiling x86_64-windows-msvc from a host with no such
// directory will fail here with a clear "not found" error rather than
// silently produce a binary with a broken import table.
func resolvePELinkDependencies(l *linkpe.Linker, modules []*vir.Module, t Target) error {
	format := vir.FormatOf(t.OS)
	seenLib := map[string]bool{}
	dirs := linkpe.SearchDirs(peABI(t))

	for _, m := range modules {
		for _, link := range m.Links {
			switch link.Kind {
			case vir.LinkShared:
				file, err := vir.DeriveLinkFile(link, format)
				if err != nil {
					return fmt.Errorf("vvm: link shared %q: %w", link.Name, err)
				}
				// vir.DeriveLinkFile derives the *.dll* spelling (e.g.
				// "kernel32.dll") the same way it does for ELF/Mach-O —
				// swap that for the matching short-format import-library
				// name ("kernel32.lib") rather than reading the DLL
				// itself.
				libFile := strings.TrimSuffix(file, filepath.Ext(file)) + ".lib"
				if seenLib[libFile] {
					continue
				}
				data, path, err := findAndReadFile(libFile, dirs)
				if err != nil {
					return fmt.Errorf(
						"vvm: link shared %q: %w (searched: %v)", link.Name, err, dirs)
				}
				if err := l.AddImportLibrary(libFile, data); err != nil {
					return fmt.Errorf("vvm: link shared %q (%s): %w", link.Name, path, err)
				}
				seenLib[libFile] = true

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

// findAndReadFile is the PE resolver's search-path scan — shared by the
// import-library lookup above. Named generically (not findAndReadDLL)
// since it now serves .lib lookups rather than reading a real .dll.
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