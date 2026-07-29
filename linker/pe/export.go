package pe

import (
	"encoding/binary"
	"sort"
)

// ExportSymbol is one function this image exposes via its export directory.
type ExportSymbol struct {
	Name string
	Sym  *TableSymbol // VAddr filled in by ResolveSymbolAddresses before fillExportDirectory runs
}

// ExportCandidates returns, for OutputShared builds, every symbol that
// qualifies as an export-directory root: non-weak, strongly or tentatively
// defined, with COFF external linkage and a real section name. GC's own
// OutputShared reachability roots use this same set (see gc.go) — kept as
// one function so the two can't silently diverge.
func ExportCandidates(symtab *SymbolTable) []*TableSymbol {
	var out []*TableSymbol
	for _, sym := range symtab.All() {
		if sym.IsDefined() && !sym.Weak && sym.RawSym != nil &&
			sym.RawSym.StorageClass.IsExternal() && sym.RawSym.SectionName != "" {
			out = append(out, sym)
		}
	}
	return out
}

// exportGeom is the layout of a computed IMAGE_EXPORT_DIRECTORY plus its
// four satellite arrays, computed once so the pre-layout size estimate and
// the post-layout byte fill can never disagree — same split idataGeom uses.
type exportGeom struct {
	dllNameOff int
	eatOff     int
	namesOff   int
	ordOff     int
	strOff     int
	total      int
	syms       []ExportSymbol // sorted by Name — required so AddressOfNames supports the loader's binary search
}

func computeExportGeom(dllName string, syms []ExportSymbol) exportGeom {
	sorted := make([]ExportSymbol, len(syms))
	copy(sorted, syms)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	g := exportGeom{syms: sorted}
	g.dllNameOff = sizeExportDir
	dllNameSize := len(dllName) + 1

	g.eatOff = g.dllNameOff + dllNameSize
	eatSize := len(sorted) * 4

	g.namesOff = g.eatOff + eatSize
	namesSize := len(sorted) * 4

	g.ordOff = g.namesOff + namesSize
	ordSize := len(sorted) * 2

	g.strOff = g.ordOff + ordSize
	strSize := 0
	for _, s := range sorted {
		strSize += len(s.Name) + 1
	}

	g.total = g.strOff + strSize
	return g
}

// fillExportDirectory writes the export directory, EAT, name pointer
// table, ordinal table, DLL name, and export-name strings into an
// already-placed .edata section.
func fillExportDirectory(edata []byte, edataRVA uint32, coreBase uint64, dllName string, g exportGeom) {
	binary.LittleEndian.PutUint32(edata[0:], 0)                              // Characteristics
	binary.LittleEndian.PutUint32(edata[4:], 0)                              // TimeDateStamp
	binary.LittleEndian.PutUint16(edata[8:], 0)                              // MajorVersion
	binary.LittleEndian.PutUint16(edata[10:], 0)                             // MinorVersion
	binary.LittleEndian.PutUint32(edata[12:], edataRVA+uint32(g.dllNameOff)) // Name
	binary.LittleEndian.PutUint32(edata[16:], 1)                             // Base
	binary.LittleEndian.PutUint32(edata[20:], uint32(len(g.syms)))           // NumberOfFunctions
	binary.LittleEndian.PutUint32(edata[24:], uint32(len(g.syms)))           // NumberOfNames
	binary.LittleEndian.PutUint32(edata[28:], edataRVA+uint32(g.eatOff))     // AddressOfFunctions
	binary.LittleEndian.PutUint32(edata[32:], edataRVA+uint32(g.namesOff))   // AddressOfNames
	binary.LittleEndian.PutUint32(edata[36:], edataRVA+uint32(g.ordOff))     // AddressOfNameOrdinals

	copy(edata[g.dllNameOff:], dllName)

	strCursor := g.strOff
	for i, s := range g.syms {
		funcRVA := toRVA(s.Sym.VAddr, coreBase)
		binary.LittleEndian.PutUint32(edata[g.eatOff+i*4:], funcRVA)
		binary.LittleEndian.PutUint32(edata[g.namesOff+i*4:], edataRVA+uint32(strCursor))
		binary.LittleEndian.PutUint16(edata[g.ordOff+i*2:], uint16(i))
		copy(edata[strCursor:], s.Name)
		strCursor += len(s.Name) + 1
	}
}