// linkdeps.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	linkelf "github.com/vertex-language/vvm/linker/elf"
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

// rejectUnresolvableLinkDependencies is shared by the Mach-O and PE
// paths, neither of which has a real §7.4 dependency resolver wired up
// yet in this package (nothing here calls linker/macho's
// AddCachedDylib/AddDynamicLibrary or linker/pe's AddDynamicLibrary from
// m.Links today). Failing loudly here means a `link` declaration can
// never silently vanish from the output — the previous behavior was to
// say nothing and produce a binary quietly missing the dependency.
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