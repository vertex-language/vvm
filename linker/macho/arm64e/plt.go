package arm64e

// SIMPLIFICATION: same caveat as patch.go — a spec-correct arm64e stub
// would be an authenticated jump (sign the loaded GOT pointer with PACIA
// under a well-defined diversifier, then BRAA/BLRAA through it). This emits
// the same unsigned ADRP+LDR+BR shape as arm64's stub. It is safe to run on
// arm64e hardware (PAC enforcement is opt-in per-binary and this binary
// doesn't claim to use it), but it is not taking advantage of — or
// correctly emulating — the arm64e ABI's actual security model.

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/macho"
)

const stubSize = 12 // ADRP + LDR + BR (unsigned — see file comment)

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

func writeStub(buf []byte, stubVA, gotVA uint64) {
	const x16 = 16
	binary.LittleEndian.PutUint32(buf[0:], encodeADRP(x16, stubVA, gotVA))
	binary.LittleEndian.PutUint32(buf[4:], encodeLDR64UnsignedOffset(x16, x16, uint32(gotVA&0xFFF)))
	binary.LittleEndian.PutUint32(buf[8:], 0xD61F0200) // BR X16 (would be BRAA X16, X17 if signed)
}