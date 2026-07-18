package pe

import "sync"

// PatcherFactory builds the relocation Patcher for a given target.
type PatcherFactory func(t Target) Patcher

// PLTPatcherFactory builds the import-thunk PLTPatcher for a given target.
type PLTPatcherFactory func(t Target) PLTPatcher

var (
	regMu           sync.RWMutex
	patcherReg      = map[Arch]PatcherFactory{}
	pltPatcherReg   = map[Arch]PLTPatcherFactory{}
	entryPointReg   = map[Arch]func(t Target) string{}
	searchDirsReg   = map[ABI]func() []string{}
)

func RegisterPatcher(a Arch, f PatcherFactory)             { regMu.Lock(); patcherReg[a] = f; regMu.Unlock() }
func RegisterPLTPatcher(a Arch, f PLTPatcherFactory)        { regMu.Lock(); pltPatcherReg[a] = f; regMu.Unlock() }
func RegisterDefaultEntryPoint(a Arch, f func(t Target) string) {
	regMu.Lock()
	entryPointReg[a] = f
	regMu.Unlock()
}
func RegisterSearchDirs(a ABI, f func() []string) { regMu.Lock(); searchDirsReg[a] = f; regMu.Unlock() }

func LookupPatcher(t Target) (Patcher, bool) {
	regMu.RLock()
	f, ok := patcherReg[t.Arch]
	regMu.RUnlock()
	if !ok {
		return nil, false
	}
	return f(t), true
}

func LookupPLTPatcher(t Target) (PLTPatcher, bool) {
	regMu.RLock()
	f, ok := pltPatcherReg[t.Arch]
	regMu.RUnlock()
	if !ok {
		return nil, false
	}
	return f(t), true
}

func lookupDefaultEntryPoint(t Target) (string, bool) {
	regMu.RLock()
	f, ok := entryPointReg[t.Arch]
	regMu.RUnlock()
	if !ok {
		return "", false
	}
	return f(t), true
}

func lookupSearchDirs(abi ABI) []string {
	regMu.RLock()
	f, ok := searchDirsReg[abi]
	regMu.RUnlock()
	if !ok {
		return nil
	}
	return f()
}