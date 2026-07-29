package arm64ec

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/pe"
)

const (
	insnADRPx16Base = uint32(0x90000010)
	insnLDRx16Base  = uint32(0xF9400210)
	insnBRx16       = uint32(0xD61F0200)
	insnNOP         = uint32(0xD503201F)
)

// arm64ecImportPatcher writes the same ADRP/LDR/BR thunk shape as aarch64 —
// EC-native code calls through the IAT with real ARM64 instructions. No
// x64-compatible thunk variant is emitted (see README known limitations).
type arm64ecImportPatcher struct {
	iatLayout *pe.IATLayout
}

func (p *arm64ecImportPatcher) SetIATLayout(l *pe.IATLayout) { p.iatLayout = l }

// Slot geometry comes from the pe package rather than local constants, for
// the same reason as aarch64's: the IAT address computed here must match
// the FirstThunk fillImports writes, or the loader binds slots these
// thunks never read. No reserved-slot offset on either side — PE's IAT
// needs none (see pe/importthunks.go).
func (p *arm64ecImportPatcher) PatchImportThunks(thunk, iat []byte, thunkBase, iatBase uint64, syms []pe.ImportThunkEntry) {
	if p.iatLayout == nil {
		return
	}
	for _, s := range syms {
		slot := p.iatLayout.SlotOf[s.Idx]
		iatVA := iatBase + uint64(slot)*pe.IATEntrySize
		thunkVA := thunkBase + uint64(s.Idx*pe.ThunkEntrySize)
		tOff := s.Idx * pe.ThunkEntrySize

		pageDelta := (iatVA &^ 0xFFF) - (thunkVA &^ 0xFFF)
		imm := int64(pageDelta) >> 12
		u := uint32(imm) & 0x1FFFFF
		immlo := u & 0x3
		immhi := (u >> 2) & 0x7FFFF
		binary.LittleEndian.PutUint32(thunk[tOff:], insnADRPx16Base|(immlo<<29)|(immhi<<5))

		// 8-byte scale of the 64-bit LDR encoding — see aarch64/importthunks.go.
		imm12 := (iatVA & 0xFFF) >> 3
		binary.LittleEndian.PutUint32(thunk[tOff+4:], insnLDRx16Base|(uint32(imm12)<<10))

		binary.LittleEndian.PutUint32(thunk[tOff+8:], insnBRx16)
		binary.LittleEndian.PutUint32(thunk[tOff+12:], insnNOP)

		s.Sym.VAddr = thunkVA
	}
}