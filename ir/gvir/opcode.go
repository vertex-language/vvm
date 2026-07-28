// opcode.go
package gvir

import "fmt"

// Opcode identifies a core GPU IR mnemonic (§8, §9, §10, §11). As in vir
// it replaces bare strings so a typo is a compile error, and every
// opcode's constraint/arity/result rule is registered exactly once in
// opTable below — init() panics at package load if any constant is
// missing an entry.
//
// The vocabulary is closed and deliberately smaller than vir's: no
// overflow predicates, no saturating add/sub, no masked/gather/scatter, no
// reductions over vectors, no syscall, no va_*, no prefetch. What is here
// and not in vir is the subgroup layer (§10.3) and the execution builtins
// (§9).
type Opcode uint16

const (
	OpInvalid Opcode = iota // zero value; never a valid instruction opcode

	// --- Memory and addressing (§8) ---
	OpAlloca
	OpLoad
	OpStore
	OpLoadVol
	OpStoreVol
	OpMemcopy
	OpMemmove
	OpMemset
	OpIndex // index.ptr
	OpField // field.ptr

	// --- Vector element access (§8.3, §4.4) ---
	OpExtract
	OpInsert
	OpSplat
	OpShuffle

	// --- Integer arithmetic (§11.1) ---
	OpAdd // shared with float (§11.3)
	OpSub
	OpMul
	OpNeg
	OpAbs
	OpUDiv
	OpSDiv
	OpURem
	OpSRem
	OpUMulH
	OpSMulH
	OpUMin
	OpUMax
	OpSMin
	OpSMax

	// --- Bitwise and shifts (§11.2) ---
	OpAnd
	OpOr
	OpXor
	OpNot
	OpShl
	OpLShr
	OpAShr
	OpRotl
	OpRotr
	OpCtlz
	OpCttz
	OpPopcnt
	OpBrev
	OpBSwap

	// --- Float arithmetic (§11.3) ---
	OpDiv
	OpSqrt
	OpFma
	OpMin
	OpMax
	OpFloor
	OpCeil
	OpRound
	OpRoundEven
	OpTruncF
	OpCopysign
	OpIsNaN
	OpIsInf

	// --- Integer and pointer comparisons (§11.4) ---
	OpEq
	OpNe
	OpULt
	OpULe
	OpUGt
	OpUGe
	OpSLt
	OpSLe
	OpSGt
	OpSGe

	// --- Float comparisons (§11.4) ---
	OpOEq
	OpONe
	OpOLt
	OpOLe
	OpOGt
	OpOGe
	OpOrd
	OpUEq
	OpUNe
	OpUnord

	// --- Conversions (§11.5) — suffix is the destination type ---
	OpTrunc
	OpSext
	OpZext
	OpFPTrunc
	OpFPExt
	OpSToInt // float -> signed iN, saturating and total
	OpUToInt // float -> unsigned iN, saturating and total
	OpIntToS // signed iN -> fN
	OpIntToU // unsigned iN -> fN
	OpBitcast

	// --- Approximate math (§11.6) — float_profile bounded only ---
	OpRcp
	OpRsqrt
	OpSin
	OpCos
	OpExp2
	OpLog2
	OpTanh

	// --- Select and calls (§11.7) ---
	OpSelect
	OpCall

	// --- Barriers (§10.1) ---
	OpBarrier

	// --- Atomics (§10.2) — relaxed in v1; ordering lives in fence ---
	OpAtomicLoad
	OpAtomicStore
	OpAtomicAdd
	OpAtomicSub
	OpAtomicAnd
	OpAtomicOr
	OpAtomicXor
	OpAtomicXchg
	OpAtomicUMin
	OpAtomicUMax
	OpAtomicSMin
	OpAtomicSMax
	OpCmpxchg
	OpFence

	// --- Subgroup collectives (§10.3) ---
	// OpShuffle above doubles as the subgroup shuffle; see its opTable note.
	OpShuffleXor
	OpShuffleUp
	OpShuffleDown
	OpBroadcast
	OpBroadcastFirst
	OpAny
	OpAll
	OpBallot
	OpSubAdd
	OpSubMin
	OpSubMax
	OpSubAnd
	OpSubOr
	OpSubXor

	// --- Submask ops and constants (§10.3) ---
	OpMaskCount
	OpMaskTest
	OpMaskFirst
	OpMaskEmpty
	OpMaskLt
	OpMaskLe
	OpMaskGt
	OpMaskGe
	OpMaskEq

	// --- Execution builtins (§9) — no operands, optional dim suffix ---
	OpThreadInGrid
	OpThreadInGroup
	OpGroupInGrid
	OpThreadInSubgroup
	OpSubgroupInGroup
	OpThreadsPerGroup
	OpGroupsPerGrid
	OpThreadsPerGrid
	OpThreadsPerSubgroup
	OpSubgroupsPerGroup
	OpDynamicGroupSize

	// --- Debug (§2) ---
	OpLoc

	opcodeCount // sentinel: total defined opcodes; must stay last
)

// operandConstraint restricts which element type an opcode's suffix may
// name, checked against ElemOrSelf(suffix).
type operandConstraint uint8

const (
	ConstraintNone operandConstraint = iota
	ConstraintInt
	ConstraintFloat
	ConstraintIntOrFloat
	ConstraintIntOrPtr
)

// resultRule says how an instruction's result type is derived.
type resultRule uint8

const (
	ruleSuffix       resultRule = iota // result type == Suffix
	ruleVoid                           // never produces a value
	ruleBool                           // i1, or vec[i1,N] for a vector suffix
	ruleSubmask                        // submask (§4.6)
	ruleI32                            // fixed i32 (mask_count, mask_first)
	rulePrivatePtr                     // alloca: ptr[private] (§8.1)
	rulePtrOfOperand                   // index.ptr / field.ptr: operand 0's own
	// pointer type, since `.ptr` names no space and the space is inherited
	// from the base pointer (§8.3).
	ruleBuiltin // §9 table, keyed by opcode and dim suffix
	ruleSpecial // computed by ir/verify with a typing environment
)

type opFlags uint8

const (
	// flagBounded: legal only under float_profile bounded (§11.6).
	flagBounded opFlags = 1 << iota
	// flagBuiltin: §9 execution builtin — takes no operands.
	flagBuiltin
	// flagUniform: requires uniform reachability across its execution
	// scope; non-uniform arrival is UB (§12.8). Covers barrier and every
	// subgroup collective.
	flagUniform
	// flagAtomic: carries a scope as its final operand and no ordering
	// operand (§10.2).
	flagAtomic
)

type opDef struct {
	op      Opcode
	name    string
	numeric operandConstraint
	arity   int // -1 == not pinned by the grammar text
	result  resultRule
	flags   opFlags
}

type opMeta struct {
	numeric operandConstraint
	arity   int
	result  resultRule
	flags   opFlags
}

// opTable is the single source of truth for every opcode. Every Opcode
// constant above must appear here exactly once; init() enforces it.
var opTable = []opDef{
	// Memory (§8). Store takes its destination first.
	{OpAlloca, "alloca", ConstraintNone, 0, rulePrivatePtr, 0},
	{OpLoad, "load", ConstraintNone, 1, ruleSuffix, 0},
	{OpStore, "store", ConstraintNone, 2, ruleVoid, 0},
	{OpLoadVol, "load_vol", ConstraintNone, 1, ruleSuffix, 0},
	{OpStoreVol, "store_vol", ConstraintNone, 2, ruleVoid, 0},
	{OpMemcopy, "memcopy", ConstraintNone, 3, ruleVoid, 0},
	{OpMemmove, "memmove", ConstraintNone, 3, ruleVoid, 0},
	{OpMemset, "memset", ConstraintNone, 3, ruleVoid, 0},
	// index.ptr is byte arithmetic with an i64 offset — no element-type
	// operand, unlike vir's index. field.ptr's k is a literal index.
	{OpIndex, "index", ConstraintNone, 2, rulePtrOfOperand, 0},
	{OpField, "field", ConstraintNone, 2, rulePtrOfOperand, 0},

	{OpExtract, "extract", ConstraintNone, 2, ruleSpecial, 0},
	{OpInsert, "insert", ConstraintNone, 3, ruleSuffix, 0},
	{OpSplat, "splat", ConstraintNone, 1, ruleSuffix, 0},
	// shuffle is overloaded by the spec: §8.3 gives the vector form
	// (a, b, mask...) and §10.3 gives the subgroup form (v, lane). One
	// mnemonic, one suffix, one result rule (== suffix), so a single
	// opcode covers both; ir/verify distinguishes them by operand count
	// and operand kinds. Arity is left unpinned for that reason.
	{OpShuffle, "shuffle", ConstraintNone, -1, ruleSuffix, 0},

	// Integer (§11.1). add/sub/mul/neg/abs are shared with float (§11.3).
	{OpAdd, "add", ConstraintIntOrFloat, 2, ruleSuffix, 0},
	{OpSub, "sub", ConstraintIntOrFloat, 2, ruleSuffix, 0},
	{OpMul, "mul", ConstraintIntOrFloat, 2, ruleSuffix, 0},
	{OpNeg, "neg", ConstraintIntOrFloat, 1, ruleSuffix, 0},
	{OpAbs, "abs", ConstraintIntOrFloat, 1, ruleSuffix, 0},
	{OpUDiv, "udiv", ConstraintInt, 2, ruleSuffix, 0},
	{OpSDiv, "sdiv", ConstraintInt, 2, ruleSuffix, 0},
	{OpURem, "urem", ConstraintInt, 2, ruleSuffix, 0},
	{OpSRem, "srem", ConstraintInt, 2, ruleSuffix, 0},
	{OpUMulH, "umulh", ConstraintInt, 2, ruleSuffix, 0},
	{OpSMulH, "smulh", ConstraintInt, 2, ruleSuffix, 0},
	{OpUMin, "umin", ConstraintInt, 2, ruleSuffix, 0},
	{OpUMax, "umax", ConstraintInt, 2, ruleSuffix, 0},
	{OpSMin, "smin", ConstraintInt, 2, ruleSuffix, 0},
	{OpSMax, "smax", ConstraintInt, 2, ruleSuffix, 0},

	// Bitwise (§11.2). i1 is an int type, so these also cover the
	// vec[i1,N] operand form §4.5 permits.
	{OpAnd, "and", ConstraintInt, 2, ruleSuffix, 0},
	{OpOr, "or", ConstraintInt, 2, ruleSuffix, 0},
	{OpXor, "xor", ConstraintInt, 2, ruleSuffix, 0},
	{OpNot, "not", ConstraintInt, 1, ruleSuffix, 0},
	{OpShl, "shl", ConstraintInt, 2, ruleSuffix, 0},
	{OpLShr, "lshr", ConstraintInt, 2, ruleSuffix, 0},
	{OpAShr, "ashr", ConstraintInt, 2, ruleSuffix, 0},
	{OpRotl, "rotl", ConstraintInt, 2, ruleSuffix, 0},
	{OpRotr, "rotr", ConstraintInt, 2, ruleSuffix, 0},
	{OpCtlz, "ctlz", ConstraintInt, 1, ruleSuffix, 0},
	{OpCttz, "cttz", ConstraintInt, 1, ruleSuffix, 0},
	{OpPopcnt, "popcnt", ConstraintInt, 1, ruleSuffix, 0},
	{OpBrev, "brev", ConstraintInt, 1, ruleSuffix, 0}, // vir spells this bitrev
	{OpBSwap, "bswap", ConstraintInt, 1, ruleSuffix, 0},

	// Float (§11.3). Unlike vir, min/max are unambiguously float-only —
	// integers use umin/umax/smin/smax — so no special case is needed.
	{OpDiv, "div", ConstraintFloat, 2, ruleSuffix, 0},
	{OpSqrt, "sqrt", ConstraintFloat, 1, ruleSuffix, 0},
	{OpFma, "fma", ConstraintFloat, 3, ruleSuffix, 0},
	{OpMin, "min", ConstraintFloat, 2, ruleSuffix, 0},
	{OpMax, "max", ConstraintFloat, 2, ruleSuffix, 0},
	{OpFloor, "floor", ConstraintFloat, 1, ruleSuffix, 0},
	{OpCeil, "ceil", ConstraintFloat, 1, ruleSuffix, 0},
	{OpRound, "round", ConstraintFloat, 1, ruleSuffix, 0},
	{OpRoundEven, "round_even", ConstraintFloat, 1, ruleSuffix, 0},
	{OpTruncF, "trunc_f", ConstraintFloat, 1, ruleSuffix, 0},
	{OpCopysign, "copysign", ConstraintFloat, 2, ruleSuffix, 0},
	{OpIsNaN, "isnan", ConstraintFloat, 1, ruleBool, 0},
	{OpIsInf, "isinf", ConstraintFloat, 1, ruleBool, 0},

	// Integer/pointer comparisons (§11.4). Pointer forms are the bare
	// `.ptr` suffix and are legal within one address space only.
	{OpEq, "eq", ConstraintIntOrPtr, 2, ruleBool, 0},
	{OpNe, "ne", ConstraintIntOrPtr, 2, ruleBool, 0},
	{OpULt, "ult", ConstraintIntOrPtr, 2, ruleBool, 0},
	{OpULe, "ule", ConstraintIntOrPtr, 2, ruleBool, 0},
	{OpUGt, "ugt", ConstraintIntOrPtr, 2, ruleBool, 0},
	{OpUGe, "uge", ConstraintIntOrPtr, 2, ruleBool, 0},
	{OpSLt, "slt", ConstraintInt, 2, ruleBool, 0},
	{OpSLe, "sle", ConstraintInt, 2, ruleBool, 0},
	{OpSGt, "sgt", ConstraintInt, 2, ruleBool, 0},
	{OpSGe, "sge", ConstraintInt, 2, ruleBool, 0},

	// Float comparisons (§11.4).
	{OpOEq, "oeq", ConstraintFloat, 2, ruleBool, 0},
	{OpONe, "one", ConstraintFloat, 2, ruleBool, 0},
	{OpOLt, "olt", ConstraintFloat, 2, ruleBool, 0},
	{OpOLe, "ole", ConstraintFloat, 2, ruleBool, 0},
	{OpOGt, "ogt", ConstraintFloat, 2, ruleBool, 0},
	{OpOGe, "oge", ConstraintFloat, 2, ruleBool, 0},
	{OpOrd, "ord", ConstraintFloat, 2, ruleBool, 0},
	{OpUEq, "ueq", ConstraintFloat, 2, ruleBool, 0},
	{OpUNe, "une", ConstraintFloat, 2, ruleBool, 0},
	{OpUnord, "unord", ConstraintFloat, 2, ruleBool, 0},

	// Conversions (§11.5). Float-to-int is one saturating, total family —
	// there is no separate _sat spelling as in vir.
	{OpTrunc, "trunc", ConstraintInt, 1, ruleSuffix, 0},
	{OpSext, "sext", ConstraintInt, 1, ruleSuffix, 0},
	{OpZext, "zext", ConstraintInt, 1, ruleSuffix, 0},
	{OpFPTrunc, "fptrunc", ConstraintFloat, 1, ruleSuffix, 0},
	{OpFPExt, "fpext", ConstraintFloat, 1, ruleSuffix, 0},
	{OpSToInt, "stoint", ConstraintInt, 1, ruleSuffix, 0},
	{OpUToInt, "utoint", ConstraintInt, 1, ruleSuffix, 0},
	{OpIntToS, "inttos", ConstraintFloat, 1, ruleSuffix, 0},
	{OpIntToU, "inttou", ConstraintFloat, 1, ruleSuffix, 0},
	{OpBitcast, "bitcast", ConstraintNone, 1, ruleSuffix, 0},

	// Approximate math (§11.6) — illegal under strict.
	{OpRcp, "rcp", ConstraintFloat, 1, ruleSuffix, flagBounded},
	{OpRsqrt, "rsqrt", ConstraintFloat, 1, ruleSuffix, flagBounded},
	{OpSin, "sin", ConstraintFloat, 1, ruleSuffix, flagBounded},
	{OpCos, "cos", ConstraintFloat, 1, ruleSuffix, flagBounded},
	{OpExp2, "exp2", ConstraintFloat, 1, ruleSuffix, flagBounded},
	{OpLog2, "log2", ConstraintFloat, 1, ruleSuffix, flagBounded},
	{OpTanh, "tanh", ConstraintFloat, 1, ruleSuffix, flagBounded},

	// Select and calls (§11.7). Both select arms are evaluated; select is
	// not control flow.
	{OpSelect, "select", ConstraintNone, 3, ruleSuffix, 0},
	{OpCall, "call", ConstraintNone, -1, ruleSpecial, 0},

	// Barrier (§10.1): execution scope is the suffix, memory scope the
	// lone optional operand.
	{OpBarrier, "barrier", ConstraintNone, -1, ruleVoid, flagUniform},

	// Atomics (§10.2). Scope is the final operand; there is no ordering
	// operand — cmpxchg therefore takes four, not vir's five.
	{OpAtomicLoad, "atomic_load", ConstraintIntOrPtr, 2, ruleSuffix, flagAtomic},
	{OpAtomicStore, "atomic_store", ConstraintIntOrPtr, 3, ruleVoid, flagAtomic},
	{OpAtomicAdd, "atomic_add", ConstraintIntOrFloat, 3, ruleSuffix, flagAtomic},
	{OpAtomicSub, "atomic_sub", ConstraintInt, 3, ruleSuffix, flagAtomic},
	{OpAtomicAnd, "atomic_and", ConstraintInt, 3, ruleSuffix, flagAtomic},
	{OpAtomicOr, "atomic_or", ConstraintInt, 3, ruleSuffix, flagAtomic},
	{OpAtomicXor, "atomic_xor", ConstraintInt, 3, ruleSuffix, flagAtomic},
	{OpAtomicXchg, "atomic_xchg", ConstraintIntOrPtr, 3, ruleSuffix, flagAtomic},
	{OpAtomicUMin, "atomic_umin", ConstraintInt, 3, ruleSuffix, flagAtomic},
	{OpAtomicUMax, "atomic_umax", ConstraintInt, 3, ruleSuffix, flagAtomic},
	{OpAtomicSMin, "atomic_smin", ConstraintInt, 3, ruleSuffix, flagAtomic},
	{OpAtomicSMax, "atomic_smax", ConstraintInt, 3, ruleSuffix, flagAtomic},
	// cmpxchg yields the OLD value at p, not a success flag (§10.2).
	{OpCmpxchg, "cmpxchg", ConstraintIntOrPtr, 4, ruleSuffix, flagAtomic},
	{OpFence, "fence", ConstraintNone, 2, ruleVoid, 0},

	// Subgroup collectives (§10.3). All require uniform reachability.
	{OpShuffleXor, "shuffle_xor", ConstraintNone, 2, ruleSuffix, flagUniform},
	{OpShuffleUp, "shuffle_up", ConstraintNone, 2, ruleSuffix, flagUniform},
	{OpShuffleDown, "shuffle_down", ConstraintNone, 2, ruleSuffix, flagUniform},
	{OpBroadcast, "broadcast", ConstraintNone, 2, ruleSuffix, flagUniform},
	{OpBroadcastFirst, "broadcast_first", ConstraintNone, 1, ruleSuffix, flagUniform},
	{OpAny, "any", ConstraintNone, 1, ruleBool, flagUniform},
	{OpAll, "all", ConstraintNone, 1, ruleBool, flagUniform},
	{OpBallot, "ballot", ConstraintNone, 1, ruleSubmask, flagUniform},
	// sub_add on floating-point T is not order-stable (§10.3).
	{OpSubAdd, "sub_add", ConstraintIntOrFloat, 1, ruleSuffix, flagUniform},
	{OpSubMin, "sub_min", ConstraintIntOrFloat, 1, ruleSuffix, flagUniform},
	{OpSubMax, "sub_max", ConstraintIntOrFloat, 1, ruleSuffix, flagUniform},
	{OpSubAnd, "sub_and", ConstraintInt, 1, ruleSuffix, flagUniform},
	{OpSubOr, "sub_or", ConstraintInt, 1, ruleSuffix, flagUniform},
	{OpSubXor, "sub_xor", ConstraintInt, 1, ruleSuffix, flagUniform},

	// Submask ops and lane-mask constants (§10.3).
	{OpMaskCount, "mask_count", ConstraintNone, 1, ruleI32, 0},
	{OpMaskTest, "mask_test", ConstraintNone, 2, ruleBool, 0},
	{OpMaskFirst, "mask_first", ConstraintNone, 1, ruleI32, 0},
	{OpMaskEmpty, "mask_empty", ConstraintNone, 1, ruleBool, 0},
	{OpMaskLt, "mask_lt", ConstraintNone, 0, ruleSubmask, 0},
	{OpMaskLe, "mask_le", ConstraintNone, 0, ruleSubmask, 0},
	{OpMaskGt, "mask_gt", ConstraintNone, 0, ruleSubmask, 0},
	{OpMaskGe, "mask_ge", ConstraintNone, 0, ruleSubmask, 0},
	{OpMaskEq, "mask_eq", ConstraintNone, 0, ruleSubmask, 0},

	// Execution builtins (§9). Result widths live in builtins.go.
	{OpThreadInGrid, "thread_in_grid", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpThreadInGroup, "thread_in_group", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpGroupInGrid, "group_in_grid", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpThreadInSubgroup, "thread_in_subgroup", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpSubgroupInGroup, "subgroup_in_group", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpThreadsPerGroup, "threads_per_group", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpGroupsPerGrid, "groups_per_grid", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpThreadsPerGrid, "threads_per_grid", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpThreadsPerSubgroup, "threads_per_subgroup", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpSubgroupsPerGroup, "subgroups_per_group", ConstraintNone, 0, ruleBuiltin, flagBuiltin},
	{OpDynamicGroupSize, "dynamic_group_size", ConstraintNone, 0, ruleBuiltin, flagBuiltin},

	{OpLoc, "loc", ConstraintNone, -1, ruleVoid, 0},
}

var (
	opMetaTable [opcodeCount]opMeta
	opNameTable [opcodeCount]string
	opByName    map[string]Opcode
)

func init() {
	opByName = make(map[string]Opcode, len(opTable))
	seen := make([]bool, opcodeCount)
	for _, d := range opTable {
		if d.op <= OpInvalid || int(d.op) >= int(opcodeCount) {
			panic(fmt.Sprintf("gvir: opTable entry %q has out-of-range opcode %d", d.name, d.op))
		}
		if seen[d.op] {
			panic(fmt.Sprintf("gvir: opcode %d registered twice in opTable (duplicate %q)", d.op, d.name))
		}
		if d.name == "" {
			panic(fmt.Sprintf("gvir: opcode %d has an empty name in opTable", d.op))
		}
		if _, dup := opByName[d.name]; dup {
			panic(fmt.Sprintf("gvir: opcode name %q registered twice in opTable", d.name))
		}
		seen[d.op] = true
		opNameTable[d.op] = d.name
		opMetaTable[d.op] = opMeta{numeric: d.numeric, arity: d.arity, result: d.result, flags: d.flags}
		opByName[d.name] = d.op
	}
	for i := 1; i < int(opcodeCount); i++ {
		if !seen[i] {
			panic(fmt.Sprintf("gvir: Opcode constant %d has no opTable entry — every opcode must be registered (see opcode.go)", i))
		}
	}
}

// String returns the canonical textual spelling of o.
func (o Opcode) String() string {
	if o == OpInvalid {
		return "<invalid opcode>"
	}
	if int(o) > 0 && int(o) < len(opNameTable) && opNameTable[o] != "" {
		return opNameTable[o]
	}
	return fmt.Sprintf("<opcode %d>", int(o))
}

// ParseOpcode resolves a mnemonic to its Opcode. Returns false for anything
// outside the closed vocabulary — including terminator keywords, which are
// separate Go types.
func ParseOpcode(s string) (Opcode, bool) {
	op, ok := opByName[s]
	return op, ok
}

func (o Opcode) meta() (opMeta, bool) {
	if int(o) <= 0 || int(o) >= len(opMetaTable) || opNameTable[o] == "" {
		return opMeta{}, false
	}
	return opMetaTable[o], true
}

// Arity returns the operand count the grammar pins for o, or -1 when it is
// not pinned (call, loc, barrier, the overloaded shuffle).
func (o Opcode) Arity() int {
	m, ok := o.meta()
	if !ok {
		return -1
	}
	return m.arity
}

func (o Opcode) has(f opFlags) bool {
	m, ok := o.meta()
	return ok && m.flags&f != 0
}

// RequiresBoundedProfile reports whether o is an approximate-math opcode,
// legal only under float_profile bounded (§11.6).
func (o Opcode) RequiresBoundedProfile() bool { return o.has(flagBounded) }

// IsBuiltin reports whether o is a §9 execution builtin.
func (o Opcode) IsBuiltin() bool { return o.has(flagBuiltin) }

// RequiresUniformReach reports whether every thread in o's execution scope
// must reach it; non-uniform arrival is UB (§12.8). True for barrier and
// every subgroup collective (§10.1, §10.3).
func (o Opcode) RequiresUniformReach() bool { return o.has(flagUniform) }

// IsAtomic reports whether o carries a trailing scope operand and no
// ordering operand (§10.2).
func (o Opcode) IsAtomic() bool { return o.has(flagAtomic) }

// LegalInProfile reports whether o may appear under profile p.
func (o Opcode) LegalInProfile(p FloatProfile) bool {
	return !o.RequiresBoundedProfile() || p.Effective() == ProfileBounded
}

// ResultType derives an instruction's result type where the opcode's rule
// allows it. env resolves an ident operand's already-bound type and may be
// nil; ok is false when the rule needs a typing environment that was not
// supplied (index.ptr / field.ptr) or needs full operand typing that only
// ir/verify has (call, extract, the overloaded shuffle).
func (i *Instruction) ResultType(env func(name string) (Type, bool)) (Type, bool) {
	m, ok := i.Op.meta()
	if !ok {
		return nil, false
	}
	switch m.result {
	case ruleVoid:
		return Void, true
	case ruleSuffix:
		if i.Suffix == nil {
			return nil, false
		}
		return i.Suffix, true
	case ruleBool:
		if v, isVec := i.Suffix.(VecType); isVec {
			return Vec(I1, v.Len), true
		}
		return I1, true
	case ruleSubmask:
		return Submask, true
	case ruleI32:
		return I32, true
	case rulePrivatePtr:
		return PtrPrivate, true
	case rulePtrOfOperand:
		if env == nil || len(i.Args) == 0 || i.Args[0].Kind != OperandIdent {
			return nil, false
		}
		t, found := env(i.Args[0].Ident)
		if !found || !IsPtr(t) {
			return nil, false
		}
		return t, true
	case ruleBuiltin:
		return BuiltinResultType(i.Op, i.Dim)
	}
	return nil, false // ruleSpecial
}

// checkNumericConstraint enforces an opcode's registered element-type
// constraint against a single instruction's suffix.
func checkNumericConstraint(op Opcode, suffix Type, c operandConstraint) error {
	if c == ConstraintNone {
		return nil
	}
	elem := ElemOrSelf(suffix)
	var ok bool
	switch c {
	case ConstraintInt:
		ok = IsInt(elem)
	case ConstraintFloat:
		ok = IsFloat(elem)
	case ConstraintIntOrFloat:
		ok = IsInt(elem) || IsFloat(elem)
	case ConstraintIntOrPtr:
		ok = IsInt(elem) || IsPtr(suffix)
	}
	if !ok {
		return fmt.Errorf("%s legal only on %s", op, constraintDescription(c))
	}
	return nil
}

// CheckSuffix is the exported form used by ir/verify.
func CheckSuffix(op Opcode, suffix Type) error {
	m, ok := op.meta()
	if !ok {
		return fmt.Errorf("unknown opcode %d", op)
	}
	return checkNumericConstraint(op, suffix, m.numeric)
}

func constraintDescription(c operandConstraint) string {
	switch c {
	case ConstraintInt:
		return "iN / vec[iN, N]"
	case ConstraintFloat:
		return "fN / vec[fN, N]"
	case ConstraintIntOrFloat:
		return "iN or fN (incl. vector forms)"
	case ConstraintIntOrPtr:
		return "iN / vec[iN, N] or ptr"
	}
	return "a compatible type"
}