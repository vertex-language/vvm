// objectwriter/aarch64/macho.go
//
// Bridges object/aarch64 to objectfile/macho (macho.TargetDarwinARM64).
// Same Call26/Jump26 approximation as elf.go — see there.
//
// ADRP/ADD-style absolute addressing (the case lower/aarch64 actually
// uses for every global reference, per object.go's own doc comment) is
// fully supported here via RelocADRPage21/RelocAddOff12. Mach-O gives
// every lo12 reference — ADD-immediate and every scaled LDR/STR width
// alike — a single ARM64_RELOC_PAGEOFF12 code, unlike AAELF64's separate
// R_AARCH64_LDSTxx_ABS_LO12_NC-per-width scheme: the linker reads the
// scale directly out of the instruction bits it's patching (see
// objectfile/macho/write.go's relocDesc table and its arm64PageReloc
// handling), so object.RelocAddAbsLo12Nc and all four
// object.RelocLdStNAbsLo12Nc kinds map onto the same macho.RelocAddOff12
// here.
//
// RelocCondBr19/RelocTstBr14 (B.cond/CBZ/CBNZ/TBZ/TBNZ) and
// RelocAdrPrelLo21 (bare ADR, not ADRP) are deliberately left unmapped —
// Mach-O's ARM64 relocation table (objectfile/macho/constants.go) has no
// codes for them at all. In practice this is fine: those fixups only
// ever target intra-function labels lower/aarch64 resolves directly, not
// external symbols requiring a real relocation entry — the same gap
// exists in elf.go, which doesn't map them either. If lower/aarch64 ever
// starts emitting one of these against an external symbol, it'll surface
// here as "unmapped reloc kind" rather than silently mis-patching, per
// this repo's fail-loudly stance.
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

	case object.RelocAdrPrelPgHi21:
		return macho.RelocADRPage21, nil // ADRP page-relative hi21

	case object.RelocAddAbsLo12Nc,
		object.RelocLdSt8AbsLo12Nc,
		object.RelocLdSt16AbsLo12Nc,
		object.RelocLdSt32AbsLo12Nc,
		object.RelocLdSt64AbsLo12Nc:
		return macho.RelocAddOff12, nil // see file doc comment: one PAGEOFF12 for every width

	case object.RelocAbs64:
		return macho.RelocAbs64, nil
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for macho/arm64", k)
}