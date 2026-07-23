// objectwriter/x86_64/macho.go
//
// Bridges object/x86_64 to objectfile/macho (macho.TargetDarwinAMD64).
//
// Known gap: object.RelocPCRel32 now covers both call/jmp branch sites and
// plain RIP-relative *data* references (e.g. `lea rax, [rip+global]`) —
// the encoder's FixupPCRel32 doesn't tag which is which, and object/x86_64
// deliberately mirrors that (see object/x86_64/object.go) rather than
// inventing a distinction the encoder can't produce. macho's x86_64
// RelocKind set only really has a correct home for the branch case
// (X86_64_RELOC_BRANCH, via macho.RelocPCRel32); there's no
// X86_64_RELOC_SIGNED equivalent wired up for the generic data case. We
// still emit macho.RelocPCRel32 for the merged kind below, since branches
// are the overwhelmingly common instance and refusing them all would be a
// worse regression than mis-tagging the rarer data-reference case. Once
// objectfile/macho grows a generic PC-relative data relocation, this
// mapping should be revisited (and would need the fixup-kind distinction
// restored upstream in the encoder to be done correctly).
package x86_64

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/x86_64"
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
	kind, custom, err := sectionKindMachO(s.Kind)
	if err != nil {
		return macho.Section{}, fmt.Errorf("objectwriter/x86_64: section %q: %w", s.Name, err)
	}

	ms := macho.Section{Kind: kind, Custom: custom, Align: s.Align}
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
			return macho.Section{}, fmt.Errorf("objectwriter/x86_64: section %q reloc to %q: %w",
				s.Name, r.Symbol, err)
		}
		ms.Relocs = append(ms.Relocs, macho.Reloc{
			Offset: r.Offset, Symbol: r.Symbol, Kind: rk, Addend: r.Addend,
		})
	}
	return ms, nil
}

func sectionKindMachO(k object.SectionKind) (macho.SectionKind, string, error) {
	switch k {
	case object.SectionText:
		return macho.SectionText, "", nil
	case object.SectionData:
		return macho.SectionData, "", nil
	case object.SectionROData:
		return macho.SectionROData, "", nil
	case object.SectionBSS:
		return macho.SectionBSS, "", nil
	case object.SectionTLSData, object.SectionTLSBSS:
		return macho.SectionTLS, "", nil
	}
	return 0, "", fmt.Errorf("unhandled object.SectionKind %v", k)
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
	case object.RelocPCRel32:
		// See package-level comment: this merged kind covers both branch
		// sites (correct here) and RIP-relative data refs (not truly
		// representable in macho's current reloc set). We emit the branch
		// reloc rather than failing every call site.
		return macho.RelocPCRel32, nil
	case object.RelocAbs64:
		return macho.RelocAbs64, nil
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for macho/amd64", k)
}