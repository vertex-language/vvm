package pe

// ── Section ──────────────────────────────────────────────────────────────────

// SectionFlags are format-agnostic section attributes used by the layout engine.
type SectionFlags uint32

const (
	SecAlloc   SectionFlags = 1 << 0 // occupies memory at runtime
	SecWrite   SectionFlags = 1 << 1 // writable at runtime
	SecExec    SectionFlags = 1 << 2 // executable
	SecTLS     SectionFlags = 1 << 3 // thread-local storage
	SecBSS     SectionFlags = 1 << 4 // no file bytes (zero-initialised)
	SecDiscard SectionFlags = 1 << 5 // discardable at runtime (e.g. .reloc)
)

// ObjectSection is one section from an input object file.
type ObjectSection struct {
	Name     string
	Flags    SectionFlags
	Data     []byte
	Size     uint64
	Align    uint64
	RawType  uint32
	RawFlags uint64
	Index    int
	Skip     bool
}

// ── Symbol ───────────────────────────────────────────────────────────────────

// COFFStorageClass mirrors the raw IMAGE_SYM_CLASS_* values from the COFF
// symbol table directly — decoded once, at parse time, and never translated
// into an ELF-shaped binding taxonomy (an earlier version of this package
// used an STB_*-mimicking SymBinding enum; COFF has its own storage classes
// and there's no reason to launder them through ELF's vocabulary).
type COFFStorageClass uint8

const (
	StorageClassExternal     COFFStorageClass = 2   // IMAGE_SYM_CLASS_EXTERNAL
	StorageClassStatic       COFFStorageClass = 3   // IMAGE_SYM_CLASS_STATIC
	StorageClassWeakExternal COFFStorageClass = 105 // IMAGE_SYM_CLASS_WEAK_EXTERNAL
)

func (c COFFStorageClass) IsExternal() bool { return c == StorageClassExternal }
func (c COFFStorageClass) IsWeak() bool     { return c == StorageClassWeakExternal }
func (c COFFStorageClass) IsLocal() bool    { return !c.IsExternal() && !c.IsWeak() }

// SectionIdx sentinels for ObjectSymbol — negative to distinguish from real indices (≥ 0).
const (
	SymSecUndef  = -1 // undefined / imported
	SymSecAbs    = -2 // absolute value
	SymSecCommon = -3 // tentative common block
)

// ObjectSymbol is one symbol from an input object's symbol table.
type ObjectSymbol struct {
	Name         string
	Value        uint64
	Size         uint64
	StorageClass COFFStorageClass
	IsFunction   bool // COFF symbol-type field's function bit (0x20), read directly
	Vis          uint8
	SectionIdx   int
	SectionName  string
}

// ── Relocation ───────────────────────────────────────────────────────────────

// ObjectReloc is one relocation entry from an input object, in COFF's native
// REL-style shape: the addend lives inline in the instruction bytes at parse
// time, not as an explicit field the format natively carries (that's a RELA
// convention — ELF's, not COFF's). Addend below is populated by extracting
// and zeroing that inline value during parsing (see object.go's
// coffReadAddend), purely for the patcher's convenience; it is not something
// COFF stores as a separate field on disk.
type ObjectReloc struct {
	TargetSectionIdx int
	Offset           uint64
	SymIdx           uint32
	Type             uint32
	Addend           int64
}

// ── Object ───────────────────────────────────────────────────────────────────

// Object is a parsed relocatable input object file.
// Sections[0] and Symbols[0] are always nil sentinels.
type Object struct {
	Name     string
	Machine  uint16
	EFlags   uint32
	Sections []*ObjectSection
	Symbols  []*ObjectSymbol
	Relocs   []*ObjectReloc
}

// ── Shared library (direct DLL parse) ────────────────────────────────────────

// SharedExport is one symbol exported from a dynamic library, read from its
// real export directory.
type SharedExport struct {
	Name    string
	Value   uint64
	Size    uint64
	Type    ExportKind
	Version string // export ordinal hint e.g. "@3"; empty if unavailable
}

// ExportKind is deliberately small — COFF/PE doesn't have ELF's STT_* symbol
// taxonomy, and the only distinction that ever matters here is "is this
// callable code."
type ExportKind uint8

const (
	ExportKindData ExportKind = iota
	ExportKindFunc
)

// SharedLib is a parsed dynamic library (.dll), opened directly via
// AddDynamicLibrary. Its Exports come from the real export directory — that
// part of parseDLL was always correct. Its ImportName does NOT: earlier
// versions used the export directory's own self-reported Name field as the
// string written into an importer's import table (ELF DT_SONAME semantics).
// PE has no equivalent convention any real linker honors this way, so that
// field is now purely diagnostic (InternalName) and never drives linking
// decisions — see shared.go's name-resolution priority.
type SharedLib struct {
	Name         string // the filename actually passed to AddDynamicLibrary
	InternalName string // the DLL's own self-reported export-directory name — informational only
	ImportName   string // the string written into the import table — see priority order in shared.go
	Needed       []string
	Exports      map[string]*SharedExport
}

// ── Base relocations ─────────────────────────────────────────────────────────

// BaseRelocSite records a VA at which an absolute pointer was written during
// relocation patching. The patcher accumulates these; the emitter builds .reloc.
type BaseRelocSite struct {
	VA uint64
}