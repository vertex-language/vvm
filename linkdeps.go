// linkdeps.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	linkelf "github.com/vertex-language/vvm/linker/elf"
	linkmacho "github.com/vertex-language/vvm/linker/macho"
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

// rejectUnresolvableLinkDependencies is used by the PE path, which has no
// real §7.4 dependency resolver wired up yet in this package (nothing
// here calls linker/pe's AddDynamicLibrary from m.Links today). Failing
// loudly here means a `link` declaration can never silently vanish from
// the output — the previous behavior was to say nothing and produce a
// binary quietly missing the dependency.
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