package ptx

// Opt is an instruction option qualifier (rounding, ftz, sat, ...),
// stored with its leading dot(s).
type Opt string

const (
	// FP rounding.
	Rn Opt = ".rn"
	Rz Opt = ".rz"
	Rm Opt = ".rm"
	Rp Opt = ".rp"
	Rs Opt = ".rs"
	// Integer rounding (cvt).
	Rni Opt = ".rni"
	Rzi Opt = ".rzi"
	Rmi Opt = ".rmi"
	Rpi Opt = ".rpi"

	Ftz       Opt = ".ftz"
	Sat       Opt = ".sat"
	Approx    Opt = ".approx"
	ApproxFtz Opt = ".approx.ftz"
	FullFtz   Opt = ".full.ftz"
	Uni       Opt = ".uni"
	NaN       Opt = ".NaN"
	Relu      Opt = ".relu"
)

func optString(opts []Opt) string {
	s := ""
	for _, o := range opts {
		s += string(o)
	}
	return s
}

// Scope is a memory-consistency scope qualifier.
type Scope string

const (
	ScopeCTA     Scope = ".cta"
	ScopeCluster Scope = ".cluster"
	ScopeGPU     Scope = ".gpu"
	ScopeSys     Scope = ".sys"
)

// Order is a memory-ordering semantics qualifier.
type Order string

const (
	Relaxed Order = ".relaxed"
	Acquire Order = ".acquire"
	Release Order = ".release"
	AcqRel  Order = ".acq_rel"
	SC      Order = ".sc"
)

// MembarLevel is the legacy membar level.
type MembarLevel string

const (
	LevelCTA MembarLevel = "cta"
	LevelGL  MembarLevel = "gl"
	LevelSys MembarLevel = "sys"
)

// AtomOp is an atom.* operation.
type AtomOp string

const (
	AtomAdd  AtomOp = "add"
	AtomSub  AtomOp = "sub" // via add of negative at SASS level; kept for symmetry
	AtomExch AtomOp = "exch"
	AtomMin  AtomOp = "min"
	AtomMax  AtomOp = "max"
	AtomInc  AtomOp = "inc"
	AtomDec  AtomOp = "dec"
	AtomCAS  AtomOp = "cas"
	AtomAnd  AtomOp = "and"
	AtomOr   AtomOp = "or"
	AtomXor  AtomOp = "xor"
)

// RedOp is a red.* reduction operation.
type RedOp string

const (
	RedAdd RedOp = "add"
	RedMin RedOp = "min"
	RedMax RedOp = "max"
	RedInc RedOp = "inc"
	RedDec RedOp = "dec"
	RedAnd RedOp = "and"
	RedOr  RedOp = "or"
	RedXor RedOp = "xor"
)

// ShflMode is a shfl.sync mode.
type ShflMode string

const (
	ShflUp   ShflMode = "up"
	ShflDown ShflMode = "down"
	ShflBfly ShflMode = "bfly"
	ShflIdx  ShflMode = "idx"
)

// VoteMode is a vote.sync mode.
type VoteMode string

const (
	VoteAll    VoteMode = "all"
	VoteAny    VoteMode = "any"
	VoteUni    VoteMode = "uni"
	VoteBallot VoteMode = "ballot"
)

// ReduxOp is a redux.sync operation.
type ReduxOp string

const (
	ReduxAdd ReduxOp = "add"
	ReduxMin ReduxOp = "min"
	ReduxMax ReduxOp = "max"
	ReduxAnd ReduxOp = "and"
	ReduxOr  ReduxOp = "or"
	ReduxXor ReduxOp = "xor"
)

// CmpOp is a setp comparison operator.
type CmpOp string

const (
	CmpEq CmpOp = "eq"
	CmpNe CmpOp = "ne"
	CmpLt CmpOp = "lt"
	CmpLe CmpOp = "le"
	CmpGt CmpOp = "gt"
	CmpGe CmpOp = "ge"
	// Unordered FP comparisons.
	CmpEqu CmpOp = "equ"
	CmpNeu CmpOp = "neu"
	CmpLtu CmpOp = "ltu"
	CmpLeu CmpOp = "leu"
	CmpGtu CmpOp = "gtu"
	CmpGeu CmpOp = "geu"
	CmpNum CmpOp = "num"
	CmpNan CmpOp = "nan"
)