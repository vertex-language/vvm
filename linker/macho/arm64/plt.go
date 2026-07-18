package arm64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/macho"
)

const stubSize = 12 // ADRP + LDR + BR

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
		writeStub(pltData[off:], stubVA, gotVA)
	}
	return stubs
}

// writeStub emits:
//
//	ADRP  X16, page(gotVA)
//	LDR   X16, [X16, #gotVA&0xFFF]
//	BR    X16
func writeStub(buf []byte, stubVA, gotVA uint64) {
	const x16 = 16
	binary.LittleEndian.PutUint32(buf[0:], encodeADRP(x16, stubVA, gotVA))
	binary.LittleEndian.PutUint32(buf[4:], encodeLDR64UnsignedOffset(x16, x16, uint32(gotVA&0xFFF)))
	binary.LittleEndian.PutUint32(buf[8:], 0xD61F0200) // BR X16
}