package x64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/pe"
)

type amd64ImportPatcher struct {
	iatLayout *pe.IATLayout
}

func (p *amd64ImportPatcher) SetIATLayout(l *pe.IATLayout) { p.iatLayout = l }

// PatchImportThunks writes one `jmp qword ptr [rip+disp32]` thunk per
// import, NOP-padded to pe.ThunkEntrySize, and points each symbol's VAddr
// at its thunk. No reserved-slot offset is added on either side (PE's IAT
// needs none — see pe/importthunks.go), so thunk and IAT addresses are
// computed directly from each entry's index/slot.
func (p *amd64ImportPatcher) PatchImportThunks(thunk, iat []byte, thunkBase, iatBase uint64, syms []pe.ImportThunkEntry) {
	if p.iatLayout == nil {
		return
	}
	for _, s := range syms {
		slot := p.iatLayout.SlotOf[s.Idx]
		iatVA := iatBase + uint64(slot)*pe.IATEntrySize
		thunkVA := thunkBase + uint64(s.Idx*pe.ThunkEntrySize)
		rel32 := int32(int64(iatVA) - int64(thunkVA+6))
		tOff := s.Idx * pe.ThunkEntrySize
		thunk[tOff+0] = 0xFF
		thunk[tOff+1] = 0x25
		binary.LittleEndian.PutUint32(thunk[tOff+2:], uint32(rel32))
		for k := 6; k < pe.ThunkEntrySize; k++ {
			thunk[tOff+k] = 0x90
		}
		s.Sym.VAddr = thunkVA
	}
}