package amdtx

// Op is a virtual, target-shaped opcode. The lower pass maps each Op to a
// physical amdgcn instruction (format + hardware opcode).
type Op int

const (
	OpInvalid Op = iota

	// Scalar ALU (SOP2 / SOP1 shaped).
	OpSAndB32
	OpSOrB32
	OpSXorB32
	OpSNandB32
	OpSNorB32
	OpSAndn2B32
	OpSLshlB32
	OpSLshrB32
	OpSAshrI32
	OpSAddU32
	OpSSubU32
	OpSAddcU32
	OpSMulI32
	OpSMovB32
	OpSCselectB32

	// Vector ALU (VOP2 / VOP1 / VOP3 shaped).
	OpVAndB32
	OpVOrB32
	OpVXorB32
	OpVNotB32
	OpVLshlrevB32
	OpVLshrrevB32
	OpVAshrrevI32
	OpVAddU32
	OpVSubU32
	OpVMulLoU32
	OpVMulHiU32
	OpVMadU32U24
	OpVAddCoU32
	OpVAddcCoU32
	OpVAddF32
	OpVSubF32
	OpVMulF32
	OpVFmaF32
	OpVMaxF32
	OpVMinF32
	OpVRcpF32
	OpVRsqF32
	OpVSqrtF32
	OpVExpF32
	OpVLogF32
	OpVCvtF32I32
	OpVCvtI32F32
	OpVMovB32
	OpVCndmaskB32

	// Compares (produce a predicate / SCC).
	OpVCmpLtU32
	OpVCmpEqU32
	OpVCmpGtU32
	OpSCmpEqU32
	OpSCmpLtU32

	// Memory.
	OpSLoadDword
	OpSLoadDwordx2
	OpSLoadDwordx4
	OpGlobalLoadDword
	OpGlobalStoreDword
	OpFlatLoadDword
	OpFlatStoreDword
	OpDsReadB32
	OpDsWriteB32
	OpGlobalAtomicAddU32

	// Control / sync.
	OpWaitcnt
	OpBarrier
	OpBarrierSignal
	OpBarrierWait
	OpBpermuteB32
	OpReadlaneB32
	OpWritelaneB32
	OpEndpgm
	OpNop
	OpTrap

	// Branches (symbolic labels; resolved to dword offsets in lower).
	OpBranch
	OpCbranchSCC0
	OpCbranchSCC1
	OpCbranchExecz
	OpCbranchVccz
	OpCbranchVccnz

	// Structured control markers (expanded to EXEC-mask sequences in lower).
	OpIfBegin
	OpElse
	OpEndControl
	OpLoopBegin
	OpLoopBreakIf
	OpLoopEnd

	// Escape hatches.
	OpRaw      // literal amdgcn text
	OpRawBytes // literal instruction bytes
	OpLoc      // .loc debug marker
)

// Inst is one virtual instruction: an opcode, a typed width, ordered operands,
// and any op-specific payload (label name, waitcnt, raw text/bytes, loc).
type Inst struct {
	Op   Op
	Type Type

	Dst  []Reg
	Src  []Operand

	Label   string    // for branch/label ops and control markers
	Wait    []Waitcnt // for OpWaitcnt
	RawText string    // for OpRaw
	RawData []byte    // for OpRawBytes

	// Debug location (OpLoc / attached to any inst).
	FileIdx int
	Line    int
	Col     int

	// forcedEnc lets a caller pin a physical encoding (see WithEncoding).
	forcedEnc Encoding
}

// Encoding names an amdgcn instruction encoding family.
type Encoding int

const (
	EncAuto Encoding = iota
	SOP2
	SOP1
	SOPK
	SOPP
	SOPC
	SMEM
	VOP1
	VOP2
	VOP3
	VOP3P
	VOPC
	DS
	FLAT
	GLOBAL
	SCRATCH
	MUBUF
	MTBUF
	MIMG
)

func (e Encoding) String() string {
	return [...]string{
		"auto", "SOP2", "SOP1", "SOPK", "SOPP", "SOPC", "SMEM",
		"VOP1", "VOP2", "VOP3", "VOP3P", "VOPC", "DS",
		"FLAT", "GLOBAL", "SCRATCH", "MUBUF", "MTBUF", "MIMG",
	}[e]
}

// WithEncoding pins the physical encoding the lower/encoder must use for the
// most recently emitted instruction, overriding auto-selection.
func (cb *CodeBuilder) WithEncoding(e Encoding) *CodeBuilder {
	if n := len(cb.insts); n > 0 {
		cb.insts[n-1].forcedEnc = e
	}
	return cb
}