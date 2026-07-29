package pe

import (
	"encoding/binary"
	"sort"
)

// IATLayout records how imported symbols are grouped into contiguous IAT
// slots inside .iat.
type IATLayout struct {
	DLLOrder []string
	SlotOf   []int
	DLLStart map[string]int
	DLLCount map[string]int
}

// resolvedDLLName returns the import-table string for a resolved symbol,
// per the priority order documented in shared.go/importlib.go: an import
// library's author-supplied DLL name (kindImport) or a direct DLL parse's
// resolved ImportName (kindShared) — never anything read out of a target
// DLL's own internals for any other purpose.
func resolvedDLLName(ts *TableSymbol) string {
	if ts == nil {
		return ""
	}
	switch ts.Kind {
	case kindImport:
		if ts.Import != nil {
			return ts.Import.DLLName
		}
	case kindShared:
		if ts.Lib != nil {
			return ts.Lib.ImportName
		}
	}
	return ""
}

func computeIATLayout(syms []ImportThunkEntry) *IATLayout {
	var dllOrder []string
	dllSeen := make(map[string]bool)
	dllCount := make(map[string]int)

	for _, s := range syms {
		dll := resolvedDLLName(s.Sym)
		if !dllSeen[dll] {
			dllSeen[dll] = true
			dllOrder = append(dllOrder, dll)
		}
		dllCount[dll]++
	}

	dllStart := make(map[string]int)
	slot := 0
	for _, dll := range dllOrder {
		dllStart[dll] = slot
		slot += dllCount[dll] + 1
	}

	slotOf := make([]int, len(syms))
	localIdx := make(map[string]int)
	for i, s := range syms {
		dll := resolvedDLLName(s.Sym)
		slotOf[i] = dllStart[dll] + localIdx[dll]
		localIdx[dll]++
	}

	return &IATLayout{
		DLLOrder: dllOrder,
		SlotOf:   slotOf,
		DLLStart: dllStart,
		DLLCount: dllCount,
	}
}

// totalIATSlots is the full .iat slot count: no reserved header region
// (PE needs none) — just, per DLL, that DLL's imports and its one
// null-terminator slot.
func (l *IATLayout) totalIATSlots() int {
	total := 0
	for _, dll := range l.DLLOrder {
		total += l.DLLCount[dll] + 1
	}
	return total
}

// ── Import table geometry ──────────────────────────────────────────────────

type idataSym struct {
	name  string
	idx   int
	hnOff int
}

type idataGeom struct {
	dllOrder        []string
	dllStart        map[string]int
	importTableSize int
	iltOff          int
	hnOff           int
	dllNOff         int
	total           int
	dllSyms         map[string][]idataSym
	dllNameOff      map[string]int
	dllNameArea     []byte
	nSyms           int
}

func (g idataGeom) size() int { return g.total }

func computeIdataGeom(symtab *SymbolTable, importNames []string, lay *IATLayout) idataGeom {
	g := idataGeom{
		dllOrder:   lay.DLLOrder,
		dllStart:   lay.DLLStart,
		dllSyms:    make(map[string][]idataSym),
		dllNameOff: make(map[string]int),
	}

	for i, sname := range importNames {
		ts := symtab.Lookup(sname)
		if ts == nil || !ts.IsImported() {
			continue
		}
		dll := resolvedDLLName(ts)
		g.dllSyms[dll] = append(g.dllSyms[dll], idataSym{name: sname, idx: i})
		g.nSyms++
	}

	nDLLs := len(g.dllOrder)
	g.importTableSize = (nDLLs + 1) * sizeImportDesc
	g.iltOff = g.importTableSize

	iltSize := 0
	for _, dll := range g.dllOrder {
		iltSize += (lay.DLLCount[dll] + 1) * IATEntrySize
	}
	g.hnOff = g.iltOff + iltSize

	hnTotal := 0
	for _, dll := range g.dllOrder {
		syms := g.dllSyms[dll]
		for j := range syms {
			syms[j].hnOff = hnTotal
			sz := 2 + len(syms[j].name) + 1
			if sz%2 != 0 {
				sz++
			}
			hnTotal += sz
		}
		g.dllSyms[dll] = syms
	}
	g.dllNOff = g.hnOff + hnTotal

	for _, dll := range g.dllOrder {
		g.dllNameOff[dll] = len(g.dllNameArea)
		g.dllNameArea = append(g.dllNameArea, []byte(dll)...)
		g.dllNameArea = append(g.dllNameArea, 0)
		if len(g.dllNameArea)%2 != 0 {
			g.dllNameArea = append(g.dllNameArea, 0)
		}
	}

	g.total = g.dllNOff + len(g.dllNameArea)
	return g
}

// fillImports writes the import directory, ILT, hint/name table, and
// DLL-name area into .idata, and mirrors the name RVAs into .iat.
func fillImports(idata, iat []byte, idataRVA, iatBaseRVA uint32, g idataGeom) (importDirSize, iatSize uint32) {
	descPtr := 0
	iltCursor := g.iltOff
	for _, dll := range g.dllOrder {
		syms := g.dllSyms[dll]

		binary.LittleEndian.PutUint32(idata[descPtr+0:], idataRVA+uint32(iltCursor))
		binary.LittleEndian.PutUint32(idata[descPtr+4:], 0)
		binary.LittleEndian.PutUint32(idata[descPtr+8:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(idata[descPtr+12:], idataRVA+uint32(g.dllNOff+g.dllNameOff[dll]))
		binary.LittleEndian.PutUint32(idata[descPtr+16:], iatBaseRVA+uint32(g.dllStart[dll]*IATEntrySize))
		descPtr += sizeImportDesc

		iatSlot := g.dllStart[dll]
		for j := range syms {
			hnRVA := uint64(idataRVA + uint32(g.hnOff+syms[j].hnOff))
			binary.LittleEndian.PutUint64(idata[iltCursor:], hnRVA)
			if gOff := iatSlot * IATEntrySize; gOff+IATEntrySize <= len(iat) {
				binary.LittleEndian.PutUint64(iat[gOff:], hnRVA)
			}
			iltCursor += IATEntrySize
			iatSlot++
		}
		binary.LittleEndian.PutUint64(idata[iltCursor:], 0)
		iltCursor += IATEntrySize
	}

	for _, dll := range g.dllOrder {
		for _, s := range g.dllSyms[dll] {
			off := g.hnOff + s.hnOff
			binary.LittleEndian.PutUint16(idata[off:], 0)
			copy(idata[off+2:], s.name)
			idata[off+2+len(s.name)] = 0
		}
	}

	copy(idata[g.dllNOff:], g.dllNameArea)

	return uint32(g.importTableSize), uint32((g.nSyms + len(g.dllOrder)) * IATEntrySize)
}

// ── Base relocations ───────────────────────────────────────────────────────

func buildBaseRelocSection(sites []BaseRelocSite, coreBase uint64) []byte {
	if len(sites) == 0 {
		return nil
	}

	sort.Slice(sites, func(i, j int) bool { return sites[i].VA < sites[j].VA })

	var out []byte
	i := 0
	for i < len(sites) {
		pageBase := sites[i].VA &^ 0xFFF

		var entries []uint16
		for i < len(sites) && (sites[i].VA&^0xFFF) == pageBase {
			offset := uint16(sites[i].VA & 0xFFF)
			entries = append(entries, (baseRelocDir64<<12)|offset)
			i++
		}
		if len(entries)%2 != 0 {
			entries = append(entries, 0)
		}
		blockSize := uint32(sizeBaseRelocBlock + len(entries)*2)

		var hdr [8]byte
		binary.LittleEndian.PutUint32(hdr[0:], uint32(pageBase-coreBase))
		binary.LittleEndian.PutUint32(hdr[4:], blockSize)
		out = append(out, hdr[:]...)
		for _, e := range entries {
			var buf [2]byte
			binary.LittleEndian.PutUint16(buf[:], e)
			out = append(out, buf[:]...)
		}
	}
	return out
}