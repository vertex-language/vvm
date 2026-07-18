package macho

import (
	"fmt"
	"os"
	"path/filepath"
)

type emitRequestBuilder = emitRequest // alias for readability in comments

type Linker struct {
	target     Target
	outputType OutputType
	entry      string
	interp     string
	soname     string
	rpath      string
	sysroot    string
	zippered   bool
	libPaths   []string

	objects     []*Object
	archives    []*Archive
	shared      []*SharedLib
	extraNeeded []string
}

func NewLinker(t Target) *Linker { return &Linker{target: t} }

func (l *Linker) SetOutputType(t OutputType) { l.outputType = t }
func (l *Linker) SetEntryPoint(name string)  { l.entry = name }
func (l *Linker) SetInterp(path string)      { l.interp = path }
func (l *Linker) SetSoname(name string)      { l.soname = name }
func (l *Linker) SetRpath(path string)       { l.rpath = path }
func (l *Linker) SetSysroot(path string)     { l.sysroot = path }
func (l *Linker) AddLibraryPath(path string) { l.libPaths = append(l.libPaths, path) }
func (l *Linker) OutputType() OutputType     { return l.outputType }
func (l *Linker) AddSONeeded(s string)       { l.extraNeeded = append(l.extraNeeded, s) }

// Supported reports whether a codegen backend is registered for this
// Linker's Target.Arch (i.e. whether the arch subpackage was blank-imported).
func (l *Linker) Supported() bool { return Supported(l.target) }

// SetZippered marks the output as carrying both PLATFORM_MACOS and
// PLATFORM_MACCATALYST LC_BUILD_VERSION commands. Valid only for
// macosx device targets (Target.Environment == EnvNone).
func (l *Linker) SetZippered(z bool) { l.zippered = z }

func (l *Linker) AddObject(name string, data []byte) error {
	obj, err := parseObject(name, data, l.target)
	if err != nil {
		return fmt.Errorf("AddObject %q: %w", name, err)
	}
	l.objects = append(l.objects, obj)
	return nil
}

func (l *Linker) AddArchive(name string, data []byte) error {
	ar, err := ParseArchive(name, data, func(n string, d []byte) (*Object, error) {
		return parseObject(n, d, l.target)
	})
	if err != nil {
		return fmt.Errorf("AddArchive %q: %w", name, err)
	}
	l.archives = append(l.archives, ar)
	return nil
}

func (l *Linker) AddDynamicLibrary(name string, data []byte) error {
	if len(data) == 0 {
		l.shared = append(l.shared, &SharedLib{Name: name, Soname: name})
		return nil
	}
	lib, err := parseSharedLib(name, data)
	if err != nil {
		return fmt.Errorf("AddDynamicLibrary %q: %w", name, err)
	}
	l.shared = append(l.shared, lib)
	return nil
}

func (l *Linker) AddCachedDylib(name string, symbols []string) {
	exports := make(map[string]*SharedExport, len(symbols))
	for _, sym := range symbols {
		mangled := "_" + sym
		exports[mangled] = &SharedExport{Name: mangled, Binding: BindGlobal, Type: SymTypeFunc}
	}
	l.shared = append(l.shared, &SharedLib{Name: name, Soname: name, Exports: exports})
}

// AddFramework registers a dyld-shared-cache-only Apple framework by its
// bare name (e.g. "Foundation", "UIKit", "CoreGraphics") — no need to spell
// out the "<Name>.framework/<Name>" path yourself. Internally this builds
// that path and delegates to AddCachedDylib, so the framework's exports are
// pre-registered for symbol resolution rather than relying on the blunt
// "first stub lib absorbs leftover undefineds" fallback in SymbolTable.Ingest.
//
// findInstallPath (builder.go) rewrites any Soname containing ".framework/"
// to "/System/Library/Frameworks/<path>" at emit time, so the resulting
// LC_LOAD_DYLIB install path comes out correct automatically.
func (l *Linker) AddFramework(name string, symbols []string) {
	path := name + ".framework/" + name
	l.AddCachedDylib(path, symbols)
}

func (l *Linker) Link() ([]byte, error) {
	if !l.Supported() {
		return nil, fmt.Errorf("macho: no codegen backend registered for %s (blank-import its subpackage)", l.target.Arch)
	}
	if err := l.target.Valid(); err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}

	patcher, _ := lookupPatcher(l.target)
	pltPatcher, _ := lookupPLTPatcher(l.target)

	if l.interp == "" {
		l.interp = lookupDefaultInterp(l.target)
	}

	if err := l.walkSharedDeps(); err != nil {
		return nil, fmt.Errorf("link: dep walk: %w", err)
	}

	symtab := NewSymbolTable()
	allObjects := l.collectObjects()
	if err := symtab.Ingest(allObjects, l.archives, l.shared); err != nil {
		return nil, fmt.Errorf("link: symbol resolution: %w", err)
	}
	allObjects = l.collectObjects()

	layout, err := MergeSections(allObjects)
	if err != nil {
		return nil, fmt.Errorf("link: merge: %w", err)
	}

	pltSyms := collectPLTSymbols(symtab, allObjects)
	if len(pltSyms) > 0 {
		if err := injectMachoPLT(pltPatcher, layout, pltSyms); err != nil {
			return nil, fmt.Errorf("link: inject PLT: %w", err)
		}
	}

	GC(layout, symtab, allObjects, l.outputType, l.entry)

	baseVA := uint64(0)
	if l.outputType != OutputShared {
		baseVA = 0x100000000
	}
	if err := AssignLayout(l.outputType, layout, baseVA); err != nil {
		return nil, fmt.Errorf("link: layout: %w", err)
	}

	if err := ResolveSymbolAddresses(symtab, layout); err != nil {
		return nil, fmt.Errorf("link: resolve symbols: %w", err)
	}

	var stubs StubMap
	if len(pltSyms) > 0 {
		stubs, err = patchPLT(pltPatcher, layout, pltSyms)
		if err != nil {
			return nil, fmt.Errorf("link: PLT patch: %w", err)
		}
	}

	if err := PatchAll(layout, symtab, allObjects, patcher, stubs); err != nil {
		return nil, fmt.Errorf("link: reloc patch: %w", err)
	}

	needed := collectNeeded(l.shared)
	seen := make(map[string]bool, len(needed))
	for _, n := range needed {
		seen[n] = true
	}
	for _, n := range l.extraNeeded {
		if !seen[n] {
			seen[n] = true
			needed = append(needed, n)
		}
	}

	req := &emitRequest{
		Target:     l.target,
		OutputType: l.outputType,
		Zippered:   l.zippered,
		Entry:      l.entry,
		Interp:     l.interp,
		Soname:     l.soname,
		Rpath:      l.rpath,
		Needed:     needed,
		Layout:     layout,
		Symtab:     symtab,
		PLTSyms:    pltSymNames(pltSyms),
		StubSize:   pltPatcher.StubSize(),
	}
	out, err := emitMachO(req)
	if err != nil {
		return nil, fmt.Errorf("link: emit: %w", err)
	}
	return out, nil
}

func (l *Linker) walkSharedDeps() error {
	seen := make(map[string]bool)
	for _, s := range l.shared {
		seen[s.Soname] = true
	}
	queue := make([]*SharedLib, len(l.shared))
	copy(queue, l.shared)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, soname := range cur.Needed {
			if seen[soname] {
				continue
			}
			seen[soname] = true
			dep, err := l.findShared(soname, cur.Rpaths)
			if err != nil {
				return fmt.Errorf("loading %s (needed by %s): %w", soname, cur.Soname, err)
			}
			l.shared = append(l.shared, dep)
			queue = append(queue, dep)
		}
	}
	return nil
}

func (l *Linker) findShared(soname string, rpaths []string) (*SharedLib, error) {
	dirs := append(append([]string{}, rpaths...), l.libPaths...)
	if root, err := resolveSysroot(l.target, l.sysroot); err == nil {
		dirs = append(dirs, filepath.Join(root, "usr", "lib"))
	}
	dirs = append(dirs, "/usr/lib", "/usr/local/lib")
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, soname))
		if err == nil {
			return parseSharedLib(soname, data)
		}
	}
	return nil, fmt.Errorf("shared library %q not found", soname)
}

func (l *Linker) collectObjects() []*Object {
	out := make([]*Object, len(l.objects))
	copy(out, l.objects)
	for _, ar := range l.archives {
		for _, m := range ar.Members {
			if m.obj != nil {
				out = append(out, m.obj)
			}
		}
	}
	return out
}

func collectNeeded(libs []*SharedLib) []string {
	seen := make(map[string]bool)
	var out []string
	for _, lib := range libs {
		if !seen[lib.Soname] {
			seen[lib.Soname] = true
			out = append(out, lib.Soname)
		}
	}
	return out
}

func pltSymNames(syms []PLTEntry) []string {
	if len(syms) == 0 {
		return nil
	}
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Name
	}
	return out
}