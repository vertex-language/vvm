package pe

import (
	"fmt"
)

// Linker constructs the PE32+ link pipeline.
type Linker struct {
	target      Target
	outputType  OutputType
	entry       string
	subsystem   Subsystem
	dllName     string
	majorOS     uint16
	minorOS     uint16
	majorSubsys uint16
	minorSubsys uint16
	libPaths    []string

	objects        []*Object
	archives       []*Archive
	shared         []*SharedLib
	explicitNeeded []string

	symtab *SymbolTable
}

// NewLinker initializes a PE32+ linker for the given target with standard defaults.
func NewLinker(t Target) *Linker {
	l := &Linker{
		target:      t,
		outputType:  OutputExec,
		subsystem:   defaultSubsystem(t),
		majorOS:     6,
		minorOS:     1,
		majorSubsys: 6,
		minorSubsys: 1,
		symtab:      NewSymbolTable(),
	}
	if ep, ok := lookupDefaultEntryPoint(t); ok {
		l.entry = ep
	} else {
		l.entry = "mainCRTStartup"
	}
	return l
}

// SetOutputType sets the kind of binary to produce (executable, PIE, or shared).
func (l *Linker) SetOutputType(ot OutputType) { l.outputType = ot }

// SetEntryPoint sets the entry symbol name.
func (l *Linker) SetEntryPoint(entry string) { l.entry = entry }

// SetSubsystem sets the Windows/UEFI subsystem.
func (l *Linker) SetSubsystem(sub Subsystem) { l.subsystem = sub }

// SetDLLName records the DLL name, though currently unused in the final export directory.
func (l *Linker) SetDLLName(name string) { l.dllName = name }

// SetMinOSVersion updates both OS and Subsystem versions simultaneously.
func (l *Linker) SetMinOSVersion(major, minor uint16) {
	l.majorOS, l.minorOS = major, minor
	l.majorSubsys, l.minorSubsys = major, minor
}

// AddLibraryPath adds a directory to the library search path.
func (l *Linker) AddLibraryPath(path string) { l.libPaths = append(l.libPaths, path) }

// AddObject parses and adds a COFF object file to the link.
func (l *Linker) AddObject(name string, data []byte) error {
	obj, err := parseObject(name, data)
	if err != nil {
		return err
	}
	l.objects = append(l.objects, obj)
	return nil
}

// AddArchive parses and adds a static archive (.lib/.a) to the link.
func (l *Linker) AddArchive(name string, data []byte) error {
	ar, err := ParseArchive(name, data, parseObject)
	if err != nil {
		return err
	}
	l.archives = append(l.archives, ar)
	return nil
}

// AddDynamicLibrary parses and adds a shared library (.dll) to the link.
func (l *Linker) AddDynamicLibrary(name string, data []byte) error {
	lib, err := parseDLL(name, data)
	if err != nil {
		return err
	}
	l.shared = append(l.shared, lib)
	return nil
}

// AddDLLNeeded explicitly adds a required DLL name to the import directory.
func (l *Linker) AddDLLNeeded(name string) {
	l.explicitNeeded = append(l.explicitNeeded, name)
}

// Supported reports whether a codegen backend is registered for the linker's target.
func (l *Linker) Supported() bool {
	_, patcherOk := LookupPatcher(l.target)
	return patcherOk
}

// Link runs the end-to-end linker pipeline and returns the serialized PE32+ image.
func (l *Linker) Link() ([]byte, error) {
	if !l.Supported() {
		return nil, fmt.Errorf("no codegen backend registered for %s", l.target)
	}

	// 1. walkSharedDeps 
	// Skipped here as it requires filesystem traversal outside package scope; relies on explicit AddDynamicLibrary calls for now.
	
	// 2. SymbolTable.Ingest
	if err := l.symtab.Ingest(l.objects, l.archives, l.shared); err != nil {
		return nil, err
	}

	// 3. MergeSections
	layout, err := MergeSections(l.objects)
	if err != nil {
		return nil, err
	}

	// 4. CollectPLTSymbols
	pltSyms := CollectPLTSymbols(l.symtab, l.objects)

	// 5. GC (Dead-section elimination)
	GC(layout, l.symtab, l.objects, l.outputType, l.entry)

	// 6. [If PLT] InjectPLTSections, computeIATLayout, computeIdataGeom, inject .idata
	var iatLayout *IATLayout
	var iGeom idataGeom
	var idataSec *MergedSection
	hasPLT := len(pltSyms) > 0

	if hasPLT {
		InjectPLTSections(layout, pltSyms)
		iatLayout = computeIATLayout(pltSyms)

		var pltNames []string
		for _, s := range pltSyms {
			pltNames = append(pltNames, s.Name)
		}
		
		iGeom = computeIdataGeom(l.symtab, pltNames, iatLayout)
		
		idataFlags := SecAlloc | SecWrite 
		idataSec = layout.AppendAllocSection(".idata", make([]byte, iGeom.size()), idataFlags, 4)
	}

	// 7. AssignLayout
	baseVA := coreBaseVA(l.outputType)
	if err := AssignLayout(l.outputType, layout, baseVA); err != nil {
		return nil, err
	}

	// 8. ResolveSymbolAddresses
	if err := ResolveSymbolAddresses(l.symtab, layout); err != nil {
		return nil, err
	}

	// 9. [If PLT] PatchPLT & fillImports
	var imports *EmitImports
	if hasPLT {
		pp, _ := LookupPLTPatcher(l.target)
		if setter, ok := pp.(IATLayoutSetter); ok {
			setter.SetIATLayout(iatLayout)
		}
		
		if err := PatchPLT(pp, layout, pltSyms); err != nil {
			return nil, err
		}

		gotSec, _ := layout.SectionByName(".got.plt")
		idataRVA := toRVA(idataSec.VAddr, baseVA)
		gotRVA := toRVA(gotSec.VAddr, baseVA)
		
		dirSz, iatSz := fillImports(idataSec.Data, gotSec.Data, idataRVA, gotRVA, iGeom)
		
		imports = &EmitImports{
			ImportDirRVA:  idataRVA,
			ImportDirSize: dirSz,
			IATRVA:        gotRVA + uint32(gotReserved*8),
			IATSize:       iatSz,
		}
	}

	// 10. PatchAll
	patcher, _ := LookupPatcher(l.target)
	if setter, ok := patcher.(CoreBaseSetter); ok {
		setter.SetCoreBase(baseVA)
	}
	if err := PatchAll(layout, l.symtab, l.objects, patcher); err != nil {
		return nil, err
	}

	// 11. buildBaseRelocSection
	if l.outputType != OutputExec {
		if collector, ok := patcher.(BaseRelocCollector); ok {
			sites := collector.BaseRelocSites()
			if len(sites) > 0 {
				relocData := buildBaseRelocSection(sites, baseVA)
				if len(relocData) > 0 {
					layout.AppendAllocSection(".reloc", relocData, SecAlloc|SecDiscard, 4)
				}
			}
		}
	}

	// 12. emitPE
	req := &EmitRequest{
		OutputType:            l.outputType,
		Target:                l.target,
		Layout:                layout,
		Entry:                 l.entry,
		Symtab:                l.symtab,
		Imports:               imports,
		MajorOSVersion:        l.majorOS,
		MinorOSVersion:        l.minorOS,
		MajorSubsystemVersion: l.majorSubsys,
		MinorSubsystemVersion: l.minorSubsys,
		Subsystem:             l.subsystem,
	}

	return emitPE(req)
}