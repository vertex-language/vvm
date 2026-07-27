package x86_64

import "strconv"

// Reg is a physical x86-64 register. Values 0-15 are general-purpose
// registers (GPRs), 16-31 are vector/float registers (XMM0-XMM15), and
// RNone is the absent sentinel.
type Reg byte

const (
	RRAX  Reg = 0
	RRCX  Reg = 1
	RRDX  Reg = 2
	RRBX  Reg = 3
	RRSP  Reg = 4
	RRBP  Reg = 5
	RRSI  Reg = 6
	RRDI  Reg = 7
	RR8   Reg = 8
	RR9   Reg = 9
	RR10  Reg = 10
	RR11  Reg = 11
	RR12  Reg = 12
	RR13  Reg = 13
	RR14  Reg = 14
	RR15  Reg = 15

	RXMM0  Reg = 16
	RXMM1  Reg = 17
	RXMM2  Reg = 18
	RXMM3  Reg = 19
	RXMM4  Reg = 20
	RXMM5  Reg = 21
	RXMM6  Reg = 22
	RXMM7  Reg = 23
	RXMM8  Reg = 24
	RXMM9  Reg = 25
	RXMM10 Reg = 26
	RXMM11 Reg = 27
	RXMM12 Reg = 28
	RXMM13 Reg = 29
	RXMM14 Reg = 30
	RXMM15 Reg = 31

	RNone Reg = 0xFF
)

const NumGPR = 16

func (r Reg) IsGPR() bool { return r < NumGPR }
func (r Reg) IsXMM() bool { return r >= RXMM0 && r <= RXMM15 }

func (r Reg) NeedsREXBit() bool {
	if r.IsGPR() {
		return r >= RR8
	}
	if r.IsXMM() {
		return r >= RXMM8
	}
	return false
}

func (r Reg) Low3() byte {
	if r.IsXMM() {
		return byte(r - RXMM0) & 7
	}
	return byte(r) & 7
}

var reg64 = [NumGPR]string{
	"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi",
	"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
}
var reg32 = [NumGPR]string{
	"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi",
	"r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d",
}
var reg16 = [NumGPR]string{
	"ax", "cx", "dx", "bx", "sp", "bp", "si", "di",
	"r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w",
}

var reg8NoREX = [8]string{"al", "cl", "dl", "bl", "ah", "ch", "dh", "bh"}
var reg8REX = [16]string{
	"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil",
	"r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b",
}

func (r Reg) Name(widthBits int) string {
	if r.IsXMM() {
		return "xmm" + strconv.Itoa(int(r-RXMM0))
	}
	if !r.IsGPR() {
		return "?"
	}
	switch widthBits {
	case 16:
		return reg16[r]
	case 32:
		return reg32[r]
	}
	return reg64[r]
}

func (r Reg) NameByte(rex bool) string {
	if !r.IsGPR() {
		return "?"
	}
	if rex {
		return reg8REX[r]
	}
	if r >= RR8 {
		return "?"
	}
	return reg8NoREX[r]
}

func (r Reg) ByteAddressable(rex bool) bool {
	if !r.IsGPR() {
		return false
	}
	if rex {
		return true
	}
	return r < RR8
}

func (r Reg) String() string { return r.Name(64) }