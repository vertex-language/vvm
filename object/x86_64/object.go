// Package object translates a lowered x86_64.Program into a generic,
// container-agnostic description of sections, symbols, and relocations —
// arrow 4 of the README taxonomy.
//
// The only arch-specific knowledge this package adds is the
// encoder.FixupKind -> RelocKind mapping. The encoder (isa/x86_64/encoder)
// only distinguishes three fixup shapes — FixupPCRel32, FixupAbs32, and
// FixupAbs64 — and does not separately tag PLT-eligible branch sites: both
// call_sym/jmp_sym and RIP-relative data references emit FixupPCRel32.
// This package mirrors that vocabulary exactly rather than inventing a
// PLT32 distinction the encoder has no way to produce. This package does
// not import objectfile and knows nothing about ELF/COFF/Mach-O.
package object

import (
	"github.com/vertex-language/vvm/isa/x86_64/encoder"
	x86_64 "github.com/vertex-language/vvm/lower/x86_64"
)

type SectionKind int

const (
	SectionText SectionKind = iota
	SectionData
	SectionROData
	SectionBSS
	SectionTLSData
	SectionTLSBSS
)

func (k SectionKind) String() string {
	switch k {
	case SectionText:
		return "text"
	case SectionData:
		return "data"
	case SectionROData:
		return "rodata"
	case SectionBSS:
		return "bss"
	case SectionTLSData:
		return "tdata"
	case SectionTLSBSS:
		return "tbss"
	}
	return "section?"
}

type Section struct {
	Kind    SectionKind
	Name    string
	Align   uint32
	Size    uint32 // total size; for BSS kinds Code is nil and only Size matters
	Code    []byte
	Symbols []Symbol
	Relocs  []Reloc
}

type Symbol struct {
	Name   string
	Offset uint32
	Size   uint32
	Export bool
}

type RelocKind int

const (
	// RelocPCRel32: field := S + A - P, where P is the field's address.
	// Covers both branch sites (call/jmp rel32) and RIP-relative data
	// references — the encoder does not distinguish them (both are
	// encoder.FixupPCRel32).
	RelocPCRel32 RelocKind = iota
	// RelocAbs32: field := S + A, a 4-byte field (encoder.FixupAbs32:
	// symbolic 32-bit immediates and absolute [msym+disp] operands).
	RelocAbs32
	// RelocAbs64: field := S + A, 8-byte field (encoder.FixupAbs64: a
	// movabs r64, imm64 whose immediate is a symbol address).
	RelocAbs64
)

func (k RelocKind) String() string {
	switch k {
	case RelocPCRel32:
		return "pcrel32"
	case RelocAbs32:
		return "abs32"
	case RelocAbs64:
		return "abs64"
	}
	return "reloc?"
}

type Reloc struct {
	Offset uint32
	Symbol string
	Kind   RelocKind
	Addend int64
}

func relocKind(k encoder.FixupKind) RelocKind {
	switch k {
	case encoder.FixupPCRel32:
		return RelocPCRel32
	case encoder.FixupAbs32:
		return RelocAbs32
	}
	return RelocAbs64
}

func alignUp(n, a uint32) uint32 {
	if a == 0 {
		a = 1
	}
	return (n + a - 1) &^ (a - 1)
}

// FromProgram lays the program out into sections. Function code is
// concatenated into one text section (per-function alignment, NOP padding);
// initialized globals go to data, zero-initialized to bss, with tdata/tbss
// for TLS. Fixup offsets are rebased to section offsets.
func FromProgram(p *x86_64.Program) []Section {
	text := Section{Kind: SectionText, Name: ".text", Align: 16}
	data := Section{Kind: SectionData, Name: ".data", Align: 1}
	bss := Section{Kind: SectionBSS, Name: ".bss", Align: 1}
	tdata := Section{Kind: SectionTLSData, Name: ".tdata", Align: 1}
	tbss := Section{Kind: SectionTLSBSS, Name: ".tbss", Align: 1}

	for _, f := range p.Funcs {
		off := alignUp(uint32(len(text.Code)), f.Align)
		for uint32(len(text.Code)) < off {
			text.Code = append(text.Code, 0x90) // NOP padding between functions
		}
		text.Symbols = append(text.Symbols, Symbol{
			Name: f.Name, Offset: off, Size: uint32(len(f.Code)), Export: f.Export,
		})
		for _, fx := range f.Fixups {
			text.Relocs = append(text.Relocs, Reloc{
				Offset: off + fx.Offset, Symbol: fx.Symbol,
				Kind: relocKind(fx.Kind), Addend: fx.Addend,
			})
		}
		text.Code = append(text.Code, f.Code...)
		if f.Align > text.Align {
			text.Align = f.Align
		}
	}
	text.Size = uint32(len(text.Code))

	place := func(sec *Section, g x86_64.Global, withData bool) {
		off := alignUp(sec.Size, g.Align)
		if withData {
			for uint32(len(sec.Code)) < off {
				sec.Code = append(sec.Code, 0)
			}
			sec.Code = append(sec.Code, g.Data...)
			for uint32(len(sec.Code)) < off+g.Size {
				sec.Code = append(sec.Code, 0) // zero tail beyond len(Data)
			}
		}
		sec.Symbols = append(sec.Symbols, Symbol{
			Name: g.Name, Offset: off, Size: g.Size, Export: g.Export,
		})
		for _, fx := range g.Fixups {
			sec.Relocs = append(sec.Relocs, Reloc{
				Offset: off + fx.Offset, Symbol: fx.Symbol,
				Kind: relocKind(fx.Kind), Addend: fx.Addend,
			})
		}
		sec.Size = off + g.Size
		if g.Align > sec.Align {
			sec.Align = g.Align
		}
	}

	for _, g := range p.Globals {
		switch {
		case g.TLS && g.Data == nil:
			place(&tbss, g, false)
		case g.TLS:
			place(&tdata, g, true)
		case g.Data == nil:
			place(&bss, g, false)
		default:
			place(&data, g, true)
		}
	}

	out := []Section{}
	for _, s := range []Section{text, data, bss, tdata, tbss} {
		if s.Size > 0 || len(s.Symbols) > 0 {
			out = append(out, s)
		}
	}
	return out
}