package pe

import (
	"fmt"
)

// DynLibOption configures a single AddDynamicLibrary call.
type DynLibOption func(*dynLibOptions)

type dynLibOptions struct {
	importName string
}

// WithImportName pins the string written into the import table for this
// DLL, overriding the default (the literal name/filename passed to
// AddDynamicLibrary). Never derived from the DLL's own export-directory
// self-report — see shared.go.
func WithImportName(name string) DynLibOption {
	return func(o *dynLibOptions) { o.importName = name }
}

// Linker constructs the PE32+ link pipeline.
type Linker struct {
	target      Target
	outputType  OutputType
	entry       string
	subsystem   Subsystem
	outputName  string // used as the export directory's own Name, for OutputShared builds
	majorOS     uint16
	minorOS     uint16
	majorSubsys uint16
	minorSubsys uint16
	libPaths    []string

	objects    []*Object
	archives   []*Archive
	importLibs []*ImportLibrary
	shared     []*SharedLib

	symtab *SymbolTable
}

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

func (l *Linker) SetOutputType(ot OutputType) { l.outputType = ot }
func (l *Linker) SetEntryPoint(entry string)   { l.entry = entry }
func (l *Linker) SetSubsystem(sub Subsystem)   { l.subsystem = sub }

// SetOutputName sets the name written into this image's own export
// directory (only meaningful for OutputShared builds). Supersedes v1's
// SetDLLName, which was stored but never reached EmitRequest — this one
// actually flows into emitPE.
func (l *Linker) SetOutputName(name string) { l.outputName = name }

func (l *Linker) SetMinOSVersion(major, minor uint16) {
	l.majorOS, l.minorOS = major, minor
	l.majorSubsys, l.minorSubsys = major, minor
}

func (l *Linker) AddLibraryPath(path string) { l.libPaths = append(l.libPaths, path) }

func (l *Linker) AddObject(name string, data []byte) error {
	obj, err := parseObject(name, data)
	if err != nil {
		return err
	}
	l.objects = append(l.objects, obj)
	return nil
}

// AddArchive parses and adds a static archive (real COFF object members).
func (l *Linker) AddArchive(name string, data []byte) error {
	ar, err := ParseArchive(name, data, parseObject)
	if err != nil {
		return err
	}
	l.archives = append(l.archives, ar)
	return nil
}

// AddImportLibrary parses and adds a short-format import library — the
// .lib normally generated alongside a DLL. Unlike AddDynamicLibrary, the
// DLL doesn't need to be present: the library carries author-chosen DLL
// names for each symbol.
func (l *Linker) AddImportLibrary(name string, data []byte) error {
	lib, err := ParseImportLibrary(name, data)
	if err != nil {
		return err
	}
	l.importLibs = append(l.importLibs, lib)
	return nil
}

// AddDynamicLibrary parses a real DLL directly: its export directory
// supplies the symbol list, and (per WithImportName, or else the literal
// name argument) the import-table string — never the export directory's
// own self-reported name.
func (l *Linker) AddDynamicLibrary(name string, data []byte, opts ...DynLibOption) error {
	var o dynLibOptions
	for _, opt := range opts {
		opt(&o)
	}
	lib, err := parseDLL(name, data, o.importName)
	if err != nil {
		return err
	}
	l.shared = append(l.shared, lib)
	return nil
}

func (l *Linker) Supported() bool {
	_, patcherOk := LookupPatcher(l.target)
	return patcherOk
}

func (l *Linker) Link() ([]byte, error) {
	if !l.Supported() {
		return nil, fmt.Errorf("no codegen backend registered for %s", l.target)
	}

	// 1. walkSharedDeps — not implemented; relies on explicit
	// AddDynamicLibrary/AddImportLibrary/AddArchive calls.

	// 2. SymbolTable.Ingest
	if err := l.symtab.Ingest(l.objects, l.archives, l.importLibs, l.shared); err != nil {
		return nil, err
	}

	// 3. MergeSections
	layout, err := MergeSections(l.objects)
	if err != nil {
		return nil, err
	}

	// 4. CollectImportSymbols
	importSyms := CollectImportSymbols(l.symtab, l.objects)

	// 5. GC (dead-section elimination)
	GC(layout, l.symtab, l.objects, l.outputType, l.entry)

	// 6. [If imports] computeIATLayout, InjectImportSections, computeIdataGeom, inject .idata
	var iatLayout *IATLayout
	var iGeom idataGeom
	var idataSec *MergedSection
	hasImports := len(importSyms) > 0

	if hasImports {
		iatLayout = computeIATLayout(importSyms)
		InjectImportSections(layout, importSyms, iatLayout)

		var importNames []string
		for _, s := range importSyms {
			importNames = append(importNames, s.Name)
		}
		iGeom = computeIdataGeom(l.symtab, importNames, iatLayout)

		idataFlags := SecAlloc | SecWrite
		idataSec = layout.AppendAllocSection(".idata", make([]byte, iGeom.size()), idataFlags, 4)
	}

	// 6b. [If exporting] compute the export directory's geometry — same
	// "append after GC, before AssignLayout" pattern as .idata above.
	var eGeom exportGeom
	var edataSec *MergedSection
	hasExports := l.outputType == OutputShared && l.outputName != ""
	if hasExports {
		var syms []ExportSymbol
		for _, c := range ExportCandidates(l.symtab) {
			syms = append(syms, ExportSymbol{Name: c.Name, Sym: c})
		}
		eGeom = computeExportGeom(l.outputName, syms)
		edataSec = layout.AppendAllocSection(".edata", make([]byte, eGeom.total), SecAlloc, 4)
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

	// 9. [If imports] PatchImportThunks & fillImports
	var imports *EmitImports
	if hasImports {
		pp, _ := LookupImportPatcher(l.target)
		if setter, ok := pp.(IATLayoutSetter); ok {
			setter.SetIATLayout(iatLayout)
		}

		if err := PatchImportThunks(pp, layout, importSyms); err != nil {
			return nil, err
		}

		iatSec, _ := layout.SectionByName(".iat")
		idataRVA := toRVA(idataSec.VAddr, baseVA)
		iatRVA := toRVA(iatSec.VAddr, baseVA)

		dirSz, iatSz := fillImports(idataSec.Data, iatSec.Data, idataRVA, iatRVA, iGeom)

		imports = &EmitImports{
			ImportDirRVA:  idataRVA,
			ImportDirSize: dirSz,
			IATRVA:        iatRVA,
			IATSize:       iatSz,
		}
	}

	// 9b. [If exporting] fillExportDirectory
	var exports *EmitExports
	if hasExports {
		edataRVA := toRVA(edataSec.VAddr, baseVA)
		fillExportDirectory(edataSec.Data, edataRVA, baseVA, l.outputName, eGeom)
		exports = &EmitExports{ExportDirRVA: edataRVA, ExportDirSize: uint32(eGeom.total)}
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
		Exports:               exports,
		MajorOSVersion:        l.majorOS,
		MinorOSVersion:        l.minorOS,
		MajorSubsystemVersion: l.majorSubsys,
		MinorSubsystemVersion: l.minorSubsys,
		Subsystem:             l.subsystem,
	}

	return emitPE(req)
}