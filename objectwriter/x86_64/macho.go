// objectwriter/x86_64/macho.go
//
// Bridges object/x86_64 to objectfile/macho (macho.TargetDarwinAMD64).
//
// Known gap: object.RelocPCRel32 (a plain RIP-relative *data* reference —
// e.g. `lea rax, [rip+global]`) has no target in macho's current x86_64
// RelocKind set. macho only exposes RelocPCRel32 for CALL/JMP branch sites
// (X86_64_RELOC_BRANCH) and RelocGOTLoad for GOT-indirected loads; there is
// no X86_64_RELOC_SIGNED equivalent wired up. Until objectfile/macho grows
// that case, any non-call PC-relative data reloc fails loudly below rather
// than being miscompiled into a branch-shaped relocation.
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
	case object.RelocPLT32:
		return macho.RelocPCRel32, nil // X86_64_RELOC_BRANCH — call/jmp sites only
	case object.RelocAbs64:
		return macho.RelocAbs64, nil
	case object.RelocPCRel32:
		return 0, fmt.Errorf(
			"macho/x86_64 has no generic RIP-relative data relocation (only branch and GOT-load); " +
				"objectfile/macho needs an X86_64_RELOC_SIGNED case added before this can be emitted")
	}
	return 0, fmt.Errorf("unmapped reloc kind %v for macho/amd64", k)
}