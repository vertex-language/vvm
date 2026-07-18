package arm64_32

// SIMPLIFICATION: GOT slots here are still 8 bytes (gotEntrySize is a
// package-level constant in the core macho package's dynamic.go, not yet
// parameterized per-arch). A correct arm64_32 backend would use 4-byte GOT
// slots and a 32-bit-offset LDR (see encodeLDR32UnsignedOffset above), and
// would need dynamic.go's injectMachoPLT/patchPLT to ask the PLTPatcher for
// GOT entry width too, not just stub width. Until that plumbing exists,
// this writes an 8-byte-load stub against an 8-byte GOT slot — functionally
// consistent with the rest of this codebase's assumptions, but not what a
// real watchOS binary looks like on disk.

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
		gotVA := gotBase + uint64(i)*8 // see file comment: should be *4
		stubs[stubVA] = gotVA

		off := i * stubSize
		writeStub(pltData[off:], stubVA, gotVA)
	}
	return stubs
}

func writeStub(buf []byte, stubVA, gotVA uint64) {
	const x16 = 16
	binary.LittleEndian.PutUint32(buf[0:], encodeADRP(x16, stubVA, gotVA))
	// Using the 64-bit load here (not encodeLDR32UnsignedOffset) to match
	// the 8-byte GOT slots dynamic.go actually allocates today.
	binary.LittleEndian.PutUint32(buf[4:], encodeLDR64UnsignedOffset(x16, x16, uint32(gotVA&0xFFF)))
	binary.LittleEndian.PutUint32(buf[8:], 0xD61F0200) // BR X16
}

// encodeLDR64UnsignedOffset duplicated locally (not imported from arm64 —
// subpackages are intentionally self-contained, see arm64e's equivalent).
func encodeLDR64UnsignedOffset(rt, rn, byteOffset uint32) uint32 {
	imm12 := (byteOffset >> 3) & 0xFFF
	return 0xF9400000 | (imm12 << 10) | ((rn & 0x1F) << 5) | (rt & 0x1F)
}