// objectfile/elf/types.go
package elf

// ── Section ───────────────────────────────────────────────────────────────

// SectionKind identifies what a Section is for; the package maps it to the
// correct ELF section name and sh_flags. Use SectionCustom for anything
// that doesn't fit the standard categories.
type SectionKind int

const (
	SectionText SectionKind = iota
	SectionData
	SectionROData
	SectionBSS
	SectionUnwind    // .eh_frame
	SectionInitArray // .init_array
	SectionFiniArray // .fini_array
	SectionTLS       // .tdata (Code set) or .tbss (Code empty)
	SectionCustom    // Custom holds the literal section name
)

// SectionFlags are bit flags on a Section.
type SectionFlags uint32

const (
	// FlagLinkOnce emits an SHT_GROUP COMDAT group keyed on the section's
	// first global/weak symbol, so the linker keeps exactly one copy
	// across translation units.
	FlagLinkOnce SectionFlags = 1 << iota
	// FlagNoDeadStrip is accepted for parity with other section-flag call
	// sites but is not yet consulted by this package's ELF encoder — ELF's
	// linker-driven dead-stripping has no direct sh_flags equivalent to
	// Mach-O's S_ATTR_NO_DEAD_STRIP. Reserved for when that's added.
	FlagNoDeadStrip
)

// Section is the fundamental unit of content handed to File.AddSection.
type Section struct {
	Kind   SectionKind
	Custom string // non-empty only when Kind == SectionCustom
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
}

// ── Reloc ─────────────────────────────────────────────────────────────────

// RelocKind is ELF's own relocation vocabulary. Each value maps to a
// specific R_<ARCH>_* type per e_machine in write.go's relocType; not every
// kind is valid for every machine (e.g. RelocRISCVHI20 only exists for
// RISC-V), and relocType returns an error for an invalid pairing rather
// than silently picking something close.
type RelocKind int

const (
	RelocAbs64 RelocKind = iota // S + A, 8-byte field
	RelocAbs32                  // S + A, 4-byte field
	RelocPCRel32                // S + A - P (calls/data, AMD64 & i386)
	RelocPLT32                  // S + A - P, PLT-eligible (AMD64)
	RelocGOTLoad                // GOT-relative load (AMD64 GOTPCREL, i386 GOT32)
	RelocPCRel26                // AArch64 CALL26 (BL)
	RelocADRPage21              // AArch64 ADRP page
	RelocAddOff12                // AArch64 ADD/LDR low-12 page offset
	RelocGOTPage21               // AArch64 GOT-relative ADRP
	RelocGOTOff12                 // AArch64 GOT-relative low-12
	RelocRISCVCall                // RISC-V CALL_PLT
	RelocRISCVHI20                // RISC-V HI20
	RelocRISCVLO12I                // RISC-V LO12 (I-type)
	RelocRISCVLO12S                 // RISC-V LO12 (S-type)
	RelocTLSGD                       // general-dynamic TLS (all arches)
	RelocTLSIE                        // initial-exec TLS (all arches)
	RelocTLSLE                         // local-exec TLS (all arches)
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
	case RelocRISCVCall:
		return "riscv_call"
	case RelocRISCVHI20:
		return "riscv_hi20"
	case RelocRISCVLO12I:
		return "riscv_lo12i"
	case RelocRISCVLO12S:
		return "riscv_lo12s"
	case RelocTLSGD:
		return "tlsgd"
	case RelocTLSIE:
		return "tlsie"
	case RelocTLSLE:
		return "tlsle"
	}
	return "reloc?"
}

// Reloc is a relocation to apply within a Section's Code.
//
// ELF uses SHT_RELA (explicit addends): Addend flows directly into
// r_addend and Code is never patched. This is the one addend convention
// this package has to honor — unlike a shared Reloc type spanning multiple
// container formats, there's no COFF/Mach-O sibling to keep in sync with,
// so the convention is simply what ELF does.
type Reloc struct {
	Offset uint32 // byte offset within Section.Code where the fixup applies
	Symbol string // target symbol name; need not be defined in this object
	Kind   RelocKind
	Addend int64 // written verbatim into r_addend
}