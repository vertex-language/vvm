// objectwriter/aarch64/elf.go
//
// Bridges object/aarch64 to objectfile/elf (elf.ArchARM64).
//
// Known gap: object.RelocMovzG3/MovkG2/MovkG1/MovkG0 (the four-instruction
// MOVZ/MOVK absolute-address sequence) have no target here — elf's
// RelocKind set has no R_AARCH64_MOVW_UABS_* entries at all, only
// ADRPage21/AddOff12 (the page+offset form) and Abs64 (data). If
// lower/aarch64's isel ever emits MOVZ/MOVK for code addresses rather than
// ADRP+ADD, this fails loudly rather than silently mis-relocating.
//
// Approximation taken knowingly: object.RelocJump26 (B) is mapped onto the
// same elf.RelocPCRel26 as RelocCall26 (BL), i.e. R_AARCH64_CALL26 in both
// cases. The bit arithmetic is identical (S+A-P, >>2, into imm26); the two
// ELF relocation numbers only diverge in PLT-stub-insertion semantics for
// undefined externals, which doesn't affect locally-resolved branches. Flag
// this if external-symbol B (not BL) targets ever show up in practice.
package aarch64

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/aarch64"
	"github.com/vertex-language/vvm/objectfile/elf"
)

func ToELF(secs []object.Section, target elf.Target) ([]byte, error) {
	f := elf.NewFile(target)
	for _, s := range secs {
		es, err := convertSectionELF(s)
		if err != nil {
			return nil, err
		}
		f.AddSection(es)
	}
	return f.Serialize()
}

func convertSectionELF(s object.Section) (elf.Section, error) {
	kind, err := sectionKindELF(s.Kind)
	if err != nil {
		return elf.Section{}, fmt.Errorf("objectwriter/aarch64: section %q: %w", s.Name, err)
	}

	es := elf.Section{Kind: kind, Align: s.Align}
	if isBSSLike(s.Kind) {
		es.VSize = uint64(s.Size)
	} else {
		es.Code = s.Code
	}

	for _, sym := range s.Symbols {
		es.Symbols = append(es.Symbols, elf.Symbol{
			Name: sym.Name, Offset: sym.Offset, Size: sym.Size,
			Binding: bindingELF(sym.Export), Kind: symKindELF(s.Kind),
		})
	}

	for _, r := range s.Relocs {
		rk, err := relocKindELF(r.Kind)
		if err != nil {
			return elf.Section{}, fmt.Errorf("objectwriter/aarch64: section %q reloc to %q: %w",
				s.Name, r.Symbol, err)
		}
		es.Relocs = append(es.Relocs, elf.Reloc{
			Offset: r.Offset, Symbol: r.Symbol, Kind: rk, Addend: r.Addend,
		})
	}
	return es, nil
}

func isBSSLike(k object.SectionKind) bool {
	return k == object.SectionBSS || k == object.SectionTLSBSS
}

func sectionKindELF(k object.SectionKind) (elf.SectionKind, error) {
	switch k {
	case object.SectionText:
		return elf.SectionText, nil
	case object.SectionData:
		return elf.SectionData, nil
	case object.SectionROData:
		return elf.SectionROData, nil
	case object.SectionBSS:
		return elf.SectionBSS, nil
	case object.SectionTLSData, object.SectionTLSBSS:
		return elf.SectionTLS, nil
	}
	return 0, fmt.Errorf("unhandled object.SectionKind %v", k)
}

func symKindELF(sk object.SectionKind) elf.SymbolKind {
	if sk == object.SectionText {
		return elf.SymFunc
	}
	return elf.SymData
}

func bindingELF(export bool) elf.Binding {
	if export {
		return elf.BindingGlobal
	}
	return elf.BindingLocal
}

func relocKindELF(k object.RelocKind) (elf.RelocKind, error) {
	switch k {
	case object.RelocCall26, object.RelocJump26:
		return elf.RelocPCRel26, nil
	case object.RelocAbs64:
		return elf.RelocAbs64, nil
	case object.RelocMovzG3, object.RelocMovkG2, object.RelocMovkG1, object.RelocMovkG0:
		return 0, fmt.Errorf(
			"elf/aarch64 has no R_AARCH64_MOVW_UABS_* relocation kind yet; " +
				"objectfile/elf needs those four added before MOVZ/MOVK sequences can be emitted")
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for elf/aarch64", k)
}