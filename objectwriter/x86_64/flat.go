// objectwriter/x86_64/flat.go
package x86_64

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/x86_64"
	"github.com/vertex-language/vvm/objectfile/flat"
)

func ToFlat(secs []object.Section, base uint64) ([]byte, error) {
	f := flat.NewFile()
	f.SetBaseAddress(base)
	for _, s := range secs {
		if len(s.Relocs) > 0 {
			return nil, fmt.Errorf(
				"objectwriter/x86_64: section %q: flat output cannot carry relocations (%d found); resolve them first or target elf/coff/macho instead",
				s.Name, len(s.Relocs))
		}
		fs := flat.Section{Align: s.Align, Kind: flatSectionKind(s.Kind)}
		if isBSSLike(s.Kind) {
			fs.VSize = uint64(s.Size)
		} else {
			fs.Code = s.Code
		}
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