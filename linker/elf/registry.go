// registry.go — per-arch codegen registration. Subpackages call these from
// their own init() so adding a new arch never touches this file.
package elf

import "fmt"

type PatcherFactory func(t Target) Patcher
type PLTPatcherFactory func(t Target) PLTPatcher
type DefaultInterpFunc func(t Target) string
type SearchDirsFunc func(t Target) []string

var (
	patcherRegistry    = map[Arch]PatcherFactory{}
	pltPatcherRegistry = map[Arch]PLTPatcherFactory{}
	defaultInterpReg   = map[Arch]DefaultInterpFunc{}
	searchDirsReg      = map[Arch]SearchDirsFunc{}
)

func RegisterPatcher(a Arch, f PatcherFactory)         { patcherRegistry[a] = f }
func RegisterPLTPatcher(a Arch, f PLTPatcherFactory)    { pltPatcherRegistry[a] = f }
func RegisterDefaultInterp(a Arch, f DefaultInterpFunc) { defaultInterpReg[a] = f }
func RegisterSearchDirs(a Arch, f SearchDirsFunc)       { searchDirsReg[a] = f }

func LookupPatcher(t Target) (Patcher, bool) {
	f, ok := patcherRegistry[t.Arch]
	if !ok {
		return nil, false
	}
	return f(t), true
}

func LookupPLTPatcher(t Target) (PLTPatcher, bool) {
	f, ok := pltPatcherRegistry[t.Arch]
	if !ok {
		return nil, false
	}
	return f(t), true
}

func defaultInterp(t Target) string {
	if f, ok := defaultInterpReg[t.Arch]; ok {
		return f(t)
	}
	return ""
}

func defaultSearchDirs(t Target) []string {
	if f, ok := searchDirsReg[t.Arch]; ok {
		return f(t)
	}
	return nil
}

// Supported reports whether a full codegen backend (patcher + PLT patcher)
// is registered for t.Arch. A target can be Valid() and still !Supported().
func (l *Linker) Supported() bool {
	return supported(l.target)
}

func supported(t Target) bool {
	_, hasPatch := patcherRegistry[t.Arch]
	_, hasPLT := pltPatcherRegistry[t.Arch]
	return hasPatch && hasPLT
}

func mustRegistered(t Target) error {
	if _, ok := patcherRegistry[t.Arch]; !ok {
		return fmt.Errorf("elf: no relocation patcher registered for %s (blank-import its subpackage)", t)
	}
	if _, ok := pltPatcherRegistry[t.Arch]; !ok {
		return fmt.Errorf("elf: no PLT patcher registered for %s (blank-import its subpackage)", t)
	}
	return nil
}