package pe

import "sync"

type PatcherFactory func(t Target) Patcher
type ImportPatcherFactory func(t Target) ImportPatcher

var (
	regMu             sync.RWMutex
	patcherReg        = map[Arch]PatcherFactory{}
	importPatcherReg  = map[Arch]ImportPatcherFactory{}
	entryPointReg     = map[Arch]func(t Target) string{}
	searchDirsReg     = map[ABI]func() []string{}
)

func RegisterPatcher(a Arch, f PatcherFactory) { regMu.Lock(); patcherReg[a] = f; regMu.Unlock() }
func RegisterImportPatcher(a Arch, f ImportPatcherFactory) {
	regMu.Lock()
	importPatcherReg[a] = f
	regMu.Unlock()
}
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

func LookupImportPatcher(t Target) (ImportPatcher, bool) {
	regMu.RLock()
	f, ok := importPatcherReg[t.Arch]
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

// SearchDirs returns the registered DLL search directories for abi, or nil
// if none are registered. Exported so vvm's own link-dependency resolver
// can locate real system DLLs on disk for AddDynamicLibrary.
func SearchDirs(abi ABI) []string {
	return lookupSearchDirs(abi)
}