// Package object translates a lowered aarch64.Program into a generic,
// container-agnostic description of sections, symbols, and relocations —
// arrow 4 of the README taxonomy, for 64-bit ARM in either data byte order.
//
// The only arch-specific knowledge this package adds is the
// aarch64.FixupKind -> RelocKind mapping, which for AArch64 ELF is the
// R_AARCH64_CALL26 / R_AARCH64_JUMP26 / R_AARCH64_MOVW_UABS_G3 /
// R_AARCH64_MOVW_UABS_G2_NC / R_AARCH64_MOVW_UABS_G1_NC /
// R_AARCH64_MOVW_UABS_G0_NC / R_AARCH64_ABS64 shape (AAELF64: the checking
// form relocates the MOVZ, the _NC forms relocate MOVKs). AAELF64
// relocation codes are identical for aarch64 and aarch64_be, and — unlike
// AArch32 — the patched instruction containers are little-endian in both,
// because A64 instruction words are architecturally little-endian
// (lower/aarch64/arch.go). Only 64-bit *data* fields (RelocAbs64 sites in
// data sections) follow Program.Arch's byte order, which `link` must honor
// when applying them. There is no BE-8 text-swap step and no mapping-symbol
// requirement for code byte order. No objectfile import; the types are
// this package's own.
package object

import aarch64 "github.com/vertex-language/vvm/lower/aarch64"

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
	// RelocCall26: BL — ((S + A - P) >> 2) into imm26 (R_AARCH64_CALL26 shape).
	RelocCall26 RelocKind = iota
	// RelocJump26: B — same arithmetic (R_AARCH64_JUMP26 shape).
	RelocJump26
	// RelocMovzG3: ((S + A) >> 48) & 0xFFFF into imm16 (R_AARCH64_MOVW_UABS_G3).
	RelocMovzG3
	// RelocMovkG2: ((S + A) >> 32) & 0xFFFF into imm16 (R_AARCH64_MOVW_UABS_G2_NC).
	RelocMovkG2
	// RelocMovkG1: ((S + A) >> 16) & 0xFFFF into imm16 (R_AARCH64_MOVW_UABS_G1_NC).
	RelocMovkG1
	// RelocMovkG0: (S + A) & 0xFFFF into imm16 (R_AARCH64_MOVW_UABS_G0_NC).
	RelocMovkG0
	// RelocAbs64: 64-bit data field := S + A (R_AARCH64_ABS64 shape).
	RelocAbs64
)

func (k RelocKind) String() string {
	switch k {
	case RelocCall26:
		return "call26"
	case RelocJump26:
		return "jump26"
	case RelocMovzG3:
		return "movz_uabs_g3"
	case RelocMovkG2:
		return "movk_uabs_g2"
	case RelocMovkG1:
		return "movk_uabs_g1"
	case RelocMovkG0:
		return "movk_uabs_g0"
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

func relocKind(k aarch64.FixupKind) RelocKind {
	switch k {
	case aarch64.FixupCall26:
		return RelocCall26
	case aarch64.FixupJump26:
		return RelocJump26
	case aarch64.FixupMovzG3:
		return RelocMovzG3
	case aarch64.FixupMovkG2:
		return RelocMovkG2
	case aarch64.FixupMovkG1:
		return RelocMovkG1
	case aarch64.FixupMovkG0:
		return RelocMovkG0
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
// concatenated into one text section (per-function alignment respected,
// with A64 NOP padding — always little-endian, per arch.go); initialized
// globals go to data, zero globals to bss, with tdata/tbss for TLS. Fixup
// offsets are rebased to section offsets and mapped to RelocKinds.
func FromProgram(p *aarch64.Program) []Section {
	// A64 NOP = 0xD503201F, little-endian in both archs — this package
	// never consults p.Arch for instruction bytes.
	nop := []byte{0x1F, 0x20, 0x03, 0xD5}

	text := Section{Kind: SectionText, Name: ".text", Align: 4}
	data := Section{Kind: SectionData, Name: ".data", Align: 1}
	bss := Section{Kind: SectionBSS, Name: ".bss", Align: 1}
	tdata := Section{Kind: SectionTLSData, Name: ".tdata", Align: 1}
	tbss := Section{Kind: SectionTLSBSS, Name: ".tbss", Align: 1}

	for _, f := range p.Funcs {
		off := alignUp(uint32(len(text.Code)), f.Align)
		for uint32(len(text.Code)) < off {
			text.Code = append(text.Code, nop...)
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

	place := func(sec *Section, g aarch64.Global, withData bool) {
		off := alignUp(sec.Size, g.Align)
		if withData {
			for uint32(len(sec.Code)) < off {
				sec.Code = append(sec.Code, 0)
			}
			sec.Code = append(sec.Code, g.Data...)
			for uint32(len(sec.Code)) < off+g.Size {
				sec.Code = append(sec.Code, 0)
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