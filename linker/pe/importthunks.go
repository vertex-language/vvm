package pe

import "encoding/binary"

const (
	ThunkEntrySize = 16 // per-import jump-stub size (arch-specific encoding, fixed slot size)
	IATEntrySize   = 8  // per-import pointer slot size
)

// ImportThunkEntry pairs an imported symbol (from either AddDynamicLibrary
// or AddImportLibrary) with its 0-based thunk index.
type ImportThunkEntry struct {
	Name string
	Sym  *TableSymbol
	Idx  int
}

// ImportPatcher writes arch-specific import thunks into .thunk and fills
// .iat slot addresses. May additionally implement IATLayoutSetter.
type ImportPatcher interface {
	PatchImportThunks(thunk, iat []byte, thunkBase, iatBase uint64, syms []ImportThunkEntry)
}

// CollectImportSymbols returns every imported symbol actually referenced
// by at least one object relocation, in stable first-seen order.
func CollectImportSymbols(symtab *SymbolTable, objects []*Object) []ImportThunkEntry {
	referenced := make(map[string]bool)
	for _, obj := range objects {
		for _, rel := range obj.Relocs {
			if int(rel.SymIdx) < len(obj.Symbols) && obj.Symbols[rel.SymIdx] != nil {
				if name := obj.Symbols[rel.SymIdx].Name; name != "" {
					referenced[name] = true
				}
			}
		}
	}

	var out []ImportThunkEntry
	seen := make(map[string]bool)
	for _, obj := range objects {
		for _, raw := range obj.Symbols {
			if raw == nil || raw.Name == "" || seen[raw.Name] || !referenced[raw.Name] {
				continue
			}
			sym := symtab.Lookup(raw.Name)
			if sym == nil || !sym.IsImported() {
				continue
			}
			seen[raw.Name] = true
			out = append(out, ImportThunkEntry{Name: raw.Name, Sym: sym, Idx: len(out)})
		}
	}
	return out
}

// InjectImportSections appends placeholder .thunk (executable jump stubs)
// and .iat (the pointer array the loader actually binds) sections so they
// receive virtual addresses during AssignLayout.
func InjectImportSections(layout *Layout, syms []ImportThunkEntry, lay *IATLayout) {
	n := len(syms)
	thunk := &MergedSection{
		Name:  ".thunk",
		Flags: SecAlloc | SecExec,
		Data:  make([]byte, n*ThunkEntrySize),
		Size:  uint64(n * ThunkEntrySize),
		Align: 16,
	}
	iatSlots := lay.totalIATSlots()
	iat := &MergedSection{
		Name:  ".iat",
		Flags: SecAlloc | SecWrite,
		Data:  make([]byte, iatSlots*IATEntrySize),
		Size:  uint64(iatSlots * IATEntrySize),
		Align: 8,
	}
	layout.Sections = append(layout.Sections, thunk, iat)
	layout.secByName[".thunk"] = thunk
	layout.secByName[".iat"] = iat
}

// PatchImportThunks fills the thunks and assigns each imported symbol's
// VAddr to its stub.
func PatchImportThunks(pp ImportPatcher, layout *Layout, syms []ImportThunkEntry) error {
	thunkSec, ok1 := layout.SectionByName(".thunk")
	iatSec, ok2 := layout.SectionByName(".iat")
	if !ok1 || !ok2 {
		return nil
	}
	pp.PatchImportThunks(thunkSec.Data, iatSec.Data, thunkSec.VAddr, iatSec.VAddr, syms)
	return nil
}

// PutI32LE writes a signed little-endian 32-bit integer into b[0:4].
func PutI32LE(b []byte, v int32) { binary.LittleEndian.PutUint32(b, uint32(v)) }