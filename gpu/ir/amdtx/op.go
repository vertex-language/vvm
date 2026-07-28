package amdtx

import "strings"

// Op identifies a modelled AMDTX instruction. AMDTX mnemonics map one-to-one
// onto amdgcn instruction families and the grammar admits any identifier as
// a mnemonic, so this table models the instructions with structure the IR
// cares about — memory width, control flow, synchronisation, termination —
// and everything else goes through Body.Inst.
//
// Mnemonics use GFX11-style bit-width naming for all targets (P4). Lowering
// rewrites to the legacy GFX9 spelling where the target requires it.
type Op uint16

const (
	OpCustom Op = iota // mnemonic supplied by the caller

	// Scalar memory.
	OpSLoad
	OpSStore

	// Vector memory.
	OpGlobalLoad
	OpGlobalStore
	OpFlatLoad
	OpFlatStore
	OpScratchLoad
	OpScratchStore
	OpBufferLoad
	OpBufferStore

	// LDS.
	OpDSLoad
	OpDSStore

	// Synchronisation.
	OpWaitcnt
	OpWaitcntVScnt
	OpFence
	OpBarrier

	// Control flow and termination.
	OpBranch
	OpCBranchSCC0
	OpCBranchSCC1
	OpCBranchExecZ
	OpCBranchExecNZ
	OpCall
	OpRet
	OpEndPgm

	OpNop
)

type opInfo struct {
	mnemonic string
	// widthSuffix appends the data register's width to the mnemonic, so
	// the width is stated exactly once and V9 holds by construction.
	widthSuffix bool
	feature     feature
}

// feature is the target capability an opcode requires.
type feature uint8

const (
	featNone feature = iota
	featVSCnt   // GFX10 or GFX11 (V12)
	featCDNA    // MFMA (V10)
	featGFX11   // WMMA (V11)
)

var opTable = map[Op]opInfo{
	OpCustom: {"", false, featNone},

	OpSLoad:  {"s_load_", true, featNone},
	OpSStore: {"s_store_", true, featNone},

	OpGlobalLoad:   {"global_load_", true, featNone},
	OpGlobalStore:  {"global_store_", true, featNone},
	OpFlatLoad:     {"flat_load_", true, featNone},
	OpFlatStore:    {"flat_store_", true, featNone},
	OpScratchLoad:  {"scratch_load_", true, featNone},
	OpScratchStore: {"scratch_store_", true, featNone},
	OpBufferLoad:   {"buffer_load_", true, featNone},
	OpBufferStore:  {"buffer_store_", true, featNone},

	OpDSLoad:  {"ds_load_", true, featNone},
	OpDSStore: {"ds_store_", true, featNone},

	OpWaitcnt:      {"waitcnt", false, featNone},
	OpWaitcntVScnt: {"waitcnt_vscnt", false, featVSCnt},
	OpFence:        {"fence", false, featNone},
	OpBarrier:      {"s_barrier", false, featNone},

	OpBranch:        {"s_branch", false, featNone},
	OpCBranchSCC0:   {"s_cbranch_scc0", false, featNone},
	OpCBranchSCC1:   {"s_cbranch_scc1", false, featNone},
	OpCBranchExecZ:  {"s_cbranch_execz", false, featNone},
	OpCBranchExecNZ: {"s_cbranch_execnz", false, featNone},
	OpCall:          {"call", false, featNone},
	OpRet:           {"ret", false, featNone},
	OpEndPgm:        {"s_endpgm", false, featNone},

	OpNop: {"s_nop", false, featNone},
}

// IsValid reports whether o names a modelled opcode.
func (o Op) IsValid() bool { _, ok := opTable[o]; return ok }

// Class is the instruction family a mnemonic belongs to. It is derived from
// the mnemonic rather than stored, so custom mnemonics are classified on
// exactly the same footing as modelled ones.
type Class uint8

const (
	ClassUnknown Class = iota
	ClassSALU
	ClassSMEM
	ClassSFlow  // scalar control flow, barriers, waits
	ClassVALU
	ClassVMEM
	ClassLDS
	ClassPseudo // waitcnt, fence, ret, call, raw
)

// Classify returns the instruction family of a mnemonic.
func Classify(mn string) Class {
	switch mn {
	case "waitcnt", "waitcnt_vscnt", "fence", "ret", "call":
		return ClassPseudo
	case "s_endpgm", "s_barrier", "s_nop", "s_sleep", "s_setpc_b64", "s_getpc_b64":
		return ClassSFlow
	}
	switch {
	case strings.HasPrefix(mn, "s_load"), strings.HasPrefix(mn, "s_store"),
		strings.HasPrefix(mn, "s_buffer_load"):
		return ClassSMEM
	case strings.HasPrefix(mn, "s_branch"), strings.HasPrefix(mn, "s_cbranch"),
		strings.HasPrefix(mn, "s_waitcnt"):
		return ClassSFlow
	case strings.HasPrefix(mn, "s_"):
		return ClassSALU
	case strings.HasPrefix(mn, "v_"):
		return ClassVALU
	case strings.HasPrefix(mn, "ds_"):
		return ClassLDS
	case strings.HasPrefix(mn, "global_"), strings.HasPrefix(mn, "flat_"),
		strings.HasPrefix(mn, "scratch_"), strings.HasPrefix(mn, "buffer_"),
		strings.HasPrefix(mn, "tbuffer_"):
		return ClassVMEM
	}
	return ClassUnknown
}

// IsLoad reports whether a mnemonic reads memory into a register, which is
// what makes a wait counter pending (W1).
func IsLoad(mn string) bool {
	return strings.Contains(mn, "_load") || strings.Contains(mn, "_atomic")
}

// PendingCounter returns the counter a mnemonic increments, or NoCounter.
func PendingCounter(mn string) CounterKind {
	switch Classify(mn) {
	case ClassSMEM, ClassLDS:
		return LGKMCnt
	case ClassVMEM:
		if IsLoad(mn) {
			return VMCnt
		}
		return VSCnt
	}
	return NoCounter
}

// WidthOfMnemonic extracts the trailing bit-width suffix of a mnemonic, or
// NoWidth if it carries none. Load and store width must equal the declared
// width of the data register (V9), and this is how that is checked for
// mnemonics the package does not model.
func WidthOfMnemonic(mn string) Width {
	i := strings.LastIndex(mn, "_b")
	if i < 0 || i+2 >= len(mn) {
		return NoWidth
	}
	n := 0
	for _, c := range mn[i+2:] {
		if c < '0' || c > '9' {
			return NoWidth
		}
		n = n*10 + int(c-'0')
	}
	return Width(n)
}