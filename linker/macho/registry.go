package macho

// PatcherFactory builds a relocation Patcher for a resolved Target.
type PatcherFactory func(Target) Patcher

// PLTPatcherFactory builds a PLT/stub Patcher for a resolved Target.
type PLTPatcherFactory func(Target) PLTPatcher

// InterpFactory returns the default dynamic linker path for a Target.
type InterpFactory func(Target) string

var (
	patcherRegistry    = map[Arch]PatcherFactory{}
	pltPatcherRegistry = map[Arch]PLTPatcherFactory{}
	interpRegistry     = map[Arch]InterpFactory{}
)

// RegisterPatcher registers the relocation backend for an Arch. Called from
// an arch subpackage's init(), after a blank import.
func RegisterPatcher(a Arch, f PatcherFactory) { patcherRegistry[a] = f }

// RegisterPLTPatcher registers the PLT/stub backend for an Arch.
func RegisterPLTPatcher(a Arch, f PLTPatcherFactory) { pltPatcherRegistry[a] = f }

// RegisterDefaultInterp registers the default dyld path resolver for an Arch.
func RegisterDefaultInterp(a Arch, f InterpFactory) { interpRegistry[a] = f }

// Supported reports whether a codegen backend is registered for t.Arch —
// i.e. whether the relevant subpackage has been blank-imported.
func Supported(t Target) bool {
	_, p := patcherRegistry[t.Arch]
	_, pp := pltPatcherRegistry[t.Arch]
	return p && pp
}

func lookupPatcher(t Target) (Patcher, bool) {
	f, ok := patcherRegistry[t.Arch]
	if !ok {
		return nil, false
	}
	return f(t), true
}

func lookupPLTPatcher(t Target) (PLTPatcher, bool) {
	f, ok := pltPatcherRegistry[t.Arch]
	if !ok {
		return nil, false
	}
	return f(t), true
}

func lookupDefaultInterp(t Target) string {
	if f, ok := interpRegistry[t.Arch]; ok {
		return f(t)
	}
	return "/usr/lib/dyld"
}