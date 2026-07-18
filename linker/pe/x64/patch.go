package x64

import (
	"encoding/binary"
	"fmt"

	"github.com/vertex-language/vvm/linker/pe"
)

type amd64Patcher struct {
	coreBase uint64
	addr64s  []pe.BaseRelocSite
}

func (p *amd64Patcher) SetCoreBase(v uint64) { p.coreBase = v }

func (p *amd64Patcher) BaseRelocSites() []pe.BaseRelocSite { return p.addr64s }

func (p *amd64Patcher) Apply(data []byte, off int, relType uint32, P, S uint64, A int64) error {
	switch relType {

	case pe.RelAMD64Absolute:
		return nil

	case pe.RelAMD64Addr64:
		if off+8 > len(data) {
			return fmt.Errorf("ADDR64 write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint64(data[off:], uint64(int64(S)+A))
		p.addr64s = append(p.addr64s, pe.BaseRelocSite{VA: P})

	case pe.RelAMD64Addr32:
		if off+4 > len(data) {
			return fmt.Errorf("ADDR32 write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint32(data[off:], uint32(int32(int64(S)+A)))

	case pe.RelAMD64Addr32NB:
		if off+4 > len(data) {
			return fmt.Errorf("ADDR32NB write at %d out of bounds", off)
		}
		v := int64(S) + A - int64(p.coreBase)
		binary.LittleEndian.PutUint32(data[off:], uint32(int32(v)))

	case pe.RelAMD64Rel32:
		return amd64WriteRel32(data, off, int64(S)+A-int64(P)-4)
	case pe.RelAMD64Rel32_1:
		return amd64WriteRel32(data, off, int64(S)+A-int64(P)-5)
	case pe.RelAMD64Rel32_2:
		return amd64WriteRel32(data, off, int64(S)+A-int64(P)-6)
	case pe.RelAMD64Rel32_3:
		return amd64WriteRel32(data, off, int64(S)+A-int64(P)-7)
	case pe.RelAMD64Rel32_4:
		return amd64WriteRel32(data, off, int64(S)+A-int64(P)-8)
	case pe.RelAMD64Rel32_5:
		return amd64WriteRel32(data, off, int64(S)+A-int64(P)-9)

	case pe.RelAMD64Section:
		if off+2 > len(data) {
			return fmt.Errorf("SECTION write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint16(data[off:], 0)

	case pe.RelAMD64SecRel, pe.RelAMD64SecRel7:
		if off+4 > len(data) {
			return fmt.Errorf("SECREL write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint32(data[off:], uint32(S+uint64(A)))

	case pe.RelAMD64Token:
		return nil

	default:
		return fmt.Errorf("unsupported AMD64 COFF relocation type 0x%04X", relType)
	}
	return nil
}

func amd64WriteRel32(data []byte, off int, v int64) error {
	if off+4 > len(data) {
		return fmt.Errorf("REL32 write at %d out of bounds", off)
	}
	if v < -0x80000000 || v > 0x7FFFFFFF {
		return fmt.Errorf("REL32 overflow: %d does not fit in int32", v)
	}
	binary.LittleEndian.PutUint32(data[off:], uint32(int32(v)))
	return nil
}