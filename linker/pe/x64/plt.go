package x64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/pe"
)

type amd64PLTPatcher struct {
	iatLayout *pe.IATLayout
}

func (p *amd64PLTPatcher) SetIATLayout(l *pe.IATLayout) { p.iatLayout = l }

// PatchPLT writes one `jmp qword ptr [rip+disp32]` thunk per import, NOP-padded
// to pe.PLTEntrySize, and points each shared symbol's VAddr at its thunk.
//
// The slot geometry comes from the pe package rather than local constants: the
// IAT address computed here must match, byte for byte, the FirstThunk that
// fillImports writes into the import descriptor. When the two disagree the
// loader binds one set of slots and these thunks read another, and the process
// faults on its first call into an imported function.
func (p *amd64PLTPatcher) PatchPLT(plt, gotPLT []byte, pltBase, gotBase uint64, syms []pe.PLTEntry) {
	if p.iatLayout == nil {
		return
	}
	for _, s := range syms {
		slot := p.iatLayout.SlotOf[s.Idx]
		iatVA := gotBase + uint64(pe.GOTReserved+slot)*pe.GOTEntrySize
		thunkVA := pltBase + uint64(pe.PLTHeaderSize+s.Idx*pe.PLTEntrySize)
		// The displacement is relative to the end of the 6-byte FF 25 form.
		rel32 := int32(int64(iatVA) - int64(thunkVA+6))
		tOff := pe.PLTHeaderSize + s.Idx*pe.PLTEntrySize
		plt[tOff+0] = 0xFF
		plt[tOff+1] = 0x25
		binary.LittleEndian.PutUint32(plt[tOff+2:], uint32(rel32))
		for k := 6; k < pe.PLTEntrySize; k++ {
			plt[tOff+k] = 0x90
		}
		s.Sym.VAddr = thunkVA
	}
}