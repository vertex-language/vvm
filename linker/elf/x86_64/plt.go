// plt.go — .plt / .got.plt / .rela.plt synthesis for x86_64.
package x86_64

import (
	"encoding/binary"

	"github.com/vertex-language/vvm/linker/elf"
)

const (
	pltHeaderSize = 16
	pltEntrySize  = 16
	gotEntrySize  = 8
	relaEntrySize = 24
)

type pltPatcher struct{}

func (pltPatcher) HeaderSize() int { return pltHeaderSize }
func (pltPatcher) EntrySize() int  { return pltEntrySize }

func (pltPatcher) PatchPLT(plt, gotPLT, relaPLT []byte, pltBase, gotBase uint64, syms []elf.PLTEntry) {
	// PLT0: pushq *(.got.plt+8)(%rip); jmpq *(.got.plt+16)(%rip); nop
	plt[0], plt[1] = 0xff, 0x35
	putI32LE(plt[2:], ripRel32(gotBase+8, pltBase+6))
	plt[6], plt[7] = 0xff, 0x25
	putI32LE(plt[8:], ripRel32(gotBase+16, pltBase+12))
	plt[12], plt[13], plt[14], plt[15] = 0x0f, 0x1f, 0x40, 0x00

	for _, e := range syms {
		i := e.Idx
		stubBase := pltBase + uint64(pltHeaderSize) + uint64(i)*pltEntrySize
		stubOff := pltHeaderSize + i*pltEntrySize
		gotSlotAddr := gotBase + uint64(3+i)*gotEntrySize
		gotSlotOff := (3 + i) * gotEntrySize

		plt[stubOff+0], plt[stubOff+1] = 0xff, 0x25
		putI32LE(plt[stubOff+2:], ripRel32(gotSlotAddr, stubBase+6))
		plt[stubOff+6] = 0x68
		putI32LE(plt[stubOff+7:], int32(i))
		plt[stubOff+11] = 0xe9
		putI32LE(plt[stubOff+12:], ripRel32(pltBase, stubBase+16))

		binary.LittleEndian.PutUint64(gotPLT[gotSlotOff:], stubBase+6)

		ro := i * relaEntrySize
		binary.LittleEndian.PutUint64(relaPLT[ro:], gotSlotAddr)
		binary.LittleEndian.PutUint64(relaPLT[ro+8:], (uint64(i+1)<<32)|uint64(R_X86_64_JUMP_SLOT))

		e.Sym.VAddr = stubBase
	}
}

func ripRel32(target, ripAfter uint64) int32 { return int32(int64(target) - int64(ripAfter)) }
func putI32LE(b []byte, v int32)             { binary.LittleEndian.PutUint32(b, uint32(v)) }