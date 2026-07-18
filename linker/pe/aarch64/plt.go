package aarch64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/pe"
)

// Import thunk: ADRP x16, page(iat) ; LDR x16, [x16, #off] ; BR x16 ; NOP.
// 16 bytes total, matching dynamic.go's pltEntrySize.
const (
	pltHdrSize  = 16
	pltEntSz    = 16
	gotResSlots = 3

	insnADRPx16Base = uint32(0x90000010) // ADRP x16, #0
	insnLDRx16Base  = uint32(0xF9400210) // LDR x16, [x16, #0]  (64-bit, scale 8)
	insnBRx16       = uint32(0xD61F0200) // BR x16
	insnNOP         = uint32(0xD503201F)
)

type arm64PLTPatcher struct {
	iatLayout *pe.IATLayout
}

func (p *arm64PLTPatcher) SetIATLayout(l *pe.IATLayout) { p.iatLayout = l }

func (p *arm64PLTPatcher) PatchPLT(plt, gotPLT []byte, pltBase, gotBase uint64, syms []pe.PLTEntry) {
	if p.iatLayout == nil {
		return
	}
	for _, s := range syms {
		slot := p.iatLayout.SlotOf[s.Idx]
		iatVA := gotBase + uint64(gotResSlots+slot)*8
		thunkVA := pltBase + uint64(pltHdrSize+s.Idx*pltEntSz)
		tOff := pltHdrSize + s.Idx*pltEntSz

		pageDelta := (iatVA &^ 0xFFF) - (thunkVA &^ 0xFFF)
		imm := int64(pageDelta) >> 12
		u := uint32(imm) & 0x1FFFFF
		immlo := u & 0x3
		immhi := (u >> 2) & 0x7FFFF
		adrp := insnADRPx16Base | (immlo << 29) | (immhi << 5)
		binary.LittleEndian.PutUint32(plt[tOff:], adrp)

		imm12 := (iatVA & 0xFFF) >> 3 // 8-byte-scaled offset for 64-bit LDR
		ldr := insnLDRx16Base | (uint32(imm12) << 10)
		binary.LittleEndian.PutUint32(plt[tOff+4:], ldr)

		binary.LittleEndian.PutUint32(plt[tOff+8:], insnBRx16)
		binary.LittleEndian.PutUint32(plt[tOff+12:], insnNOP)

		s.Sym.VAddr = thunkVA
	}
}