// objectwriter/x86_64/elf.go
//
// Bridges object/x86_64 to objectfile/elf (elf.ArchAMD64, ELFCLASS64).
package x86_64

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/x86_64"
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
		return elf.Section{}, fmt.Errorf("objectwriter/x86_64: section %q: %w", s.Name, err)
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
			return elf.Section{}, fmt.Errorf("objectwriter/x86_64: section %q: %w", s.Name, err)
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

// relocKindELF maps object/x86_64's reloc vocabulary onto elf's AMD64
// relocation kinds.
//
// object.RelocPCRel32 now covers both call/jmp branch sites and
// RIP-relative data references (see object/x86_64/object.go — the
// encoder's FixupPCRel32 doesn't distinguish them). There used to be a
// separate object.RelocPLT32 for the branch case; it's gone, and this
// package can no longer tell the two apart either. We map to the generic
// PC-relative reloc rather than PLT32: R_X86_64_PLT32 signals "may need a
// PLT stub", which is meaningless (and wrong) for a data reference, while
// R_X86_64_PC32 is correct for data and still resolves correctly for calls
// that don't need PLT indirection.
func relocKindELF(k object.RelocKind) (elf.RelocKind, error) {
	switch k {
	case object.RelocPCRel32:
		return elf.RelocPCRel32, nil
	case object.RelocAbs64:
		return elf.RelocAbs64, nil
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for elf/amd64", k)
}