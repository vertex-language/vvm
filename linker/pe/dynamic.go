package pe

import "encoding/binary"

// PLT/GOT slot geometry. These are exported because the per-arch PLTPatcher
// subpackages need the exact same numbers to compute thunk and IAT addresses;
// a local redeclaration in each subpackage is how the FirstThunk skew got in.
//
// PLTHeaderSize and GOTReserved are both ELF inheritances: PLT[0] is ELF's
// resolver trampoline and the three reserved .got.plt slots are ELF's
// _DYNAMIC / link_map / _dl_runtime_resolve entries. PE has no equivalent of
// either — its IAT is a bare null-terminated thunk array starting exactly at
// FirstThunk. They're kept for now because the layout and patchers are written
// around them; if they're dropped later, these two constants go to 0 and every
// consumer follows automatically.
const (
	PLTHeaderSize = 16
	PLTEntrySize  = 16
	GOTEntrySize  = 8
	GOTReserved   = 3
)

// PLTEntry pairs a shared symbol with its 0-based stub index (PLT0 not counted).
type PLTEntry struct {
	Name string
	Sym  *TableSymbol
	Idx  int
}

// PLTPatcher writes arch-specific PLT thunks. PE imports resolve through the
// IAT in .got.plt and the import directory — there is no ELF-style .rela.plt.
//
// A PLTPatcher may additionally implement IATLayoutSetter (see patch.go) to
// receive the computed slot layout at Link() time.
type PLTPatcher interface {
	PatchPLT(plt, gotPLT []byte, pltBase, gotBase uint64, syms []PLTEntry)
}

// CollectPLTSymbols returns every kindShared symbol actually referenced by at
// least one object relocation, in stable first-seen order.
func CollectPLTSymbols(symtab *SymbolTable, objects []*Object) []PLTEntry {
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

	var out []PLTEntry
	seen := make(map[string]bool)
	for _, obj := range objects {
		for _, raw := range obj.Symbols {
			if raw == nil || raw.Name == "" || seen[raw.Name] || !referenced[raw.Name] {
				continue
			}
			sym := symtab.Lookup(raw.Name)
			if sym == nil || !sym.IsShared() {
				continue
			}
			seen[raw.Name] = true
			out = append(out, PLTEntry{Name: raw.Name, Sym: sym, Idx: len(out)})
		}
	}
	return out
}

// InjectPLTSections appends placeholder .plt and .got.plt sections so they
// receive virtual addresses during AssignLayout.
//
// .got.plt is sized from lay.totalGOTSlots() rather than len(syms): the IAT
// carries one null-terminator slot per DLL in addition to the reserved header
// slots, and computeIATLayout already assigns real slot indices past those
// terminators. Sizing this section any other way makes the IAT data directory
// (which spans every slot including terminators) overrun the section's own
// virtual size.
//
// No .rela.plt is injected. PE resolves imports through the IAT and import
// directory rather than ELF RELA entries; an allocatable section that
// AssignLayout addresses but the emitter never writes would leave an uncovered
// RVA range, which the NT image loader rejects with ERROR_BAD_EXE_FORMAT.
func InjectPLTSections(layout *Layout, syms []PLTEntry, lay *IATLayout) {
	n := len(syms)
	plt := &MergedSection{
		Name:  ".plt",
		Flags: SecAlloc | SecExec,
		Data:  make([]byte, PLTHeaderSize+n*PLTEntrySize),
		Size:  uint64(PLTHeaderSize + n*PLTEntrySize),
		Align: 16,
	}
	gotSlots := lay.totalGOTSlots()
	gotPLT := &MergedSection{
		Name:  ".got.plt",
		Flags: SecAlloc | SecWrite,
		Data:  make([]byte, gotSlots*GOTEntrySize),
		Size:  uint64(gotSlots * GOTEntrySize),
		Align: 8,
	}
	layout.Sections = append(layout.Sections, plt, gotPLT)
	layout.secByName[".plt"] = plt
	layout.secByName[".got.plt"] = gotPLT
}

// PatchPLT fills the PLT thunks and assigns each PLT symbol's VAddr to its stub.
func PatchPLT(pp PLTPatcher, layout *Layout, syms []PLTEntry) error {
	pltSec, ok1 := layout.SectionByName(".plt")
	gotSec, ok2 := layout.SectionByName(".got.plt")
	if !ok1 || !ok2 {
		return nil
	}
	pp.PatchPLT(pltSec.Data, gotSec.Data, pltSec.VAddr, gotSec.VAddr, syms)
	return nil
}

// PutI32LE writes a signed little-endian 32-bit integer into b[0:4].
func PutI32LE(b []byte, v int32) { binary.LittleEndian.PutUint32(b, uint32(v)) }