// objectfile/macho/types.go
package macho

// ── Section ───────────────────────────────────────────────────────────────

// SectionKind identifies what a Section is for; the package maps it to the
// correct Mach-O segment/section name pair and S_* flags. Use
// SectionCustom for anything that doesn't fit the standard categories.
type SectionKind int

const (
	SectionText SectionKind = iota
	SectionData
	SectionROData
	SectionBSS
	SectionUnwind    // __TEXT,__unwind_info (compact unwind records)
	SectionInitArray // __DATA,__mod_init_func
	SectionFiniArray // __DATA,__mod_term_func
	SectionTLS       // __DATA,__thread_data (Code set) or __thread_bss (Code empty)
	SectionCustom    // Custom holds "segment,section", e.g. "__TEXT,__eh_frame"
)

// SectionFlags are bit flags on a Section.
type SectionFlags uint32

const (
	// FlagLinkOnce marks every global/weak symbol in the section
	// N_WEAK_DEF, so the linker keeps exactly one copy across translation
	// units — Mach-O's equivalent of ELF's SHT_GROUP COMDAT.
	FlagLinkOnce SectionFlags = 1 << iota
	// FlagNoDeadStrip sets S_ATTR_NO_DEAD_STRIP on the section, telling the
	// linker never to dead-strip it even with no visible references.
	// Mach-O has a direct native flag for this — unlike ELF, where the
	// equivalent field is accepted but not yet consulted by elf's encoder.
	// Same flag name, two different levels of support; that's fine.
	FlagNoDeadStrip
)

// Section is the fundamental unit of content handed to File.AddSection.
type Section struct {
	Kind   SectionKind
	Custom string // "segment,section"; non-empty only when Kind == SectionCustom
	Align  uint32 // alignment in bytes; 0 = format default for Kind

	Code  []byte // raw bytes; nil/empty for BSS and zero-fill TLS
	VSize uint64 // virtual size; for BSS may exceed len(Code); 0 = len(Code)

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

// SymbolKind is accepted for parity with elf.SymbolKind / coff.SymbolKind —
// the public shape link/ adapters walk is symmetric across format
// packages — but Mach-O's nlist_64 has no function/data distinction beyond
// N_SECT itself, so this package's encoder does not consult it today.
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
	Size    uint32 // accepted for parity; nlist_64 carries no symbol size
	Binding Binding
	Kind    SymbolKind
}

// ── Reloc ─────────────────────────────────────────────────────────────────

// RelocKind is Mach-O's own relocation vocabulary. Each value maps to a
// specific r_type per cpu_type in relocDesc; not every kind is valid for
// every cpu_type (e.g. RelocPCRel26 only exists for ARM64), and relocDesc
// returns an error for an invalid pairing rather than silently picking
// something close.
type RelocKind int

const (
	RelocAbs64    RelocKind = iota // X86_64_RELOC_UNSIGNED / ARM64_RELOC_UNSIGNED
	RelocPCRel32                   // X86_64_RELOC_BRANCH (32-bit PC-relative CALL/JMP)
	RelocGOTLoad                   // X86_64_RELOC_GOT_LOAD (MOVQ load of GOT entry)
	RelocPCRel26                   // ARM64_RELOC_BRANCH26 (BL/B)
	RelocADRPage21                 // ARM64_RELOC_PAGE21 (ADRP)
	RelocAddOff12                  // ARM64_RELOC_PAGEOFF12 (ADD/LDR page offset)
	RelocGOTPage21                 // ARM64_RELOC_GOT_LOAD_PAGE21
	RelocGOTOff12                  // ARM64_RELOC_GOT_LOAD_PAGEOFF12
	RelocTLSGD                     // X86_64_RELOC_TLV / ARM64_RELOC_TLVP_LOAD_PAGE21
)

func (k RelocKind) String() string {
	switch k {
	case RelocAbs64:
		return "abs64"
	case RelocPCRel32:
		return "pcrel32"
	case RelocGOTLoad:
		return "gotload"
	case RelocPCRel26:
		return "pcrel26"
	case RelocADRPage21:
		return "adrpage21"
	case RelocAddOff12:
		return "addoff12"
	case RelocGOTPage21:
		return "gotpage21"
	case RelocGOTOff12:
		return "gotoff12"
	case RelocTLSGD:
		return "tlsgd"
	}
	return "reloc?"
}

// Reloc is a relocation to apply within a Section's Code.
//
// Mach-O stores no addend in the relocation record (implicit addends):
// Addend is patched directly into Code at Offset before the section bytes
// are emitted, and the on-disk relocation_info always carries zero. This
// is the one addend convention this package has to honor — independently
// true of Mach-O, not borrowed from coff's version of the same idea, and
// free to diverge from it (e.g. in whether a zero addend still triggers a
// write) without either package asking the other's permission.
type Reloc struct {
	Offset uint32 // byte offset within Section.Code where the fixup applies
	Symbol string // target symbol name; need not be defined in this object
	Kind   RelocKind
	Addend int64 // patched into Code[Offset:]; on-disk record carries zero
}