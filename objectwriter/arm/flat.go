// objectwriter/arm/flat.go
//
// Bridges object/arm (32-bit ARM, either byte order) to objectfile/flat.
//
// There is deliberately no arm/elf.go in this package yet. Two gaps in
// objectfile/elf block it:
//   1. elf.Arch has no ArchARM entry (only AMD64/ARM64/RISCV64/X86) —
//      there's no e_machine value to select for AArch32.
//   2. elf/write.go hardcodes little-endian output throughout, but
//      object/arm's Program.Arch carries an explicit big-endian variant
//      (armeb) that a real target needs honored.
// Until objectfile/elf grows both, an ARM32 Linux target can only reach
// flat here — which is a dead end for anything needing external symbol
// resolution, since flat forbids relocations outright.
package arm

import (
	"fmt"

	object "github.com/vertex-language/vvm/object/arm"
	"github.com/vertex-language/vvm/objectfile/flat"
)

func ToFlat(secs []object.Section, base uint64) ([]byte, error) {
	f := flat.NewFile()
	f.SetBaseAddress(base)
	for _, s := range secs {
		if len(s.Relocs) > 0 {
			return nil, fmt.Errorf(
				"objectwriter/arm: section %q: flat output cannot carry relocations (%d found); "+
					"ARM32 has no elf/coff/macho target in objectwriter yet — see package doc comment",
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

func isBSSLike(k object.SectionKind) bool {
	return k == object.SectionBSS || k == object.SectionTLSBSS
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