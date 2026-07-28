package ptx

import "strconv"

// A Qual is an instruction qualifier. Qualifiers are supplied variadically
// to emit methods in any order; each one writes a distinct field of Quals,
// and the printer emits them in the canonical order recorded for the opcode.
// Supplying two qualifiers of the same category is a Verify diagnostic, not
// a silent last-wins.
type Qual interface{ apply(*Quals) }

// Quals is the full set of qualifier slots an instruction may carry. Each
// slot's zero value means "absent".
type Quals struct {
	Space Space
	Sem   Sem
	Scope Scope
	Round Round
	Cache CacheOp
	Evict Evict
	Width Width
	Cmp   Cmp
	Bool  BoolOp
	Atom  AtomOp
	Shfl  ShflMode
	Vote  VoteMode
	Match MatchMode
	Redux ReduxOp
	Testp TestpOp
	Level Level
	Proxy Proxy
	Dir   Dir
	Shf   ShfMode
	Vec   int

	Ftz       bool
	Sat       bool
	SatFinite bool
	Approx    bool
	Full      bool
	Uni       bool
	Aligned   bool
	NC        bool
	Relu      bool
	NaN       bool
	Abs       bool
	To        bool
	ShiftAmt  bool
}

// qualKind identifies one qualifier slot. Each Op records the order in which
// its slots are emitted.
type qualKind uint8

const (
	qSem qualKind = iota
	qScope
	qSpace
	qAtom
	qWidth
	qCmp
	qBool
	qRound
	qFtz
	qSat
	qSatFinite
	qApprox
	qFull
	qCache
	qNC
	qEvict
	qVec
	qUni
	qAligned
	qRelu
	qNaN
	qAbs
	qTo
	qShiftAmt
	qShfl
	qVote
	qMatch
	qRedux
	qTestp
	qLevel
	qProxy
	qDir
	qShf
)

// text renders one qualifier slot, or "" if the slot is unset.
func (q Quals) text(k qualKind) string {
	switch k {
	case qSem:
		return q.Sem.String()
	case qScope:
		return q.Scope.String()
	case qSpace:
		return q.Space.String()
	case qAtom:
		return q.Atom.String()
	case qWidth:
		return q.Width.String()
	case qCmp:
		return q.Cmp.String()
	case qBool:
		return q.Bool.String()
	case qRound:
		return q.Round.String()
	case qCache:
		return q.Cache.String()
	case qEvict:
		return q.Evict.String()
	case qShfl:
		return q.Shfl.String()
	case qVote:
		return q.Vote.String()
	case qMatch:
		return q.Match.String()
	case qRedux:
		return q.Redux.String()
	case qTestp:
		return q.Testp.String()
	case qLevel:
		return q.Level.String()
	case qProxy:
		return q.Proxy.String()
	case qDir:
		return q.Dir.String()
	case qShf:
		return q.Shf.String()
	case qVec:
		if q.Vec != 0 {
			return ".v" + strconv.Itoa(q.Vec)
		}
	case qFtz:
		if q.Ftz {
			return ".ftz"
		}
	case qSat:
		if q.Sat {
			return ".sat"
		}
	case qSatFinite:
		if q.SatFinite {
			return ".satfinite"
		}
	case qApprox:
		if q.Approx {
			return ".approx"
		}
	case qFull:
		if q.Full {
			return ".full"
		}
	case qUni:
		if q.Uni {
			return ".uni"
		}
	case qAligned:
		if q.Aligned {
			return ".aligned"
		}
	case qNC:
		if q.NC {
			return ".nc"
		}
	case qRelu:
		if q.Relu {
			return ".relu"
		}
	case qNaN:
		if q.NaN {
			return ".NaN"
		}
	case qAbs:
		if q.Abs {
			return ".abs"
		}
	case qTo:
		if q.To {
			return ".to"
		}
	case qShiftAmt:
		if q.ShiftAmt {
			return ".shiftamt"
		}
	}
	return ""
}

// ---- Qualifier categories -------------------------------------------------

// Round is a rounding-mode qualifier.
type Round uint8

const (
	NoRound Round = iota
	RN            // .rn  round to nearest even
	RZ            // .rz  round toward zero
	RM            // .rm  round toward -inf
	RP            // .rp  round toward +inf
	RS            // .rs  stochastic rounding
	RNI           // .rni integer round to nearest even
	RZI           // .rzi integer round toward zero
	RMI           // .rmi integer round toward -inf
	RPI           // .rpi integer round toward +inf
)

func (r Round) String() string {
	return [...]string{"", ".rn", ".rz", ".rm", ".rp", ".rs", ".rni", ".rzi", ".rmi", ".rpi"}[r]
}
func (r Round) apply(q *Quals) { q.Round = r }

// Sem is a memory-ordering semantics qualifier.
type Sem uint8

const (
	NoSem Sem = iota
	SemWeak
	SemRelaxed
	SemAcquire
	SemRelease
	SemAcqRel
	SemSC
	SemMMIO
)

func (s Sem) String() string {
	return [...]string{"", ".weak", ".relaxed", ".acquire", ".release", ".acq_rel", ".sc", ".mmio"}[s]
}
func (s Sem) apply(q *Quals) { q.Sem = s }

// Scope is a memory-consistency scope qualifier.
type Scope uint8

const (
	NoScope Scope = iota
	ScopeCTA
	ScopeCluster
	ScopeGPU
	ScopeSys
)

func (s Scope) String() string {
	return [...]string{"", ".cta", ".cluster", ".gpu", ".sys"}[s]
}
func (s Scope) apply(q *Quals) { q.Scope = s }

// CacheOp is a cache operator on ld/st.
type CacheOp uint8

const (
	NoCache CacheOp = iota
	CA              // .ca  cache at all levels
	CG              // .cg  cache at global level
	CS              // .cs  cache streaming
	LU              // .lu  last use
	CV              // .cv  don't cache and fetch again
	WB              // .wb  cache write-back
	WT              // .wt  cache write-through
)

func (c CacheOp) String() string {
	return [...]string{"", ".ca", ".cg", ".cs", ".lu", ".cv", ".wb", ".wt"}[c]
}
func (c CacheOp) apply(q *Quals) { q.Cache = c }

// Evict is an L1 cache eviction-priority hint.
type Evict uint8

const (
	NoEvict Evict = iota
	EvictNormal
	EvictFirst
	EvictLast
	EvictUnchanged
	EvictNoAllocate
)

func (e Evict) String() string {
	return [...]string{"",
		".L1::evict_normal", ".L1::evict_first", ".L1::evict_last",
		".L1::evict_unchanged", ".L1::no_allocate"}[e]
}
func (e Evict) apply(q *Quals) { q.Evict = e }

// Width selects the result half of a widening multiply.
type Width uint8

const (
	NoWidth Width = iota
	MulLo
	MulHi
	MulWide
)

func (w Width) String() string { return [...]string{"", ".lo", ".hi", ".wide"}[w] }
func (w Width) apply(q *Quals)  { q.Width = w }

// Cmp is a comparison operator for set/setp.
type Cmp uint8

const (
	NoCmp Cmp = iota
	Eq
	Ne
	Lt
	Le
	Gt
	Ge
	Lo // unsigned lower
	Ls // unsigned lower-or-same
	Hi // unsigned higher
	Hs // unsigned higher-or-same
	Equ
	Neu
	Ltu
	Leu
	Gtu
	Geu
	Num
	Nan
)

func (c Cmp) String() string {
	return [...]string{"", ".eq", ".ne", ".lt", ".le", ".gt", ".ge",
		".lo", ".ls", ".hi", ".hs",
		".equ", ".neu", ".ltu", ".leu", ".gtu", ".geu", ".num", ".nan"}[c]
}
func (c Cmp) apply(q *Quals) { q.Cmp = c }

// BoolOp is the optional second-stage boolean operator on setp.
type BoolOp uint8

const (
	NoBool BoolOp = iota
	BoolAnd
	BoolOr
	BoolXor
)

func (b BoolOp) String() string { return [...]string{"", ".and", ".or", ".xor"}[b] }
func (b BoolOp) apply(q *Quals)  { q.Bool = b }

// AtomOp is an atom.* / red.* operation.
type AtomOp uint8

const (
	NoAtom AtomOp = iota
	AtomAdd
	AtomExch
	AtomMin
	AtomMax
	AtomInc
	AtomDec
	AtomCAS
	AtomAnd
	AtomOr
	AtomXor
)

func (a AtomOp) String() string {
	return [...]string{"", ".add", ".exch", ".min", ".max", ".inc", ".dec",
		".cas", ".and", ".or", ".xor"}[a]
}
func (a AtomOp) apply(q *Quals) { q.Atom = a }

// ShflMode is a shfl.sync mode.
type ShflMode uint8

const (
	NoShfl ShflMode = iota
	ShflUp
	ShflDown
	ShflBfly
	ShflIdx
)

func (s ShflMode) String() string { return [...]string{"", ".up", ".down", ".bfly", ".idx"}[s] }
func (s ShflMode) apply(q *Quals)  { q.Shfl = s }

// VoteMode is a vote.sync mode.
type VoteMode uint8

const (
	NoVote VoteMode = iota
	VoteAll
	VoteAny
	VoteUni
	VoteBallot
)

func (v VoteMode) String() string { return [...]string{"", ".all", ".any", ".uni", ".ballot"}[v] }
func (v VoteMode) apply(q *Quals)  { q.Vote = v }

// MatchMode is a match.sync mode.
type MatchMode uint8

const (
	NoMatch MatchMode = iota
	MatchAny
	MatchAll
)

func (m MatchMode) String() string { return [...]string{"", ".any", ".all"}[m] }
func (m MatchMode) apply(q *Quals)  { q.Match = m }

// ReduxOp is a redux.sync operation.
type ReduxOp uint8

const (
	NoRedux ReduxOp = iota
	ReduxAdd
	ReduxMin
	ReduxMax
	ReduxAnd
	ReduxOr
	ReduxXor
)

func (r ReduxOp) String() string {
	return [...]string{"", ".add", ".min", ".max", ".and", ".or", ".xor"}[r]
}
func (r ReduxOp) apply(q *Quals) { q.Redux = r }

// TestpOp is a testp predicate class.
type TestpOp uint8

const (
	NoTestp TestpOp = iota
	Finite
	Infinite
	Number
	NotANumber
	Normal
	Subnormal
)

func (t TestpOp) String() string {
	return [...]string{"", ".finite", ".infinite", ".number", ".notanumber",
		".normal", ".subnormal"}[t]
}
func (t TestpOp) apply(q *Quals) { q.Testp = t }

// Level is a membar level or a prefetch cache level.
type Level uint8

const (
	NoLevel Level = iota
	LevelCTA
	LevelGL
	LevelSys
	LevelL1
	LevelL2
)

func (l Level) String() string {
	return [...]string{"", ".cta", ".gl", ".sys", ".L1", ".L2"}[l]
}
func (l Level) apply(q *Quals) { q.Level = l }

// Proxy is a fence proxy kind.
type Proxy uint8

const (
	NoProxy Proxy = iota
	ProxyAlias
	ProxyAsync
	ProxyAsyncGlobal
	ProxyAsyncShared
	ProxyTensormap
	ProxyMBarrierInit
)

func (p Proxy) String() string {
	return [...]string{"", ".proxy.alias", ".proxy.async", ".proxy.async.global",
		".proxy.async.shared", ".proxy.tensormap", ".mbarrier_init"}[p]
}
func (p Proxy) apply(q *Quals) { q.Proxy = p }

// Dir is a shift direction for shf.
type Dir uint8

const (
	NoDir Dir = iota
	DirL
	DirR
)

func (d Dir) String() string { return [...]string{"", ".l", ".r"}[d] }
func (d Dir) apply(q *Quals)  { q.Dir = d }

// ShfMode is the wrap behaviour of a funnel shift.
type ShfMode uint8

const (
	NoShf ShfMode = iota
	Clamp
	Wrap
)

func (s ShfMode) String() string { return [...]string{"", ".clamp", ".wrap"}[s] }
func (s ShfMode) apply(q *Quals)  { q.Shf = s }

// Space, when supplied as a qualifier, sets the state space slot.
func (s Space) apply(q *Quals) { q.Space = s }

// ---- Flag qualifiers ------------------------------------------------------

type flag uint8

const (
	// Ftz flushes subnormal inputs and results to zero.
	Ftz flag = iota
	// Sat clamps the result to the type's saturation range.
	Sat
	// SatFinite clamps conversions to the finite range of the target type.
	SatFinite
	// Approx selects the fast approximate implementation.
	Approx
	// Full selects the full-range approximate division.
	Full
	// Uni marks a branch or call as warp-uniform.
	Uni
	// Aligned asserts that all threads in the CTA reach the barrier.
	Aligned
	// NC loads through the non-coherent (read-only) cache.
	NC
	// Relu clamps negative conversion results to zero.
	Relu
	// NaN makes min/max return NaN if either input is NaN.
	NaN
	// Abs takes the magnitude of the inputs before comparing.
	Abs
	// To selects the generic-to-space direction of cvta.
	To
	// ShiftAmt makes bfind return a shift amount rather than a bit position.
	ShiftAmt
)

func (f flag) apply(q *Quals) {
	switch f {
	case Ftz:
		q.Ftz = true
	case Sat:
		q.Sat = true
	case SatFinite:
		q.SatFinite = true
	case Approx:
		q.Approx = true
	case Full:
		q.Full = true
	case Uni:
		q.Uni = true
	case Aligned:
		q.Aligned = true
	case NC:
		q.NC = true
	case Relu:
		q.Relu = true
	case NaN:
		q.NaN = true
	case Abs:
		q.Abs = true
	case To:
		q.To = true
	case ShiftAmt:
		q.ShiftAmt = true
	}
}

// vecQual is set from operand arity, not by the caller.
type vecQual int

func (v vecQual) apply(q *Quals) { q.Vec = int(v) }

func buildQuals(qs []Qual) Quals {
	var q Quals
	for _, x := range qs {
		if x != nil {
			x.apply(&q)
		}
	}
	return q
}