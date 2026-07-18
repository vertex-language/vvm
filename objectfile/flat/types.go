// objectfile/flat/types.go
package flat

// Package flat produces raw binary images by concatenating sections in
// declaration order with no header, symbol table, or relocation records.
// This package is fully self-contained: it does not import any shared
// "object" package. Section and Symbol below are flat's own.
//
// Unlike elf, coff, and macho, flat has no Target/Arch/OS: nothing in a
// flat image's byte layout ever varies by architecture or operating
// system — a flat binary is just concatenated, pre-resolved bytes. Adding
// a Target type here just for API symmetry would be a fact this format
// doesn't have, so there isn't one.

// ── Section ───────────────────────────────────────────────────────────────

// SectionKind identifies what a Section is for. Flat binary has no section
// headers, so Kind only affects how the section's bytes are produced
// (SectionBSS emits VSize zero bytes; everything else emits Code as-is) —
// it has no bearing on layout or naming, unlike elf/coff/macho where Kind
// picks a section name and header flags.
type SectionKind int

const (
	SectionText SectionKind = iota
	SectionData
	SectionROData
	SectionBSS
	SectionUnwind
	SectionInitArray
	SectionFiniArray
	SectionTLS
	SectionCustom
)

// Section is the fundamental unit of content handed to File.AddSection.
//
// There is deliberately no Relocs field. Flat binary forbids relocations
// outright — every reference must already be resolved into Code before the
// section is added — so a flat section carrying relocations is now an
// impossible state to construct, rather than a runtime check in WriteTo.
// If a future caller needs relocations against a flat image, that's a
// different format, not an extension of this one.
type Section struct {
	Kind   SectionKind
	Custom string // non-empty only when Kind == SectionCustom; informational only
	Align  uint32 // alignment in bytes; tail-padding boundary. 0/1 = no padding

	Code  []byte // raw bytes; nil/empty for BSS
	VSize uint64 // zero-fill byte count for SectionBSS; 0 = emit nothing

	// Symbols is accepted for call-site symmetry with elf/coff/macho — a
	// caller assembling sections generically doesn't need a flat-specific
	// code path just to drop symbol info — but flat binary has no symbol
	// table to put them in, so this package's encoder never reads it.
	Symbols []Symbol
}

// ── Symbol ────────────────────────────────────────────────────────────────

// Binding and SymbolKind exist only so Symbol's shape matches the other
// format packages'; flat has no symbol table, so neither is consulted.
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

// Symbol is accepted by Section.Symbols and silently discarded — flat
// binary emits no symbol table. Kept only for API symmetry; see Symbols.
type Symbol struct {
	Name    string
	Offset  uint32
	Size    uint32
	Binding Binding
	Kind    SymbolKind
}