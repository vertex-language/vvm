package arm64ec

import (
	"encoding/binary"
	"fmt"

	"github.com/vertex-language/vvm/linker/pe"
)

// arm64ecPatcher applies the same IMAGE_REL_ARM64_* relocation encodings as
// the aarch64 package. Final-image Machine is AMD64 (see Arch.machine() in
// target.go) but object-level relocations against ARM64EC-native code use
// the ARM64 numeric reloc types, not AMD64 ones.
//
// NOT implemented here (see README "Known limitations"):
//   - x64 shadow-space call-boundary adjustments
//   - CHPE metadata (IMAGE_DIRECTORY_ENTRY_LOAD_CONFIG range table)
// Output links end-to-end but tools/loader will see a plain x64 image.
type arm64ecPatcher struct {
	coreBase uint64
	addr64s  []pe.BaseRelocSite
}

func (p *arm64ecPatcher) SetCoreBase(v uint64)              { p.coreBase = v }
func (p *arm64ecPatcher) BaseRelocSites() []pe.BaseRelocSite { return p.addr64s }

func (p *arm64ecPatcher) Apply(data []byte, off int, relType uint32, P, S uint64, A int64) error {
	switch relType {
	case pe.RelARM64Absolute:
		return nil
	case pe.RelARM64Addr32:
		if off+4 > len(data) {
			return fmt.Errorf("ADDR32 write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint32(data[off:], uint32(int32(int64(S)+A)))
	case pe.RelARM64Addr32NB:
		if off+4 > len(data) {
			return fmt.Errorf("ADDR32NB write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint32(data[off:], uint32(int32(int64(S)+A-int64(p.coreBase))))
	case pe.RelARM64Addr64:
		if off+8 > len(data) {
			return fmt.Errorf("ADDR64 write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint64(data[off:], uint64(int64(S)+A))
		p.addr64s = append(p.addr64s, pe.BaseRelocSite{VA: P})
	case pe.RelARM64Branch26:
		return patchBranch26(data, off, int64(S)+A-int64(P))
	case pe.RelARM64Branch19:
		return patchBranch19(data, off, int64(S)+A-int64(P))
	case pe.RelARM64Branch14:
		return patchBranch14(data, off, int64(S)+A-int64(P))
	case pe.RelARM64PagebaseRel21:
		target := uint64(int64(S) + A)
		return patchADRP(data, off, (target&^0xFFF)-(P&^0xFFF))
	case pe.RelARM64Rel21:
		return patchADR(data, off, int64(S)+A-int64(P))
	case pe.RelARM64PageOffset12A, pe.RelARM64SecRelLow12A:
		return patchAddImm12(data, off, uint64(int64(S)+A)&0xFFF)
	case pe.RelARM64PageOffset12L, pe.RelARM64SecRelLow12L:
		return patchLdrStrImm12(data, off, uint64(int64(S)+A)&0xFFF)
	case pe.RelARM64SecRelHigh12A:
		return patchAddImm12(data, off, (uint64(int64(S)+A)>>12)&0xFFF)
	case pe.RelARM64Section:
		if off+2 > len(data) {
			return fmt.Errorf("SECTION write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint16(data[off:], 0)
	case pe.RelARM64SecRel:
		if off+4 > len(data) {
			return fmt.Errorf("SECREL write at %d out of bounds", off)
		}
		binary.LittleEndian.PutUint32(data[off:], uint32(S+uint64(A)))
	case pe.RelARM64Rel32:
		if off+4 > len(data) {
			return fmt.Errorf("REL32 write at %d out of bounds", off)
		}
		v := int64(S) + A - int64(P)
		if v < -0x80000000 || v > 0x7FFFFFFF {
			return fmt.Errorf("REL32 overflow: %d does not fit in int32", v)
		}
		binary.LittleEndian.PutUint32(data[off:], uint32(int32(v)))
	case pe.RelARM64Token:
		return nil
	default:
		return fmt.Errorf("unsupported ARM64EC COFF relocation type 0x%04X", relType)
	}
	return nil
}