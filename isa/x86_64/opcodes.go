// The opcode<->mnemonic correspondence for every instruction form this
// compiler's encoder needs. Before this package existed, these tables
// (aluEnc, shiftExt, grp3Ext, grp5Ext, and the scattered fixed-opcode
// literals throughout encode.go's one() switch) lived unexported inside
// lower/x86_64/mcode, reachable only by the encoder itself.
package x86_64

// ALUOp is one two-operand ALU mnemonic's three opcode forms: MR (r/m,
// reg — reg is the source), RM (reg, r/m — reg is the destination), and
// the /ext digit used by the 80/81/83 (r/m, imm) group.
type ALUOp struct{ MR, RM, Ext byte }

// ALUOpcodes covers add/or/and/sub/xor/cmp — six mnemonics sharing one
// systematic pattern (0x01+8n MR, 0x03+8n RM, group /ext n).
var ALUOpcodes = map[string]ALUOp{
	"add": {0x01, 0x03, 0}, "or": {0x09, 0x0B, 1}, "and": {0x21, 0x23, 4},
	"sub": {0x29, 0x2B, 5}, "xor": {0x31, 0x33, 6}, "cmp": {0x39, 0x3B, 7},
}

// ShiftExt is the /ext digit for the C0/C1 (immediate count) and D0-D3
// (by 1 / by CL) shift-group opcodes.
var ShiftExt = map[string]byte{"rol": 0, "ror": 1, "shl": 4, "shr": 5, "sar": 7}

// Grp3Ext is the /ext digit for the F6/F7 group: not, neg, and the
// one-operand mul/imul/div/idiv forms (whose other operand is implicit
// in RAX:RDX).
var Grp3Ext = map[string]byte{"not": 2, "neg": 3, "mul1": 4, "imul1": 5, "div": 6, "idiv": 7}

// Grp5Ext is the /ext digit this encoder uses from the FE/FF group:
// register/memory inc/dec. (FF's other extensions — call/jmp/push
// r/m — are named individually below, since this encoder only ever
// reaches them via register operands.)
var Grp5Ext = map[string]byte{"inc": 0, "dec": 1}

// Fixed opcode bytes and /ext digits for forms outside a systematic group.
const (
	OpLea         = 0x8D
	OpMovRM8      = 0x88 // mov r/m8, r8
	OpMovRM       = 0x89 // mov r/m, r  (dest is r/m)
	OpMovMR       = 0x8B // mov r, r/m  (dest is reg)
	OpMovImm32    = 0xC7 // mov r/m, imm32 (sign-extended); /ext 0
	OpMovImmR     = 0xB8 // mov r, imm32/imm64 (+register folded into low 3 bits)
	OpTest        = 0x85 // test r/m, r
	OpImulRM      = 0xAF // 0F AF: imul r, r/m
	OpImul3       = 0x69 // imul r, r/m, imm32
	OpJmpRel32    = 0xE9
	OpCallRel32   = 0xE8
	OpJccBase     = 0x80 // 0F 80+cc
	OpSetccBase   = 0x90 // 0F 90+cc
	OpCmovccBase  = 0x40 // 0F 40+cc
	OpPushBase    = 0x50 // +register folded into low 3 bits
	OpPopBase     = 0x58
	OpRet         = 0xC3
	OpUD2Lo       = 0x0B // 0F 0B
	OpSyscallLo   = 0x05 // 0F 05
	OpNop         = 0x90
	OpBsr         = 0xBD // 0F BD
	OpBsf         = 0xBC // 0F BC
	OpBswapBase   = 0xC8 // 0F C8+register
	OpXchg        = 0x87 // xchg r/m, r (implicitly LOCKed when r/m is memory)
	OpLockXadd    = 0xC1 // 0F C1, with an explicit LOCK prefix
	OpLockCmpxchg = 0xB1 // 0F B1, with an explicit LOCK prefix
	OpMfence      = 0xAE // 0F AE F0
	OpPopcntLo    = 0xB8 // 0F B8, with an F3 mandatory prefix
	OpMovzx8      = 0xB6 // 0F B6: movzx r32, r/m8
	OpMovzx16     = 0xB7 // 0F B7: movzx r32, r/m16
	OpMovsx8      = 0xBE // 0F BE: movsx r, r/m8
	OpMovsx16     = 0xBF // 0F BF: movsx r, r/m16
	OpMovsxd      = 0x63 // movsxd r64, r/m32
	OpCallRegExt  = 2    // FF /2: call r/m
	OpJmpRegExt   = 4    // FF /4: jmp r/m
	OpCdq         = 0x99
	OpCld         = 0xFC
	OpStd         = 0xFD
	OpRepMovsb    = 0xA4 // preceded by F3
	OpRepStosb    = 0xAA // preceded by F3
)