package aarch64

import (
	"encoding/binary"
	"fmt"

	"github.com/vertex-language/vvm/linker/pe"
)

type arm64Patcher struct {
	coreBase uint64
	addr64s  []pe.BaseRelocSite
}

func (p *arm64Patcher) SetCoreBase(v uint64)                { p.coreBase = v }
func (p *arm64Patcher) BaseRelocSites() []pe.BaseRelocSite   { return p.addr64s }

func (p *arm64Patcher) Apply(data []byte, off int, relType uint32, P, S uint64, A int64) error {
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
		v := int64(S) + A - int64(p.coreBase)
		binary.LittleEndian.PutUint32(data[off:], uint32(int32(v)))

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

	case pe.RelARM64PagebaseRel21: // ADRP
		target := uint64(int64(S) + A)
		return patchADRP(data, off, (target&^0xFFF)-(P&^0xFFF))

	case pe.RelARM64Rel21: // ADR
		return patchADR(data, off, int64(S)+A-int64(P))

	case pe.RelARM64PageOffset12A: // ADD Xd,Xn,#imm12
		return patchAddImm12(data, off, uint64(int64(S)+A)&0xFFF)

	case pe.RelARM64PageOffset12L: // LDR/STR unsigned-offset, scaled
		return patchLdrStrImm12(data, off, uint64(int64(S)+A)&0xFFF)

	case pe.RelARM64SecRelLow12A:
		return patchAddImm12(data, off, uint64(int64(S)+A)&0xFFF)

	case pe.RelARM64SecRelLow12L:
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
		return fmt.Errorf("unsupported ARM64 COFF relocation type 0x%04X", relType)
	}
	return nil
}

// ── A64 instruction encoders ─────────────────────────────────────────────────
// Each of these reads the existing instruction word (to preserve Rd/Rn/Rt and,
// for LDR/STR, to recover the access-size scale — the relocation itself
// carries no size information) and rewrites only the immediate field.

func readU32(data []byte, off int) (uint32, error) {
	if off < 0 || off+4 > len(data) {
		return 0, fmt.Errorf("read at %d out of bounds", off)
	}
	return binary.LittleEndian.Uint32(data[off:]), nil
}

func writeU32(data []byte, off int, v uint32) { binary.LittleEndian.PutUint32(data[off:], v) }

func patch21BitPCRel(data []byte, off int, delta int64) (uint32, error) {
	insn, err := readU32(data, off)
	if err != nil {
		return 0, err
	}
	if delta < -(1<<20) || delta >= (1<<20) {
		return 0, fmt.Errorf("21-bit immediate: delta %d out of range", delta)
	}
	u := uint32(delta) & 0x1FFFFF
	immlo := u & 0x3
	immhi := (u >> 2) & 0x7FFFF
	insn &^= (uint32(0x3) << 29) | (uint32(0x7FFFF) << 5)
	insn |= immlo << 29
	insn |= immhi << 5
	return insn, nil
}

func patchADRP(data []byte, off int, pageDelta uint64) error {
	insn, err := patch21BitPCRel(data, off, int64(pageDelta)>>12)
	if err != nil {
		return fmt.Errorf("ADRP: %w", err)
	}
	writeU32(data, off, insn)
	return nil
}

func patchADR(data []byte, off int, delta int64) error {
	insn, err := patch21BitPCRel(data, off, delta)
	if err != nil {
		return fmt.Errorf("ADR: %w", err)
	}
	writeU32(data, off, insn)
	return nil
}

func patchAddImm12(data []byte, off int, imm12 uint64) error {
	insn, err := readU32(data, off)
	if err != nil {
		return fmt.Errorf("ADD imm12: %w", err)
	}
	if imm12 > 0xFFF {
		return fmt.Errorf("ADD imm12: %d out of range", imm12)
	}
	insn &^= uint32(0xFFF) << 10
	insn |= uint32(imm12) << 10
	writeU32(data, off, insn)
	return nil
}

// patchLdrStrImm12 patches the 12-bit unsigned, size-scaled immediate on an
// LDR/STR (unsigned offset) instruction. The scale (1/2/4/8/16 bytes) is
// read from the instruction's own size/opc bits, not from the relocation.
func patchLdrStrImm12(data []byte, off int, byteOffset uint64) error {
	insn, err := readU32(data, off)
	if err != nil {
		return fmt.Errorf("LDR/STR imm12: %w", err)
	}
	size := (insn >> 30) & 0x3
	opc := (insn >> 22) & 0x3
	isVec := (insn>>26)&0x1 == 1
	scale := uint(size)
	if isVec && size == 0 && opc&0x2 != 0 {
		scale = 4 // 128-bit vector LDR/STR
	}
	if byteOffset%(1<<scale) != 0 {
		return fmt.Errorf("LDR/STR imm12: offset %d not aligned to scale %d", byteOffset, 1<<scale)
	}
	imm := byteOffset >> scale
	if imm > 0xFFF {
		return fmt.Errorf("LDR/STR imm12: %d out of range", imm)
	}
	insn &^= uint32(0xFFF) << 10
	insn |= uint32(imm) << 10
	writeU32(data, off, insn)
	return nil
}

func patchBranch26(data []byte, off int, delta int64) error {
	insn, err := readU32(data, off)
	if err != nil {
		return fmt.Errorf("B/BL imm26: %w", err)
	}
	if delta%4 != 0 {
		return fmt.Errorf("B/BL imm26: unaligned delta %d", delta)
	}
	imm := delta / 4
	if imm < -(1<<25) || imm >= (1<<25) {
		return fmt.Errorf("B/BL imm26: delta %d out of range", delta)
	}
	insn &^= uint32(0x3FFFFFF)
	insn |= uint32(imm) & 0x3FFFFFF
	writeU32(data, off, insn)
	return nil
}

func patchBranch19(data []byte, off int, delta int64) error {
	insn, err := readU32(data, off)
	if err != nil {
		return fmt.Errorf("B.cond/CBZ/CBNZ imm19: %w", err)
	}
	if delta%4 != 0 {
		return fmt.Errorf("B.cond/CBZ/CBNZ imm19: unaligned delta %d", delta)
	}
	imm := delta / 4
	if imm < -(1<<18) || imm >= (1<<18) {
		return fmt.Errorf("B.cond/CBZ/CBNZ imm19: delta %d out of range", delta)
	}
	insn &^= uint32(0x7FFFF) << 5
	insn |= (uint32(imm) & 0x7FFFF) << 5
	writeU32(data, off, insn)
	return nil
}

func patchBranch14(data []byte, off int, delta int64) error {
	insn, err := readU32(data, off)
	if err != nil {
		return fmt.Errorf("TBZ/TBNZ imm14: %w", err)
	}
	if delta%4 != 0 {
		return fmt.Errorf("TBZ/TBNZ imm14: unaligned delta %d", delta)
	}
	imm := delta / 4
	if imm < -(1<<13) || imm >= (1<<13) {
		return fmt.Errorf("TBZ/TBNZ imm14: delta %d out of range", delta)
	}
	insn &^= uint32(0x3FFF) << 5
	insn |= (uint32(imm) & 0x3FFF) << 5
	writeU32(data, off, insn)
	return nil
}