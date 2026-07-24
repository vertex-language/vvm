package aarch64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/pe"
)

// Import thunk: ADRP x16, page(iat) ; LDR x16, [x16, #off] ; BR x16 ; NOP.
// 16 bytes total — pe.PLTEntrySize is the authority on that, not a local copy.
const (
	insnADRPx16Base = uint32(0x90000010) // ADRP x16, #0
	insnLDRx16Base  = uint32(0xF9400210) // LDR x16, [x16, #0]  (64-bit, scale 8)
	insnBRx16       = uint32(0xD61F0200) // BR x16
	insnNOP         = uint32(0xD503201F)
)

type arm64PLTPatcher struct {
	iatLayout *pe.IATLayout
}

func (p *arm64PLTPatcher) SetIATLayout(l *pe.IATLayout) { p.iatLayout = l }

// PatchPLT writes one ADRP/LDR/BR thunk per import and points each shared
// symbol's VAddr at its thunk.
//
// Slot geometry comes from the pe package rather than local constants: the IAT
// address computed here must match, byte for byte, the FirstThunk that
// fillImports writes into the import descriptor. When the two disagree the
// loader binds one set of slots while these thunks read another, and the
// process faults on its first call into an imported function.
func (p *arm64PLTPatcher) PatchPLT(plt, gotPLT []byte, pltBase, gotBase uint64, syms []pe.PLTEntry) {
	if p.iatLayout == nil {
		return
	}
	for _, s := range syms {
		slot := p.iatLayout.SlotOf[s.Idx]
		iatVA := gotBase + uint64(pe.GOTReserved+slot)*pe.GOTEntrySize
		thunkVA := pltBase + uint64(pe.PLTHeaderSize+s.Idx*pe.PLTEntrySize)
		tOff := pe.PLTHeaderSize + s.Idx*pe.PLTEntrySize

		pageDelta := (iatVA &^ 0xFFF) - (thunkVA &^ 0xFFF)
		imm := int64(pageDelta) >> 12
		u := uint32(imm) & 0x1FFFFF
		immlo := u & 0x3
		immhi := (u >> 2) & 0x7FFFF
		adrp := insnADRPx16Base | (immlo << 29) | (immhi << 5)
		binary.LittleEndian.PutUint32(plt[tOff:], adrp)

		// The >>3 is the 64-bit LDR's own 8-byte immediate scale, an A64
		// encoding fact — it happens to equal log2(pe.GOTEntrySize), but it
		// isn't derived from it, so it stays a literal.
		imm12 := (iatVA & 0xFFF) >> 3
		ldr := insnLDRx16Base | (uint32(imm12) << 10)
		binary.LittleEndian.PutUint32(plt[tOff+4:], ldr)

		binary.LittleEndian.PutUint32(plt[tOff+8:], insnBRx16)
		binary.LittleEndian.PutUint32(plt[tOff+12:], insnNOP)

		s.Sym.VAddr = thunkVA
	}
}