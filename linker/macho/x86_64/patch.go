package x86_64

import (
	"encoding/binary"
	"fmt"

	"github.com/vertex-language/vvm/linker/macho"
)

func applyAMD64(data []byte, off int, relType uint32, P, S uint64, A int64, stubs macho.StubMap) error {
	rtype := relType & 0xF
	rlen := (relType >> 8) & 0x3
	pcrel := (relType >> 16) & 0x1

	switch rtype {
	case macho.X86_64_RELOC_UNSIGNED:
		val := uint64(int64(S) + A)
		switch rlen {
		case 2:
			return put32(data, off, uint32(val))
		case 3:
			return put64(data, off, val)
		}

	case macho.X86_64_RELOC_SIGNED, macho.X86_64_RELOC_SIGNED_1, macho.X86_64_RELOC_SIGNED_2, macho.X86_64_RELOC_SIGNED_4:
		if pcrel == 0 {
			return fmt.Errorf("SIGNED reloc at 0x%x but r_pcrel=0", P)
		}
		val := int32(int64(S) + A - int64(P))
		return put32(data, off, uint32(val))

	case macho.X86_64_RELOC_BRANCH:
		if pcrel == 0 {
			return fmt.Errorf("BRANCH reloc at 0x%x but r_pcrel=0", P)
		}
		val := int32(int64(S) + A - int64(P))
		return put32(data, off, uint32(val))

	case macho.X86_64_RELOC_GOT_LOAD, macho.X86_64_RELOC_GOT:
		gotVA, ok := stubs[S]
		if !ok {
			gotVA = S
		}
		if pcrel != 0 {
			val := int32(int64(gotVA) + A - int64(P))
			return put32(data, off, uint32(val))
		}
		return put64(data, off, gotVA)

	case macho.X86_64_RELOC_SUBTRACTOR:
		return nil

	default:
		return fmt.Errorf("unsupported AMD64 Mach-O reloc type %d at off 0x%x", rtype, off)
	}
	return fmt.Errorf("unhandled AMD64 reloc rtype=%d rlen=%d at off 0x%x", rtype, rlen, off)
}

func put32(data []byte, off int, v uint32) error {
	if off+4 > len(data) {
		return fmt.Errorf("reloc patch offset 0x%x out of bounds", off)
	}
	binary.LittleEndian.PutUint32(data[off:], v)
	return nil
}
func put64(data []byte, off int, v uint64) error {
	if off+8 > len(data) {
		return fmt.Errorf("reloc patch offset 0x%x out of bounds", off)
	}
	binary.LittleEndian.PutUint64(data[off:], v)
	return nil
}