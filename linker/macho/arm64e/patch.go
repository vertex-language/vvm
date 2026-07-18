package arm64e

// SIMPLIFICATION: arm64e's userspace pointer-authentication ABI wants the
// GOT-load-and-branch sequence signed (typically PACIA on the loaded
// pointer, then BLRAA/BRAA to branch through it), keyed off a
// context-specific diversifier. Getting that encoding wrong is worse than
// not having it — a badly-signed branch either traps at runtime or, worse,
// silently authenticates against the wrong key. Rather than guess at the
// diversifier scheme, this file reuses arm64's plain (unsigned) ADRP+LDR+BR
// relocation math verbatim. The binary will carry the correct arm64e
// cputype/cpusubtype (so tooling identifies it correctly) but its PLT stubs
// are not PAC-signed. Do not treat output from this backend as a
// spec-correct arm64e binary until this is revisited.

import (
	"encoding/binary"
	"fmt"

	"github.com/vertex-language/vvm/linker/macho"
)

func applyARM64E(data []byte, off int, relType uint32, P, S uint64, A int64, stubs macho.StubMap) error {
	rtype := relType & 0xF

	switch rtype {
	case macho.ARM64_RELOC_UNSIGNED:
		val := uint64(int64(S) + A)
		return put64(data, off, val)

	case macho.ARM64_RELOC_BRANCH26:
		delta := int64(S) + A - int64(P)
		if delta%4 != 0 {
			return fmt.Errorf("ARM64_RELOC_BRANCH26: misaligned target delta %d at P=0x%x", delta, P)
		}
		imm26 := uint32(delta/4) & 0x3FFFFFF
		instr := binary.LittleEndian.Uint32(data[off:])
		instr = (instr & 0xFC000000) | imm26
		binary.LittleEndian.PutUint32(data[off:], instr)
		return nil

	case macho.ARM64_RELOC_PAGE21:
		instr := binary.LittleEndian.Uint32(data[off:])
		rd := instr & 0x1F
		target := uint64(int64(S) + A)
		newInstr := encodeADRP(rd, P, target)
		binary.LittleEndian.PutUint32(data[off:], newInstr)
		return nil

	case macho.ARM64_RELOC_PAGEOFF12:
		instr := binary.LittleEndian.Uint32(data[off:])
		target := uint64(int64(S)+A) & 0xFFF
		newInstr, err := patchPageOff12(instr, target)
		if err != nil {
			return fmt.Errorf("ARM64_RELOC_PAGEOFF12 at 0x%x: %w", P, err)
		}
		binary.LittleEndian.PutUint32(data[off:], newInstr)
		return nil

	case macho.ARM64_RELOC_GOT_LOAD_PAGE21:
		gotVA, ok := stubs[S]
		if !ok {
			gotVA = S
		}
		instr := binary.LittleEndian.Uint32(data[off:])
		rd := instr & 0x1F
		newInstr := encodeADRP(rd, P, gotVA)
		binary.LittleEndian.PutUint32(data[off:], newInstr)
		return nil

	case macho.ARM64_RELOC_GOT_LOAD_PAGEOFF12:
		gotVA, ok := stubs[S]
		if !ok {
			gotVA = S
		}
		instr := binary.LittleEndian.Uint32(data[off:])
		pageOff := gotVA & 0xFFF
		newInstr, err := patchPageOff12(instr, pageOff)
		if err != nil {
			return fmt.Errorf("ARM64_RELOC_GOT_LOAD_PAGEOFF12 at 0x%x: %w", P, err)
		}
		binary.LittleEndian.PutUint32(data[off:], newInstr)
		return nil

	case macho.ARM64_RELOC_POINTER_TO_GOT:
		gotVA, ok := stubs[S]
		if !ok {
			gotVA = S
		}
		val := int32(int64(gotVA) + A - int64(P))
		return put32(data, off, uint32(val))

	case macho.ARM64_RELOC_SUBTRACTOR:
		return nil

	default:
		return fmt.Errorf("unsupported ARM64E Mach-O reloc type %d at off 0x%x", rtype, off)
	}
}

func patchPageOff12(instr uint32, byteOffset uint64) (uint32, error) {
	top8 := instr >> 24

	switch {
	case top8 == 0x91:
		imm12 := uint32(byteOffset & 0xFFF)
		return (instr & 0xFFC003FF) | (imm12 << 10), nil

	case top8&0xBF == 0x39:
		scale := uint64(1)
		if top8&0x40 != 0 {
			scale = 2
		}
		imm12 := uint32((byteOffset / scale) & 0xFFF)
		return (instr & 0xFFC003FF) | (imm12 << 10), nil

	case top8 == 0xB9:
		imm12 := uint32((byteOffset >> 2) & 0xFFF)
		return (instr & 0xFFC003FF) | (imm12 << 10), nil

	case top8 == 0xF9:
		imm12 := uint32((byteOffset >> 3) & 0xFFF)
		return (instr & 0xFFC003FF) | (imm12 << 10), nil

	case top8 == 0x3D:
		imm12 := uint32((byteOffset >> 4) & 0xFFF)
		return (instr & 0xFFC003FF) | (imm12 << 10), nil

	default:
		size := (instr >> 30) & 0x3
		scale := uint64(1) << size
		imm12 := uint32((byteOffset / scale) & 0xFFF)
		return (instr & 0xFFC003FF) | (imm12 << 10), nil
	}
}

func encodeADRP(rd uint32, PC, target uint64) uint32 {
	imm21 := int64(target>>12) - int64(PC>>12)
	immlo := uint32(imm21) & 0x3
	immhi := uint32(imm21>>2) & 0x7FFFF
	return 0x90000000 | (immlo << 29) | (immhi << 5) | (rd & 0x1F)
}

func encodeLDR64UnsignedOffset(rt, rn, byteOffset uint32) uint32 {
	imm12 := (byteOffset >> 3) & 0xFFF
	return 0xF9400000 | (imm12 << 10) | ((rn & 0x1F) << 5) | (rt & 0x1F)
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