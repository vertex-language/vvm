package x86_64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/macho"
)

const stubSize = 6 // jmpq *[rip+offset]

type pltPatcher struct{}

func (pltPatcher) StubSize() int { return stubSize }

func (pltPatcher) PatchPLT(pltData, gotData, _ []byte, pltBase, gotBase uint64, syms []macho.PLTEntry) macho.StubMap {
	stubs := make(macho.StubMap, len(syms))
	for _, sym := range syms {
		i := sym.Idx
		stubVA := pltBase + uint64(i)*stubSize
		gotVA := gotBase + uint64(i)*8
		stubs[stubVA] = gotVA

		off := i * stubSize
		pltData[off] = 0xFF
		pltData[off+1] = 0x25
		rel := int32(int64(gotVA) - int64(stubVA+6))
		binary.LittleEndian.PutUint32(pltData[off+2:], uint32(rel))
	}
	return stubs
}