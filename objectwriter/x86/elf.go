// objectwriter/x86/elf.go
//
// Bridges object/x86 (32-bit x86 generic sections) to objectfile/elf
// (elf.ArchX86, ELFCLASS32). This file is the only place that knows both
// object.RelocKind and elf.RelocKind exist; nothing else in the tree needs to.
package x86

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/x86"
	"github.com/vertex-language/vvm/objectfile/elf"
)

// ToELF serializes secs (as produced by object.FromProgram) into an ELF32
// relocatable object file for target. target.Arch must be elf.ArchX86 —
// this function doesn't check or correct that, the caller owns it.
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
		return elf.Section{}, fmt.Errorf("objectwriter/x86: section %q: %w", s.Name, err)
	}

	es := elf.Section{Kind: kind, Align: s.Align}
	if isBSSLike(s.Kind) {
		es.VSize = uint64(s.Size)
	} else {
		es.Code = s.Code
	}

	for _, sym := range s.Symbols {
		es.Symbols = append(es.Symbols, elf.Symbol{
			Name:    sym.Name,
			Offset:  sym.Offset,
			Size:    sym.Size,
			Binding: bindingELF(sym.Export),
			Kind:    symKindELF(s.Kind),
		})
	}

	for _, r := range s.Relocs {
		rk, err := relocKindELF(r.Kind)
		if err != nil {
			return elf.Section{}, fmt.Errorf("objectwriter/x86: section %q: %w", s.Name, err)
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
	case object.RelocPCRel32:
		return elf.RelocPCRel32, nil
	case object.RelocAbs32:
		return elf.RelocAbs32, nil
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for elf/i386", k)
}