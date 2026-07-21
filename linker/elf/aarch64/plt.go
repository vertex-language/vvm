// plt.go — .plt / .got.plt / .rela.plt synthesis for AArch64.
// PLT0 is 32 bytes here (vs 16 for x86_64) — HeaderSize() must reflect that
// or InjectPLTSections will undersize .plt and every stub after PLT0 will
// land 16 bytes short of where PatchPLT writes it.
package aarch64

import (

	"github.com/vertex-language/vvm/linker/elf"
)

const (
	pltHeaderSize = 32
	pltEntrySize  = 16
	gotEntrySize  = 8
	relaEntrySize = 24
)

type pltPatcher struct{ bigEndian bool }

func (p pltPatcher) HeaderSize() int { return pltHeaderSize }
func (p pltPatcher) EntrySize() int  { return pltEntrySize }

func (p pltPatcher) PatchPLT(plt, gotPLT, relaPLT []byte, pltBase, gotBase uint64, syms []elf.PLTEntry) {
	bo := byteOrder(p.bigEndian)

	plt0 := []byte{
		0xf0, 0x7b, 0xbf, 0xa9, // stp   x16, x30, [sp, #-16]!
		0x10, 0x00, 0x00, 0x90, // adrp  x16, .got.plt@PAGE
		0x11, 0x02, 0x40, 0xf9, // ldr   x17, [x16, #:lo12:.got.plt+8]
		0x10, 0x00, 0x00, 0x91, // add   x16, x16, #:lo12:.got.plt
		0x11, 0x06, 0x40, 0xf9, // ldr   x17, [x16, #16]
		0x20, 0x02, 0x1f, 0xd6, // br    x17
		0x1f, 0x20, 0x03, 0xd5, // nop
		0x1f, 0x20, 0x03, 0xd5, // nop
	}
	copy(plt, plt0)

	stub := []byte{
		0x10, 0x00, 0x00, 0x90, // adrp  x16, gotSlot@PAGE
		0x11, 0x02, 0x40, 0xf9, // ldr   x17, [x16, gotSlot@PAGEOFF]
		0x10, 0x00, 0x00, 0x91, // add   x16, x16, gotSlot@PAGEOFF
		0x20, 0x02, 0x1f, 0xd6, // br    x17
	}

	for _, e := range syms {
		i := e.Idx
		stubBase := pltBase + uint64(pltHeaderSize) + uint64(i)*pltEntrySize
		stubOff := pltHeaderSize + i*pltEntrySize
		gotSlotAddr := gotBase + uint64(3+i)*gotEntrySize
		gotSlotOff := (3 + i) * gotEntrySize

		copy(plt[stubOff:], stub)
		bo.PutUint64(gotPLT[gotSlotOff:], stubBase)

		ro := i * relaEntrySize
		bo.PutUint64(relaPLT[ro:], gotSlotAddr)
		bo.PutUint64(relaPLT[ro+8:], (uint64(i+1)<<32)|uint64(R_AARCH64_JUMP_SLOT))
		e.Sym.VAddr = stubBase
	}
}