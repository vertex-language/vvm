package arm

// DPOp describes one data-processing instruction's 4-bit opcode field
// (A32 bits 24:21). Cmp is true for the compare-only forms (tst/cmp/cmn)
// that always set flags and never write a destination register.
type DPOp struct {
	Name string
	Code uint32
	Cmp  bool
}

var DPOps = []DPOp{
	{"and", 0x0, false},
	{"eor", 0x1, false},
	{"sub", 0x2, false},
	{"rsb", 0x3, false},
	{"add", 0x4, false},
	{"tst", 0x8, true},
	{"cmp", 0xA, true},
	{"cmn", 0xB, true},
	{"orr", 0xC, false},
	{"bic", 0xE, false},
}

var dpByName = map[string]DPOp{}

func init() {
	for _, d := range DPOps {
		dpByName[d.Name] = d
	}
}

func DPByName(name string) (DPOp, bool) { d, ok := dpByName[name]; return d, ok }

// ShiftOp describes one shift/rotate mnemonic's 2-bit shift-type field
// (A32 bits 6:5), shared by the register-shifted-register form and by
// LSL/LSR/ASR/ROR used standalone.
type ShiftOp struct {
	Name string
	Code uint32
}

var ShiftOps = []ShiftOp{
	{"lsl", 0}, {"lsr", 1}, {"asr", 2}, {"ror", 3},
}

var shiftByName = map[string]ShiftOp{}

func init() {
	for _, s := range ShiftOps {
		shiftByName[s.Name] = s
	}
}

func ShiftByName(name string) (ShiftOp, bool) { s, ok := shiftByName[name]; return s, ok }

// Fixed A32 base words for instructions that have exactly one encoded
// form. Register fields (Rd/Rn/Rm/Rs/RdHi/RdLo) are zero in each Base;
// isa/arm/encoder ORs them in at the fixed bit positions noted below —
// that placement is itself an ISA fact (would still be true for any A32
// assembler), just not one that fits a uniform Name->Base table the way
// the DP/shift forms above do, since each shape's field layout differs.
//
// Forms that bake in cond=AL (0xE, unconditional) are the ones this ISA
// never varies the condition on in practice; forms whose cond nibble is
// left zero are meant to be OR'd with a caller-supplied cond<<28.
const (
	BaseMOVR  uint32 = 0xE1A00000 // MOV Rd,Rm            -- Rd:12 Rm:0
	BaseMVN   uint32 = 0xE1E00000 // MVN Rd,Rm            -- Rd:12 Rm:0
	BaseMOVW  uint32 = 0xE3000000 // MOVW Rd,#imm16       -- Rd:12 imm4:16 imm12:0
	BaseMOVT  uint32 = 0xE3400000 // MOVT Rd,#imm16       -- Rd:12 imm4:16 imm12:0
	BaseMOVCCI uint32 = 0x03A00000 // + cond<<28: MOVcc Rd,#imm8  -- Rd:12 imm8:0
	BaseMOVCCR uint32 = 0x01A00000 // + cond<<28: MOVcc Rd,Rm    -- Rd:12 Rm:0

	BaseMUL   uint32 = 0xE0000090 // MUL Rd,Rm,Rs             -- Rd:16 Rs:8 Rm:0
	BaseMLS   uint32 = 0xE0600090 // MLS Rd,Rm,Rs,Ra          -- Rd:16 Ra:12 Rs:8 Rm:0
	BaseUMULL uint32 = 0xE0800090 // UMULL RdLo,RdHi,Rm,Rs    -- RdHi:16 RdLo:12 Rs:8 Rm:0
	BaseSMULL uint32 = 0xE0C00090 // SMULL RdLo,RdHi,Rm,Rs    -- RdHi:16 RdLo:12 Rs:8 Rm:0
	BaseUDIV  uint32 = 0xE730F010 // UDIV Rd,Rn,Rm            -- Rd:16 Rm:8 Rn:0
	BaseSDIV  uint32 = 0xE710F010 // SDIV Rd,Rn,Rm            -- Rd:16 Rm:8 Rn:0

	BaseCLZ  uint32 = 0xE16F0F10 // CLZ Rd,Rm  -- Rd:12 Rm:0
	BaseRBIT uint32 = 0xE6FF0F30 // RBIT Rd,Rm -- Rd:12 Rm:0
	BaseREV  uint32 = 0xE6BF0F30 // REV Rd,Rm  -- Rd:12 Rm:0
	BaseUXTB uint32 = 0xE6EF0070 // UXTB Rd,Rm -- Rd:12 Rm:0
	BaseUXTH uint32 = 0xE6FF0070 // UXTH Rd,Rm -- Rd:12 Rm:0
	BaseSXTB uint32 = 0xE6AF0070 // SXTB Rd,Rm -- Rd:12 Rm:0
	BaseSXTH uint32 = 0xE6BF0070 // SXTH Rd,Rm -- Rd:12 Rm:0

	// CMP Rn, Rm, ASR #31 (a fixed shift-immediate compare, not a general
	// shifted-operand form): Rn:16, imm5=31 baked into bits 11:7, shift
	// type ASR baked into bits 6:5, Rm:0.
	BaseCMPASR31 uint32 = 0xE1500000

	BaseLDR   uint32 = 0xE5100000 // LDR  Rt,[Rn,#+/-imm12] -- U:23 Rn:16 Rt:12 imm12:0
	BaseSTR   uint32 = 0xE5000000 // STR  Rt,[Rn,#+/-imm12]
	BaseLDRB  uint32 = 0xE5500000 // LDRB Rt,[Rn,#+/-imm12]
	BaseSTRB  uint32 = 0xE5400000 // STRB Rt,[Rn,#+/-imm12]
	BaseLDRH  uint32 = 0xE15000B0 // LDRH  Rt,[Rn,#+/-imm8] -- U:23 Rn:16 Rt:12 imm8 split 11:8/3:0
	BaseSTRH  uint32 = 0xE14000B0 // STRH  Rt,[Rn,#+/-imm8]
	BaseLDRSB uint32 = 0xE15000D0 // LDRSB Rt,[Rn,#+/-imm8]
	BaseLDRSH uint32 = 0xE15000F0 // LDRSH Rt,[Rn,#+/-imm8]

	BaseLDRBR uint32 = 0xE7D00000 // LDRB Rt,[Rn,Rm] -- Rn:16 Rt:12 Rm:0
	BaseSTRBR uint32 = 0xE7C00000 // STRB Rt,[Rn,Rm] -- Rn:16 Rt:12 Rm:0

	BaseLDREX uint32 = 0xE1900F9F // LDREX Rt,[Rn] -- Rn:16 Rt:12
	BaseSTREX uint32 = 0xE1800F90 // STREX Rd,Rt,[Rn] -- Rn:16 Rd:12 Rt:0
	BaseCLREX uint32 = 0xF57FF01F // CLREX
	BaseDMB   uint32 = 0xF57FF05B // DMB ISH

	BaseB    uint32 = 0xEA000000 // B <label>       (cond AL baked in)
	BaseBcc  uint32 = 0x0A000000 // + cond<<28: Bcc <label>
	BaseBL   uint32 = 0xEB000000 // BL <symbol>     (cond AL baked in)
	BaseBLXR uint32 = 0xE12FFF30 // BLX Rm -- Rm:0
	BaseBXR  uint32 = 0xE12FFF10 // BX Rm  -- Rm:0

	BasePUSH uint32 = 0xE92D0000 // STMDB sp!, {reglist} -- reglist:0-15
	BasePOP  uint32 = 0xE8BD0000 // LDMIA sp!, {reglist} -- reglist:0-15

	BaseUDF uint32 = 0xE7F000F0 // UDF #0 -- canonical deterministic halt
)