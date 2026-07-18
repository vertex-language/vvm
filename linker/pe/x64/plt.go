package x64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/pe"
)

type amd64PLTPatcher struct {
	iatLayout *pe.IATLayout
}

func (p *amd64PLTPatcher) SetIATLayout(l *pe.IATLayout) { p.iatLayout = l }

func (p *amd64PLTPatcher) PatchPLT(plt, gotPLT []byte, pltBase, gotBase uint64, syms []pe.PLTEntry) {
	if p.iatLayout == nil {
		return
	}
	const (
		pltHdrSize  = 16
		pltEntSz    = 16
		gotResSlots = 3
	)
	for _, s := range syms {
		slot := p.iatLayout.SlotOf[s.Idx]
		iatVA := gotBase + uint64(gotResSlots+slot)*8
		thunkVA := pltBase + uint64(pltHdrSize+s.Idx*pltEntSz)
		rel32 := int32(int64(iatVA) - int64(thunkVA+6))
		tOff := pltHdrSize + s.Idx*pltEntSz
		plt[tOff+0] = 0xFF
		plt[tOff+1] = 0x25
		binary.LittleEndian.PutUint32(plt[tOff+2:], uint32(rel32))
		for k := 6; k < pltEntSz; k++ {
			plt[tOff+k] = 0x90
		}
		s.Sym.VAddr = thunkVA
	}
}