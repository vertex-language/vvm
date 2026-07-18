// objectfile/coff/types.go
package coff

// ── Section ───────────────────────────────────────────────────────────────

// SectionKind identifies what a Section is for; the package maps it to the
// correct COFF section name and characteristics. Use SectionCustom for
// anything that doesn't fit the standard categories.
type SectionKind int

const (
	SectionText SectionKind = iota
	SectionData
	SectionROData
	SectionBSS
	SectionUnwind    // .pdata; pair with a SectionCustom ".xdata" section if needed
	SectionInitArray // .CRT$XCU
	SectionFiniArray // .CRT$XTZ
	SectionTLS       // .tls (Code set) or .tls$ZZZ (Code empty)
	SectionCustom    // Custom holds the literal section name
)

// SectionFlags are bit flags on a Section.
type SectionFlags uint32

const (
	// FlagLinkOnce marks the section IMAGE_SCN_LNK_COMDAT and attaches an
	// IMAGE_COMDAT_SELECT_ANY auxiliary record to the section symbol, keyed
	// on the section's first global symbol — whose name also becomes the
	// section's header name, so the linker can match identical definitions
	// across translation units.
	FlagLinkOnce SectionFlags = 1 << iota
	// FlagNoDeadStrip is accepted for parity with other section-flag call
	// sites but is not yet consulted by this package's COFF encoder — a
	// COFF object file leaves dead-stripping entirely to the linker, with
	// no per-section opt-out bit of its own to set. Reserved for when
	// that's added.
	FlagNoDeadStrip
)

// Section is the fundamental unit of content handed to File.AddSection.
type Section struct {
	Kind   SectionKind
	Custom string // non-empty only when Kind == SectionCustom
	Align  uint32 // alignment in bytes; 0 = format default for Kind

	Code  []byte // raw bytes; nil/empty for BSS and zero-fill TLS
	VSize uint64 // virtual size; for BSS/TLS may exceed len(Code); 0 = len(Code)

	Symbols []Symbol
	Relocs  []Reloc
	Flags   SectionFlags
}

// ── Symbol ────────────────────────────────────────────────────────────────

type Binding int

const (
	BindingLocal Binding = iota
	BindingGlobal
	BindingWeak
)

type SymbolKind int

const (
	SymFunc SymbolKind = iota
	SymData
	SymSection
)

// Symbol is defined at a byte offset within the Section that owns it.
type Symbol struct {
	Name    string
	Offset  uint32
	Size    uint32 // 0 = unknown / not specified
	Binding Binding
	Kind    SymbolKind

	// DLLExport marks the symbol for a /EXPORT:<name> linker directive.
	// Any symbol with DLLExport == true causes a synthetic .drectve section
	// to be appended at Serialize time.
	DLLExport bool
}

// ── Reloc ─────────────────────────────────────────────────────────────────

// RelocKind is COFF's own relocation vocabulary. Each value maps to a
// specific IMAGE_REL_<ARCH>_* type per machine in write.go's relocType; not
// every kind is valid for every machine, and relocType returns an error for
// an invalid pairing rather than silently picking something close.
type RelocKind int

const (
	RelocAbs64    RelocKind = iota // 64-bit VA
	RelocAbs32                     // 32-bit VA
	RelocPCRel32                   // 32-bit PC-relative (AMD64 REL32)
	RelocPLT32                     // 32-bit PC-relative, PLT-eligible (treated as REL32/BRANCH26)
	RelocPCRel26                   // ARM64 26-bit PC-relative B/BL
	RelocIAT                       // 32-bit image-relative (IAT)
	RelocAddr32NB                  // 32-bit image-relative, non-IAT
	RelocTLSIE                     // 32-bit section-relative (TLS IE)
)

func (k RelocKind) String() string {
	switch k {
	case RelocAbs64:
		return "abs64"
	case RelocAbs32:
		return "abs32"
	case RelocPCRel32:
		return "pcrel32"
	case RelocPLT32:
		return "plt32"
	case RelocPCRel26:
		return "pcrel26"
	case RelocIAT:
		return "iat"
	case RelocAddr32NB:
		return "addr32nb"
	case RelocTLSIE:
		return "tlsie"
	}
	return "reloc?"
}

// Reloc is a relocation to apply within a Section's Code.
//
// COFF's IMAGE_RELOCATION carries no addend field: Addend is patched into
// Code at Offset before the section is written, and the on-disk record
// itself carries no addend information at all. This is the one addend
// convention this package has to honor — unlike a shared Reloc type
// spanning multiple container formats, there's no ELF sibling to keep in
// sync with, so the convention is simply what COFF does. Mach-O's
// Reloc.Addend follows the same implicit-patch idea independently, and is
// free to diverge — see macho's own doc comment for its version.
type Reloc struct {
	Offset uint32 // byte offset within Section.Code where the fixup applies
	Symbol string // target symbol name; need not be defined in this object
	Kind   RelocKind
	Addend int64 // patched into Code; the emitted relocation record carries none
}