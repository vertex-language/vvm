// Package object translates a lowered aarch64.Program into a generic,
// container-agnostic description of sections, symbols, and relocations —
// arrow 4 of the README taxonomy, for 64-bit ARM in either data byte order.
//
// The only arch-specific knowledge this package adds is the
// aarch64.FixupKind -> RelocKind mapping. lower/aarch64 reaches a global by
// the adrp + add :lo12: idiom (the position-independent form and the only
// one isa/aarch64/encoder names), never by a movz/movk absolute sequence,
// so the AAELF64 shapes here are R_AARCH64_CALL26 / R_AARCH64_JUMP26 /
// R_AARCH64_CONDBR19 / R_AARCH64_TSTBR14 / R_AARCH64_ADR_PREL_PG_HI21 /
// R_AARCH64_ADR_PREL_LO21 / R_AARCH64_ADD_ABS_LO12_NC /
// R_AARCH64_LDST{8,16,32,64}_ABS_LO12_NC / R_AARCH64_ABS64 (the last for a
// `global g ptr = addr f` data word, which has no instruction-field
// counterpart at all). AAELF64 relocation codes are identical for aarch64
// and aarch64_be, and — unlike AArch32 — the patched instruction containers
// are little-endian in both, because A64 instruction words are
// architecturally little-endian (lower/aarch64/arch.go). Only 64-bit *data*
// fields (RelocAbs64 sites in data sections) follow Program.Arch's byte
// order, which `link` must honor when applying them. There is no BE-8
// text-swap step and no mapping-symbol requirement for code byte order.
// No objectfile import; the types are this package's own.
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
	// RelocCondBr19: B.cond/CBZ/CBNZ — ((S + A - P) >> 2) into imm19
	// (R_AARCH64_CONDBR19 shape).
	RelocCondBr19
	// RelocTstBr14: TBZ/TBNZ — ((S + A - P) >> 2) into imm14
	// (R_AARCH64_TSTBR14 shape).
	RelocTstBr14
	// RelocAdrPrelPgHi21: ADRP — the page-relative delta of S+A into the
	// immhi/immlo split (R_AARCH64_ADR_PREL_PG_HI21 shape).
	RelocAdrPrelPgHi21
	// RelocAdrPrelLo21: ADR — (S + A - P) into the immhi/immlo split
	// (R_AARCH64_ADR_PREL_LO21 shape).
	RelocAdrPrelLo21
	// RelocAddAbsLo12Nc: ADD (immediate) — (S + A) & 0xFFF into imm12
	// (R_AARCH64_ADD_ABS_LO12_NC shape).
	RelocAddAbsLo12Nc
	// RelocLdSt8AbsLo12Nc: byte load/store — (S + A) & 0xFFF into imm12,
	// unscaled (R_AARCH64_LDST8_ABS_LO12_NC shape).
	RelocLdSt8AbsLo12Nc
	// RelocLdSt16AbsLo12Nc: halfword load/store — ((S + A) & 0xFFF) >> 1
	// into imm12 (R_AARCH64_LDST16_ABS_LO12_NC shape).
	RelocLdSt16AbsLo12Nc
	// RelocLdSt32AbsLo12Nc: word load/store — ((S + A) & 0xFFF) >> 2
	// into imm12 (R_AARCH64_LDST32_ABS_LO12_NC shape).
	RelocLdSt32AbsLo12Nc
	// RelocLdSt64AbsLo12Nc: doubleword load/store — ((S + A) & 0xFFF) >> 3
	// into imm12 (R_AARCH64_LDST64_ABS_LO12_NC shape).
	RelocLdSt64AbsLo12Nc
	// RelocAbs64: 64-bit data field := S + A (R_AARCH64_ABS64 shape). The
	// one kind with no instruction-field counterpart: a `global g ptr =
	// addr f` relocates a whole data word, not a bit-field inside an
	// instruction.
	RelocAbs64
)

func (k RelocKind) String() string {
	switch k {
	case RelocCall26:
		return "call26"
	case RelocJump26:
		return "jump26"
	case RelocCondBr19:
		return "condbr19"
	case RelocTstBr14:
		return "tstbr14"
	case RelocAdrPrelPgHi21:
		return "adr_prel_pg_hi21"
	case RelocAdrPrelLo21:
		return "adr_prel_lo21"
	case RelocAddAbsLo12Nc:
		return "add_abs_lo12_nc"
	case RelocLdSt8AbsLo12Nc:
		return "ldst8_abs_lo12_nc"
	case RelocLdSt16AbsLo12Nc:
		return "ldst16_abs_lo12_nc"
	case RelocLdSt32AbsLo12Nc:
		return "ldst32_abs_lo12_nc"
	case RelocLdSt64AbsLo12Nc:
		return "ldst64_abs_lo12_nc"
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

// relocKind translates lower/aarch64's FixupKind into this package's
// RelocKind. An explicit switch, not a numeric cast, even though the two
// enums are declared in the same order: a switch fails loudly if either
// side gains a case instead of silently misreinterpreting it, matching the
// convention aarch64.fromEncoderKind and encode.go's toEncoderOpr already
// use for the same reason.
func relocKind(k aarch64.FixupKind) RelocKind {
	switch k {
	case aarch64.FixupCall26:
		return RelocCall26
	case aarch64.FixupJump26:
		return RelocJump26
	case aarch64.FixupCondBr19:
		return RelocCondBr19
	case aarch64.FixupTestBr14:
		return RelocTstBr14
	case aarch64.FixupAdrPrelPgHi21:
		return RelocAdrPrelPgHi21
	case aarch64.FixupAdrPrelLo21:
		return RelocAdrPrelLo21
	case aarch64.FixupAddAbsLo12Nc:
		return RelocAddAbsLo12Nc
	case aarch64.FixupLdSt8AbsLo12Nc:
		return RelocLdSt8AbsLo12Nc
	case aarch64.FixupLdSt16AbsLo12Nc:
		return RelocLdSt16AbsLo12Nc
	case aarch64.FixupLdSt32AbsLo12Nc:
		return RelocLdSt32AbsLo12Nc
	case aarch64.FixupLdSt64AbsLo12Nc:
		return RelocLdSt64AbsLo12Nc
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