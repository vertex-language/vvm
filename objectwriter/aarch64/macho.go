// objectwriter/aarch64/macho.go
//
// Bridges object/aarch64 to objectfile/macho (macho.TargetDarwinARM64).
// Same MOVZ/MOVK gap and Call26/Jump26 approximation as elf.go — see there.
// Unlike x86_64/macho.go, ADRP/ADD-style absolute addressing (the case
// lower/aarch64 actually uses for globals) is fully supported here via
// RelocADRPage21/RelocAddOff12 — those two just aren't reachable from
// object.RelocKind's current set, which only has Call26/Jump26/Movz*/Abs64.
// If lower/aarch64 starts emitting ADRP+ADD instead of MOVZ/MOVK for
// absolute addresses, object/aarch64 needs those two RelocKinds added
// first — this file will pick them up trivially once it does.
package aarch64

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/aarch64"
	"github.com/vertex-language/vvm/objectfile/macho"
)

func ToMachO(secs []object.Section, target macho.Target) ([]byte, error) {
	f := macho.NewFile(target)
	for _, s := range secs {
		ms, err := convertSectionMachO(s)
		if err != nil {
			return nil, err
		}
		f.AddSection(ms)
	}
	return f.Serialize()
}

func convertSectionMachO(s object.Section) (macho.Section, error) {
	kind, err := sectionKindMachO(s.Kind)
	if err != nil {
		return macho.Section{}, fmt.Errorf("objectwriter/aarch64: section %q: %w", s.Name, err)
	}

	ms := macho.Section{Kind: kind, Align: s.Align}
	if isBSSLike(s.Kind) {
		ms.VSize = uint64(s.Size)
	} else {
		ms.Code = s.Code
	}

	for _, sym := range s.Symbols {
		ms.Symbols = append(ms.Symbols, macho.Symbol{
			Name: sym.Name, Offset: sym.Offset, Size: sym.Size,
			Binding: bindingMachO(sym.Export), Kind: symKindMachO(s.Kind),
		})
	}

	for _, r := range s.Relocs {
		rk, err := relocKindMachO(r.Kind)
		if err != nil {
			return macho.Section{}, fmt.Errorf("objectwriter/aarch64: section %q reloc to %q: %w",
				s.Name, r.Symbol, err)
		}
		ms.Relocs = append(ms.Relocs, macho.Reloc{
			Offset: r.Offset, Symbol: r.Symbol, Kind: rk, Addend: r.Addend,
		})
	}
	return ms, nil
}

func sectionKindMachO(k object.SectionKind) (macho.SectionKind, error) {
	switch k {
	case object.SectionText:
		return macho.SectionText, nil
	case object.SectionData:
		return macho.SectionData, nil
	case object.SectionROData:
		return macho.SectionROData, nil
	case object.SectionBSS:
		return macho.SectionBSS, nil
	case object.SectionTLSData, object.SectionTLSBSS:
		return macho.SectionTLS, nil
	}
	return 0, fmt.Errorf("unhandled object.SectionKind %v", k)
}

func symKindMachO(sk object.SectionKind) macho.SymbolKind {
	if sk == object.SectionText {
		return macho.SymFunc
	}
	return macho.SymData
}

func bindingMachO(export bool) macho.Binding {
	if export {
		return macho.BindingGlobal
	}
	return macho.BindingLocal
}

func relocKindMachO(k object.RelocKind) (macho.RelocKind, error) {
	switch k {
	case object.RelocCall26, object.RelocJump26:
		return macho.RelocPCRel26, nil // single BRANCH26 code covers both B and BL
	case object.RelocAbs64:
		return macho.RelocAbs64, nil
	case object.RelocMovzG3, object.RelocMovkG2, object.RelocMovkG1, object.RelocMovkG0:
		return 0, fmt.Errorf(
			"macho/arm64 has no MOVZ/MOVK-style relocation kind — use ADRP+ADD in lower/aarch64 " +
				"and object.RelocADRPage21/AddOff12 instead if code-address loads are needed")
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for macho/arm64", k)
}