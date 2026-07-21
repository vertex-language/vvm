// linker.go — ELF64 linker entry point and main Link pipeline (Target-based).
package elf

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EmitRequest carries all post-link data needed to produce the output binary.
type EmitRequest struct {
	Target     Target
	OutputType OutputType
	Entry      string
	Interp     string
	Soname     string
	Rpath      string
	Needed     []string
	Layout     *Layout
	Symtab     *SymbolTable
	PLTSyms    []string
}

// Linker is the ELF64 linker.
type Linker struct {
	target     Target
	outputType OutputType
	entry      string
	interp     string
	soname     string
	rpath      string
	libPaths   []string
	sysroot    string
	sysrootSet bool

	objects     []*Object
	archives    []*Archive
	shared      []*SharedLib
	extraNeeded []string
}

// NewLinker returns a Linker configured for Target t. If t is a cross build
// relative to the host, a sysroot is auto-probed (see probeSysroot); native
// builds skip probing and use absolute host paths. The interpreter is NOT
// set here — Link() sets it automatically once the output has shared-library
// dependencies. Call SetInterp to override explicitly.
func NewLinker(t Target) *Linker {
	l := &Linker{target: t}
	if !isNativeBuild(t) {
		if root, ok := probeSysroot(t); ok {
			l.sysroot = root
			l.sysrootSet = true
		}
	}
	return l
}

func (l *Linker) SetOutputType(t OutputType) { l.outputType = t }
func (l *Linker) SetEntryPoint(name string)  { l.entry = name }
func (l *Linker) SetInterp(path string)      { l.interp = path }
func (l *Linker) SetSoname(name string)      { l.soname = name }
func (l *Linker) SetRpath(path string)       { l.rpath = path }
func (l *Linker) AddLibraryPath(path string) { l.libPaths = append(l.libPaths, path) }
func (l *Linker) OutputType() OutputType     { return l.outputType }
func (l *Linker) Target() Target             { return l.target }

// SetSysroot overrides auto-detection explicitly.
func (l *Linker) SetSysroot(path string) {
	l.sysroot = path
	l.sysrootSet = path != ""
}

func (l *Linker) AddSONeeded(soname string) {
	l.extraNeeded = append(l.extraNeeded, soname)
}

func (l *Linker) AddObject(name string, data []byte) error {
	obj, err := ParseObject(name, data)
	if err != nil {
		return fmt.Errorf("AddObject %q: %w", name, err)
	}
	l.objects = append(l.objects, obj)
	return nil
}

func (l *Linker) AddArchive(name string, data []byte) error {
	ar, err := ParseArchive(name, data, ParseObject)
	if err != nil {
		return fmt.Errorf("AddArchive %q: %w", name, err)
	}
	l.archives = append(l.archives, ar)
	return nil
}

func (l *Linker) AddDynamicLibrary(name string, data []byte) error {
	lib, err := ParseSharedLib(name, data)
	if err != nil {
		return fmt.Errorf("AddDynamicLibrary %q: %w", name, err)
	}
	l.shared = append(l.shared, lib)
	return nil
}

// AddSystemLibrary locates soname on the linker's configured search path —
// explicit AddLibraryPath entries, then a sysroot-prefixed copy of the
// target's registered dirs (if a sysroot is active), then the target's
// registered dirs unprefixed (searchDirs()'s existing priority order) —
// and adds it as a dynamic-library dependency, exactly as if its bytes had
// been handed to AddDynamicLibrary directly. This is the same resolution
// logic walkSharedDeps already uses for transitive DT_NEEDED dependencies
// (findShared), just invoked directly against an explicit name instead of
// from the dependency-walk queue.
func (l *Linker) AddSystemLibrary(soname string) error {
	lib, err := l.findShared(soname, nil)
	if err != nil {
		return fmt.Errorf("system library %q: %w", soname, err)
	}
	l.shared = append(l.shared, lib)
	return nil
}

// AddDefaultNamespace adds whatever l.target's registered
// DefaultNamespaceFunc says provides the target's default symbol namespace
// (§7.4 — what an anonymous `extern :` group resolves against, e.g. libc
// on hosted OSes). It is a hard error, not a silent no-op, if l.target's
// arch has no DefaultNamespaceFunc registered at all — same "fail loudly"
// stance mustRegistered already takes for missing codegen backends, rather
// than than quietly emitting a binary with unresolved symbols. A
// registered function that legitimately has nothing to add for this
// particular (os, abi) combination returns an empty (not missing) slice,
// which is not an error — see defaultNamespace in registry.go for the
// registered/empty distinction.
func (l *Linker) AddDefaultNamespace() error {
	sonames, ok := defaultNamespace(l.target)
	if !ok {
		return fmt.Errorf(
			"elf: no default namespace registered for %s (blank-import its "+
				"subpackage, or use a named extern group with an explicit "+
				"`link shared \"...\"` instead)",
			l.target)
	}
	for _, soname := range sonames {
		if err := l.AddSystemLibrary(soname); err != nil {
			return fmt.Errorf("default namespace for %s: %w", l.target, err)
		}
	}
	return nil
}

// Link runs all linking phases and returns the native binary bytes.
func (l *Linker) Link() ([]byte, error) {
	if err := mustRegistered(l.target); err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}

	// Default entry point for position-dependent and PIE executables.
	if l.outputType != OutputShared && l.entry == "" {
		l.entry = "_start"
	}

	// Phase 1: transitive shared-library dependency walk.
	if err := l.walkSharedDeps(); err != nil {
		return nil, fmt.Errorf("link: dep walk: %w", err)
	}

	// Phase 2: symbol resolution.
	symtab := NewSymbolTable()
	allObjects := l.collectObjects()
	if err := symtab.Ingest(allObjects, l.archives, l.shared); err != nil {
		return nil, fmt.Errorf("link: symbol resolution: %w", err)
	}
	allObjects = l.collectObjects()

	// Phase 3: section merging.
	layout, err := MergeSections(allObjects)
	if err != nil {
		return nil, fmt.Errorf("link: merge: %w", err)
	}

	// Phase 4: dead-code elimination.
	// Must run BEFORE PLT injection — synthetic PLT sections have no Pieces,
	// so GC's reachability walk never marks them and they'd be deleted if it
	// ran after.
	GC(layout, symtab, allObjects, l.outputType, l.entry)

	// Phase 3b: PLT injection — sized per l.target's registered PLTPatcher.
	pltSyms := CollectPLTSymbols(symtab, allObjects)
	if len(pltSyms) > 0 {
		if err := InjectPLTSections(layout, pltSyms, l.target); err != nil {
			return nil, fmt.Errorf("link: plt inject: %w", err)
		}
	}

	// Phase 5: virtual address and file-offset assignment.
	if err := AssignLayout(l.outputType, layout, 0); err != nil {
		return nil, fmt.Errorf("link: layout: %w", err)
	}

	// Phase 6: resolve symbol virtual addresses.
	if err := ResolveSymbolAddresses(symtab, layout); err != nil {
		return nil, fmt.Errorf("link: resolve symbols: %w", err)
	}

	// Phase 7: write PLT stubs and assign stub VAddrs to shared symbols.
	if len(pltSyms) > 0 {
		pltPatcher, _ := LookupPLTPatcher(l.target) // presence guaranteed by mustRegistered above
		if err := PatchPLT(pltPatcher, layout, pltSyms); err != nil {
			return nil, fmt.Errorf("link: PLT patch: %w", err)
		}
	}

	// Phase 8: relocation patching.
	patcher, _ := LookupPatcher(l.target)
	if err := PatchAll(layout, symtab, allObjects, patcher); err != nil {
		return nil, fmt.Errorf("link: reloc patch: %w", err)
	}

	// Phase 9: collect DT_NEEDED / import list.
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

	if l.outputType != OutputShared && len(needed) > 0 && l.interp == "" {
		if interp := defaultInterp(l.target); interp != "" {
			l.interp = interp
		}
	}

	// Phase 10: emit the binary.
	req := &EmitRequest{
		Target:     l.target,
		OutputType: l.outputType,
		Entry:      l.entry,
		Interp:     l.interp,
		Soname:     l.soname,
		Rpath:      l.rpath,
		Needed:     needed,
		Layout:     layout,
		Symtab:     symtab,
		PLTSyms:    pltSymNames(pltSyms),
	}
	out, err := Emit(req)
	if err != nil {
		return nil, fmt.Errorf("link: emit: %w", err)
	}
	return out, nil
}

// ── search path / sysroot resolution ──────────────────────────────────────────

// searchDirs returns, in priority order: explicit AddLibraryPath entries,
// then sysroot-prefixed target dirs (if a sysroot is active), then the
// unprefixed target dirs. Project-resolved paths always win over whatever
// the host happens to have lying around.
func (l *Linker) searchDirs() []string {
	dirs := append([]string{}, l.libPaths...)
	target := defaultSearchDirs(l.target)
	if l.sysrootSet && l.sysroot != "" {
		for _, d := range target {
			dirs = append(dirs, filepath.Join(l.sysroot, d))
		}
	}
	dirs = append(dirs, target...)
	return dirs
}

func hostArch() Arch {
	switch runtime.GOARCH {
	case "amd64":
		return ArchX86_64
	case "386":
		return ArchX86
	case "arm64":
		return ArchARM64
	case "arm":
		return ArchARM
	case "riscv64":
		return ArchRISCV64
	case "ppc64le", "ppc64":
		return ArchPowerPC64
	case "s390x":
		return ArchS390X
	case "loong64":
		return ArchLoongArch64
	case "mips64le":
		return ArchMIPS64
	}
	return 0
}

func hostOS() OS {
	switch runtime.GOOS {
	case "linux":
		return OSLinux
	case "freebsd":
		return OSFreeBSD
	case "netbsd":
		return OSNetBSD
	case "openbsd":
		return OSOpenBSD
	case "android":
		return OSAndroid
	}
	return OSNone
}

func isNativeBuild(t Target) bool {
	return t.Arch == hostArch() && t.OS == hostOS()
}

// probeSysroot tries a small set of conventional cross-sysroot locations
// (as used by Debian/Ubuntu's cross-toolchain packaging) and returns the
// first that exists on disk. This is intentionally conservative — it does
// not attempt to invoke a cross-gcc to ask it for its own sysroot the way a
// fuller AutoSysroot implementation would.
func probeSysroot(t Target) (string, bool) {
	triple := crossTripleGuess(t)
	if triple == "" {
		return "", false
	}
	candidates := []string{
		filepath.Join("/usr", triple),
		filepath.Join("/usr", "local", triple),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c, true
		}
	}
	return "", false
}

func crossTripleGuess(t Target) string {
	var archPart string
	switch t.Arch {
	case ArchX86_64:
		archPart = "x86_64"
	case ArchARM64:
		if t.BigEndian {
			archPart = "aarch64_be"
		} else {
			archPart = "aarch64"
		}
	case ArchARM:
		if t.BigEndian {
			archPart = "armeb"
		} else {
			archPart = "arm"
		}
	case ArchRISCV64:
		archPart = "riscv64"
	default:
		return ""
	}
	abiPart := "gnu"
	switch t.ABI {
	case ABIEABIHF:
		abiPart = "gnueabihf"
	case ABIEABI:
		abiPart = "gnueabi"
	}
	return archPart + "-linux-" + abiPart
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
	dirs := append(append([]string{}, rpaths...), l.searchDirs()...)
	for _, dir := range dirs {
		path := filepath.Join(dir, soname)
		data, err := os.ReadFile(path)
		if err == nil {
			return ParseSharedLib(soname, data)
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