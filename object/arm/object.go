// Package object translates a lowered arm.Program into a generic,
// container-agnostic description of sections, symbols, and relocations —
// arrow 4 of the README taxonomy, for 32-bit ARM in either byte order.
//
// The only arch-specific knowledge this package adds is the
// arm.FixupKind -> RelocKind mapping, which for AArch32 ELF is the
// R_ARM_CALL / R_ARM_JUMP24 / R_ARM_MOVW_ABS_NC / R_ARM_MOVT_ABS /
// R_ARM_ABS32 shape. AAELF32 relocation codes are identical for arm and
// armeb; the byte order of the patched containers follows Program.Arch,
// which `link` must honor when applying them (and where the BE-8 .text
// word swap + EF_ARM_BE8 + $a/$d mapping symbols live for armeb — see
// lower/arm/arch.go). No objectfile import; the types are this package's own.
package object

import arm "github.com/vertex-language/vvm/lower/arm"

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
	// RelocCall24: BL — ((S + A - P) >> 2) into imm24 (R_ARM_CALL shape).
	RelocCall24 RelocKind = iota
	// RelocJump24: B — same arithmetic (R_ARM_JUMP24 shape).
	RelocJump24
	// RelocMovwAbs: (S + A) & 0xFFFF into the split imm16 (R_ARM_MOVW_ABS_NC).
	RelocMovwAbs
	// RelocMovtAbs: ((S + A) >> 16) & 0xFFFF into the split imm16 (R_ARM_MOVT_ABS).
	RelocMovtAbs
	// RelocAbs32: field := S + A (R_ARM_ABS32 shape; data words).
	RelocAbs32
)

func (k RelocKind) String() string {
	switch k {
	case RelocCall24:
		return "call24"
	case RelocJump24:
		return "jump24"
	case RelocMovwAbs:
		return "movw_abs"
	case RelocMovtAbs:
		return "movt_abs"
	case RelocAbs32:
		return "abs32"
	}
	return "reloc?"
}

type Reloc struct {
	Offset uint32
	Symbol string
	Kind   RelocKind
	Addend int64
}

func relocKind(k arm.FixupKind) RelocKind {
	switch k {
	case arm.FixupCall24:
		return RelocCall24
	case arm.FixupJump24:
		return RelocJump24
	case arm.FixupMovwAbs:
		return RelocMovwAbs
	case arm.FixupMovtAbs:
		return RelocMovtAbs
	}
	return RelocAbs32
}

func alignUp(n, a uint32) uint32 {
	if a == 0 {
		a = 1
	}
	return (n + a - 1) &^ (a - 1)
}

// FromProgram lays the program out into sections. Function code is
// concatenated into one text section (per-function alignment respected,
// with A32 NOP padding serialized in the program's byte order); initialized
// globals go to data, zero globals to bss, with tdata/tbss for TLS. Fixup
// offsets are rebased to section offsets and mapped to RelocKinds.
func FromProgram(p *arm.Program) []Section {
	// A32 NOP (mov r0, r0) = 0xE1A00000, serialized per Program.Arch —
	// the single place this package writes instruction bytes.
	nop := []byte{0x00, 0x00, 0xA0, 0xE1}
	if p.Arch.Big() {
		nop = []byte{0xE1, 0xA0, 0x00, 0x00}
	}

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

	place := func(sec *Section, g arm.Global, withData bool) {
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