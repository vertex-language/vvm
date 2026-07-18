// objectwriter/x86/flat.go
//
// Bridges object/x86 to objectfile/flat. Flat binary forbids relocations by
// construction (flat.Section has no Relocs field), so a section carrying any
// is rejected here rather than silently dropped.
package x86

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/x86"
	"github.com/vertex-language/vvm/objectfile/flat"
)

// ToFlat concatenates secs into a raw binary image at the given base
// address. Every section in secs must have zero Relocs; if you need
// unresolved references, target ToELF instead.
func ToFlat(secs []object.Section, base uint64) ([]byte, error) {
	f := flat.NewFile()
	f.SetBaseAddress(base)
	for _, s := range secs {
		if len(s.Relocs) > 0 {
			return nil, fmt.Errorf(
				"objectwriter/x86: section %q: flat output cannot carry relocations (%d found); resolve them first or target elf instead",
				s.Name, len(s.Relocs))
		}
		fs := flat.Section{Align: s.Align, Kind: flatSectionKind(s.Kind)}
		if isBSSLike(s.Kind) {
			fs.VSize = uint64(s.Size)
		} else {
			fs.Code = s.Code
		}
		// flat.Section.Symbols exists only for call-site symmetry — the
		// flat encoder never reads it, so we don't bother populating it.
		f.AddSection(fs)
	}
	return f.Serialize()
}

func flatSectionKind(k object.SectionKind) flat.SectionKind {
	switch k {
	case object.SectionText:
		return flat.SectionText
	case object.SectionData:
		return flat.SectionData
	case object.SectionROData:
		return flat.SectionROData
	case object.SectionBSS:
		return flat.SectionBSS
	case object.SectionTLSData, object.SectionTLSBSS:
		return flat.SectionTLS
	}
	return flat.SectionData
}