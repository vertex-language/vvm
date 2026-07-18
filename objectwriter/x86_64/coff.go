// objectwriter/x86_64/coff.go
//
// Bridges object/x86_64 to objectfile/coff (coff.TargetWindowsAMD64).
package x86_64

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/x86_64"
	"github.com/vertex-language/vvm/objectfile/coff"
)

func ToCOFF(secs []object.Section, target coff.Target) ([]byte, error) {
	f := coff.NewFile(target)
	for _, s := range secs {
		cs, err := convertSectionCOFF(s)
		if err != nil {
			return nil, err
		}
		f.AddSection(cs)
	}
	return f.Serialize()
}

func convertSectionCOFF(s object.Section) (coff.Section, error) {
	kind, err := sectionKindCOFF(s.Kind)
	if err != nil {
		return coff.Section{}, fmt.Errorf("objectwriter/x86_64: section %q: %w", s.Name, err)
	}

	cs := coff.Section{Kind: kind, Align: s.Align}
	if isBSSLike(s.Kind) {
		cs.VSize = uint64(s.Size)
	} else {
		cs.Code = s.Code
	}

	for _, sym := range s.Symbols {
		cs.Symbols = append(cs.Symbols, coff.Symbol{
			Name: sym.Name, Offset: sym.Offset, Size: sym.Size,
			Binding: bindingCOFF(sym.Export), Kind: symKindCOFF(s.Kind),
			// DLLExport is a distinct Windows concept (an actual .dll export
			// table entry) from "externally linkable within this object" —
			// object.Symbol.Export only means the latter, so we never set
			// this from here. If DLL-export support is needed later, that's
			// a new field on object.Symbol, not an inference from Export.
			DLLExport: false,
		})
	}

	for _, r := range s.Relocs {
		rk, err := relocKindCOFF(r.Kind)
		if err != nil {
			return coff.Section{}, fmt.Errorf("objectwriter/x86_64: section %q: %w", s.Name, err)
		}
		cs.Relocs = append(cs.Relocs, coff.Reloc{
			Offset: r.Offset, Symbol: r.Symbol, Kind: rk, Addend: r.Addend,
		})
	}
	return cs, nil
}

func sectionKindCOFF(k object.SectionKind) (coff.SectionKind, error) {
	switch k {
	case object.SectionText:
		return coff.SectionText, nil
	case object.SectionData:
		return coff.SectionData, nil
	case object.SectionROData:
		return coff.SectionROData, nil
	case object.SectionBSS:
		return coff.SectionBSS, nil
	case object.SectionTLSData, object.SectionTLSBSS:
		return coff.SectionTLS, nil
	}
	return 0, fmt.Errorf("unhandled object.SectionKind %v", k)
}

func symKindCOFF(sk object.SectionKind) coff.SymbolKind {
	if sk == object.SectionText {
		return coff.SymFunc
	}
	return coff.SymData
}

func bindingCOFF(export bool) coff.Binding {
	if export {
		return coff.BindingGlobal
	}
	return coff.BindingLocal
}

func relocKindCOFF(k object.RelocKind) (coff.RelocKind, error) {
	switch k {
	case object.RelocPLT32:
		return coff.RelocPLT32, nil
	case object.RelocPCRel32:
		return coff.RelocPCRel32, nil
	case object.RelocAbs64:
		return coff.RelocAbs64, nil
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for coff/amd64", k)
}