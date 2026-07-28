// opcode.go
package gvir

import "fmt"

// Opcode identifies a core Vertex GPU IR mnemonic (§8-§11). It replaces bare
// strings so a typo is a compile error, and every opcode's suffix shape,
// arity, operand constraint, result rule and behavioural flags are
// registered exactly once in opTable below — init() panics at package load
// if any constant is missing an entry.
//
// The vocabulary is closed by construction: §1 promises an opcode means the
// same thing on all three backends or it is not in the IR, so there is no
// per-dialect extension mechanism and nothing here is open-ended.
type Opcode uint16

const (
	OpInvalid Opcode = iota // zero value; never a valid instruction opcode

	// Integer arithmetic (§11.1). add/sub/mul/neg/abs are shared with §11.3.
	OpAdd
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

	// Bitwise and shifts (§11.2).
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

	// Float (§11.3).
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

	// Integer and pointer comparisons (§11.4).
	OpEq
	OpNe
	OpUlt
	OpUle
	OpUgt
	OpUge
	OpSlt
	OpSle
	OpSgt
	OpSge

	// Float comparisons (§11.4) — ordered only, plus ord/unord.
	OpOeq
	OpOne
	OpOlt
	OpOle
	OpOgt
	OpOge
	OpOrd
	OpUnord

	// Conversions (§11.5) — suffix is the destination type.
	OpTrunc
	OpSext
	OpZext
	OpFPTrunc
	OpFPExt
	OpStoint
	OpUtoint
	OpInttos
	OpInttou
	OpBitcast

	// Approximate opcodes (§11.6) — require float_profile approx.
	OpRcp
	OpRsqrt
	OpSin
	OpCos
	OpExp2
	OpLog2
	OpTanh

	// Memory (§8.1, §8.3).
	OpAlloca
	OpLoad
	OpStore
	OpMemcopy
	OpMemmove
	OpMemset
	OpIndex
	OpField

	// Vectors (§8.3, §4.4).
	OpExtract
	OpInsert
	OpSplat
	OpSwizzle

	// Synchronization and atomics (§10.1, §10.2).
	OpBarrier
	OpFence
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

	// Subgroup collectives (§10.3).
	OpShuffle
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

	// submask operations and constants (§10.3).
	OpMaskCount
	OpMaskTest
	OpMaskFirst
	OpMaskEmpty
	OpMaskLt
	OpMaskLe
	OpMaskGt
	OpMaskGe
	OpMaskEq

	// Execution builtins (§9).
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

	// Select and calls (§11.7).
	OpSelect
	OpCall

	// Debug (§2 loc-line).
	OpLoc

	opcodeCount // sentinel: total defined opcodes; must stay last
)

// operandConstraint restricts which element type an opcode's type suffix may
// name. Checked against ElemOrSelf(suffix).
type operandConstraint uint8

const (
	ConstraintNone operandConstraint = iota
	ConstraintInt                    // i8..i64 (i1 excluded) / vec thereof
	ConstraintFloat
	ConstraintIntOrFloat
	ConstraintIntOrPred // adds i1 and vec[i1,N] (§4.5 and/or/xor/not)
	ConstraintIntOrPtr  // adds the bare `ptr` suffix word (§11.4)
)

// suffixKind says which suffix channel an opcode uses. Exactly one channel
// per opcode; init() and CheckSuffix keep Instruction honest about it.
type suffixKind uint8

const (
	suffixNone          suffixKind = iota // no suffix at all
	suffixType                            // op "." type
	suffixTypeOrPtrWord                   // type, or the bare word `ptr` (§11.4)
	suffixPtrWord                         // always the bare word `ptr` (§8.3)
	suffixDim                             // optional .x/.y/.z (§9)
	suffixExec                            // .subgroup / .group (§10.1)
)

// resultRule says how an instruction's result type is derived.
type resultRule uint8

const (
	ruleSuffix      resultRule = iota // result type == Suffix
	ruleVoid                          // op never produces a value
	ruleBool                          // i1, or vec[i1,N] when Suffix is a vector
	ruleElem                          // element type of a vector Suffix (extract)
	ruleI32                           // §9 fixed-width builtins, mask_count/mask_first
	ruleI64                           // unsuffixed positional builtins that reject .x/.y/.z
	ruleDim                           // i32 with a dim suffix, i64 without (§9)
	ruleSubmask                       // ballot and the mask_* constants
	rulePrivatePtr                    // alloca yields ptr[private] (§8.1)
	ruleSpecial                       // computed by ir/verify: call, index, field
)

type opFlags uint8

const (
	flagBuiltin    opFlags = 1 << iota // §9 execution builtin; takes no operands
	flagApprox                         // requires float_profile approx (§11.6)
	flagCollective                     // §10.3 collective: subgroup-uniform reachability required
	flagAtomic                         // final operand is a §10.2 scope
	flagAlignable                      // may carry an `align N` clause (§8.1, §8.3)
)

type opDef struct {
	op      Opcode
	name    string
	numeric operandConstraint
	suffix  suffixKind
	arity   int // -1 == not pinned by the grammar text; checked in ir/verify
	result  resultRule
	flags   opFlags
}

type opMeta struct {
	numeric operandConstraint
	suffix  suffixKind
	arity   int
	result  resultRule
	flags   opFlags
}

// opTable is the single source of truth for every opcode. Every Opcode
// constant above must appear here exactly once; init() enforces it.
var opTable = []opDef{
	// --- Integer / shared arithmetic (§11.1) ------------------------------
	{OpAdd, "add", ConstraintIntOrFloat, suffixType, 2, ruleSuffix, 0},
	{OpSub, "sub", ConstraintIntOrFloat, suffixType, 2, ruleSuffix, 0},
	{OpMul, "mul", ConstraintIntOrFloat, suffixType, 2, ruleSuffix, 0},
	{OpNeg, "neg", ConstraintIntOrFloat, suffixType, 1, ruleSuffix, 0},
	{OpAbs, "abs", ConstraintIntOrFloat, suffixType, 1, ruleSuffix, 0},
	{OpUDiv, "udiv", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpSDiv, "sdiv", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpURem, "urem", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpSRem, "srem", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpUMulH, "umulh", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpSMulH, "smulh", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpUMin, "umin", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpUMax, "umax", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpSMin, "smin", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpSMax, "smax", ConstraintInt, suffixType, 2, ruleSuffix, 0},

	// --- Bitwise and shifts (§11.2) ---------------------------------------
	// and/or/xor/not additionally accept i1 and vec[i1,N] (§4.5).
	{OpAnd, "and", ConstraintIntOrPred, suffixType, 2, ruleSuffix, 0},
	{OpOr, "or", ConstraintIntOrPred, suffixType, 2, ruleSuffix, 0},
	{OpXor, "xor", ConstraintIntOrPred, suffixType, 2, ruleSuffix, 0},
	{OpNot, "not", ConstraintIntOrPred, suffixType, 1, ruleSuffix, 0},
	{OpShl, "shl", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpLShr, "lshr", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpAShr, "ashr", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpRotl, "rotl", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpRotr, "rotr", ConstraintInt, suffixType, 2, ruleSuffix, 0},
	{OpCtlz, "ctlz", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpCttz, "cttz", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpPopcnt, "popcnt", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpBrev, "brev", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpBSwap, "bswap", ConstraintInt, suffixType, 1, ruleSuffix, 0},

	// --- Float (§11.3) -----------------------------------------------------
	{OpDiv, "div", ConstraintFloat, suffixType, 2, ruleSuffix, 0},
	{OpSqrt, "sqrt", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpFma, "fma", ConstraintFloat, suffixType, 3, ruleSuffix, 0},
	{OpMin, "min", ConstraintFloat, suffixType, 2, ruleSuffix, 0},
	{OpMax, "max", ConstraintFloat, suffixType, 2, ruleSuffix, 0},
	{OpFloor, "floor", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpCeil, "ceil", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpRound, "round", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpRoundEven, "round_even", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpTruncF, "trunc_f", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpCopysign, "copysign", ConstraintFloat, suffixType, 2, ruleSuffix, 0},
	{OpIsNaN, "isnan", ConstraintFloat, suffixType, 1, ruleBool, 0},
	{OpIsInf, "isinf", ConstraintFloat, suffixType, 1, ruleBool, 0},

	// --- Comparisons (§11.4) ----------------------------------------------
	// The unsigned family doubles as the pointer family (eq.ptr, ult.ptr,
	// ...); same-address-space is a two-operand check in ir/verify.
	{OpEq, "eq", ConstraintIntOrPtr, suffixTypeOrPtrWord, 2, ruleBool, 0},
	{OpNe, "ne", ConstraintIntOrPtr, suffixTypeOrPtrWord, 2, ruleBool, 0},
	{OpUlt, "ult", ConstraintIntOrPtr, suffixTypeOrPtrWord, 2, ruleBool, 0},
	{OpUle, "ule", ConstraintIntOrPtr, suffixTypeOrPtrWord, 2, ruleBool, 0},
	{OpUgt, "ugt", ConstraintIntOrPtr, suffixTypeOrPtrWord, 2, ruleBool, 0},
	{OpUge, "uge", ConstraintIntOrPtr, suffixTypeOrPtrWord, 2, ruleBool, 0},
	{OpSlt, "slt", ConstraintInt, suffixType, 2, ruleBool, 0},
	{OpSle, "sle", ConstraintInt, suffixType, 2, ruleBool, 0},
	{OpSgt, "sgt", ConstraintInt, suffixType, 2, ruleBool, 0},
	{OpSge, "sge", ConstraintInt, suffixType, 2, ruleBool, 0},

	{OpOeq, "oeq", ConstraintFloat, suffixType, 2, ruleBool, 0},
	{OpOne, "one", ConstraintFloat, suffixType, 2, ruleBool, 0},
	{OpOlt, "olt", ConstraintFloat, suffixType, 2, ruleBool, 0},
	{OpOle, "ole", ConstraintFloat, suffixType, 2, ruleBool, 0},
	{OpOgt, "ogt", ConstraintFloat, suffixType, 2, ruleBool, 0},
	{OpOge, "oge", ConstraintFloat, suffixType, 2, ruleBool, 0},
	{OpOrd, "ord", ConstraintFloat, suffixType, 2, ruleBool, 0},
	{OpUnord, "unord", ConstraintFloat, suffixType, 2, ruleBool, 0},

	// --- Conversions (§11.5) ----------------------------------------------
	{OpTrunc, "trunc", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpSext, "sext", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpZext, "zext", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpFPTrunc, "fptrunc", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpFPExt, "fpext", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpStoint, "stoint", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpUtoint, "utoint", ConstraintInt, suffixType, 1, ruleSuffix, 0},
	{OpInttos, "inttos", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	{OpInttou, "inttou", ConstraintFloat, suffixType, 1, ruleSuffix, 0},
	// bitcast's legality is a width-and-space pair rule, not an element-type
	// rule: IsBitcastType plus the §11.5 equal-width check in ir/verify.
	{OpBitcast, "bitcast", ConstraintNone, suffixType, 1, ruleSuffix, 0},

	// --- Approximate opcodes (§11.6) --------------------------------------
	{OpRcp, "rcp", ConstraintFloat, suffixType, 1, ruleSuffix, flagApprox},
	{OpRsqrt, "rsqrt", ConstraintFloat, suffixType, 1, ruleSuffix, flagApprox},
	{OpSin, "sin", ConstraintFloat, suffixType, 1, ruleSuffix, flagApprox},
	{OpCos, "cos", ConstraintFloat, suffixType, 1, ruleSuffix, flagApprox},
	{OpExp2, "exp2", ConstraintFloat, suffixType, 1, ruleSuffix, flagApprox},
	{OpLog2, "log2", ConstraintFloat, suffixType, 1, ruleSuffix, flagApprox},
	{OpTanh, "tanh", ConstraintFloat, suffixType, 1, ruleSuffix, flagApprox},

	// --- Memory (§8.1, §8.3) ----------------------------------------------
	// alloca takes no operand: its type is statically sized (§8.1), unlike
	// the host IR's sized alloca.
	{OpAlloca, "alloca", ConstraintNone, suffixType, 0, rulePrivatePtr, flagAlignable},
	{OpLoad, "load", ConstraintNone, suffixType, 1, ruleSuffix, flagAlignable},
	// store is destination-first (§8.3): store.<T> ptr, value.
	{OpStore, "store", ConstraintNone, suffixType, 2, ruleVoid, flagAlignable},
	{OpMemcopy, "memcopy", ConstraintNone, suffixNone, 3, ruleVoid, 0},
	{OpMemmove, "memmove", ConstraintNone, suffixNone, 3, ruleVoid, 0},
	{OpMemset, "memset", ConstraintNone, suffixNone, 3, ruleVoid, 0},
	// index/field keep the operand's address space, so their result is
	// computed in ir/verify rather than read off the suffix.
	{OpIndex, "index", ConstraintNone, suffixPtrWord, 2, ruleSpecial, 0},
	{OpField, "field", ConstraintNone, suffixPtrWord, 2, ruleSpecial, 0},

	// --- Vectors (§8.3) ----------------------------------------------------
	// For all four, the suffix names the *vector* type; extract's result is
	// therefore its element type. k = 3 on a width-3 vector is a
	// verification error (§4.4), checked in ir/verify.
	{OpExtract, "extract", ConstraintNone, suffixType, 2, ruleElem, 0},
	{OpInsert, "insert", ConstraintNone, suffixType, 3, ruleSuffix, 0},
	{OpSplat, "splat", ConstraintNone, suffixType, 1, ruleSuffix, 0},
	{OpSwizzle, "swizzle", ConstraintNone, suffixType, -1, ruleSuffix, 0},

	// --- Synchronization and atomics (§10.1, §10.2) ------------------------
	// barrier's execution scope is the suffix; the optional memory scope is
	// its one operand, hence an unpinned arity.
	{OpBarrier, "barrier", ConstraintNone, suffixExec, -1, ruleVoid, 0},
	{OpFence, "fence", ConstraintNone, suffixNone, 2, ruleVoid, 0},
	// Atomics carry no operandConstraint: "i32, i64, ptr" is not an element
	// class, so IsAtomicType / IsAtomicAddType do that work in ir/verify.
	{OpAtomicLoad, "atomic_load", ConstraintNone, suffixType, 2, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicStore, "atomic_store", ConstraintNone, suffixType, 3, ruleVoid, flagAtomic | flagAlignable},
	{OpAtomicAdd, "atomic_add", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicSub, "atomic_sub", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicAnd, "atomic_and", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicOr, "atomic_or", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicXor, "atomic_xor", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicXchg, "atomic_xchg", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicUMin, "atomic_umin", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicUMax, "atomic_umax", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicSMin, "atomic_smin", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	{OpAtomicSMax, "atomic_smax", ConstraintNone, suffixType, 3, ruleSuffix, flagAtomic | flagAlignable},
	// cmpxchg yields the old value, not a success flag (§10.2).
	{OpCmpxchg, "cmpxchg", ConstraintNone, suffixType, 4, ruleSuffix, flagAtomic | flagAlignable},

	// --- Subgroup collectives (§10.3) --------------------------------------
	{OpShuffle, "shuffle", ConstraintNone, suffixType, 2, ruleSuffix, flagCollective},
	{OpShuffleXor, "shuffle_xor", ConstraintNone, suffixType, 2, ruleSuffix, flagCollective},
	{OpShuffleUp, "shuffle_up", ConstraintNone, suffixType, 2, ruleSuffix, flagCollective},
	{OpShuffleDown, "shuffle_down", ConstraintNone, suffixType, 2, ruleSuffix, flagCollective},
	{OpBroadcast, "broadcast", ConstraintNone, suffixType, 2, ruleSuffix, flagCollective},
	{OpBroadcastFirst, "broadcast_first", ConstraintNone, suffixType, 1, ruleSuffix, flagCollective},
	{OpAny, "any", ConstraintNone, suffixNone, 1, ruleBool, flagCollective},
	{OpAll, "all", ConstraintNone, suffixNone, 1, ruleBool, flagCollective},
	{OpBallot, "ballot", ConstraintNone, suffixNone, 1, ruleSubmask, flagCollective},
	// sub_min/sub_max take the §11.3 float reading on floats; the spec does
	// not spell a signed/unsigned split for their integer forms, so the
	// signedness question is left to ir/verify rather than invented here.
	{OpSubAdd, "sub_add", ConstraintIntOrFloat, suffixType, 1, ruleSuffix, flagCollective},
	{OpSubMin, "sub_min", ConstraintIntOrFloat, suffixType, 1, ruleSuffix, flagCollective},
	{OpSubMax, "sub_max", ConstraintIntOrFloat, suffixType, 1, ruleSuffix, flagCollective},
	{OpSubAnd, "sub_and", ConstraintInt, suffixType, 1, ruleSuffix, flagCollective},
	{OpSubOr, "sub_or", ConstraintInt, suffixType, 1, ruleSuffix, flagCollective},
	{OpSubXor, "sub_xor", ConstraintInt, suffixType, 1, ruleSuffix, flagCollective},

	// submask ops read a mask already in hand rather than other lanes, so
	// they are not flagged collective; ballot, which produces one, is.
	{OpMaskCount, "mask_count", ConstraintNone, suffixNone, 1, ruleI32, 0},
	{OpMaskTest, "mask_test", ConstraintNone, suffixNone, 2, ruleBool, 0},
	{OpMaskFirst, "mask_first", ConstraintNone, suffixNone, 1, ruleI32, 0},
	{OpMaskEmpty, "mask_empty", ConstraintNone, suffixNone, 1, ruleBool, 0},
	{OpMaskLt, "mask_lt", ConstraintNone, suffixNone, 0, ruleSubmask, 0},
	{OpMaskLe, "mask_le", ConstraintNone, suffixNone, 0, ruleSubmask, 0},
	{OpMaskGt, "mask_gt", ConstraintNone, suffixNone, 0, ruleSubmask, 0},
	{OpMaskGe, "mask_ge", ConstraintNone, suffixNone, 0, ruleSubmask, 0},
	{OpMaskEq, "mask_eq", ConstraintNone, suffixNone, 0, ruleSubmask, 0},

	// --- Execution builtins (§9) -------------------------------------------
	// Dimension-suffixed forms are i32; the unsuffixed linearized forms are
	// i64 (§9 "Result widths").
	{OpThreadInGrid, "thread_in_grid", ConstraintNone, suffixDim, 0, ruleDim, flagBuiltin},
	{OpThreadInGroup, "thread_in_group", ConstraintNone, suffixDim, 0, ruleDim, flagBuiltin},
	{OpGroupInGrid, "group_in_grid", ConstraintNone, suffixDim, 0, ruleDim, flagBuiltin},
	{OpThreadsPerGroup, "threads_per_group", ConstraintNone, suffixDim, 0, ruleDim, flagBuiltin},
	{OpGroupsPerGrid, "groups_per_grid", ConstraintNone, suffixDim, 0, ruleDim, flagBuiltin},
	{OpThreadsPerGrid, "threads_per_grid", ConstraintNone, suffixDim, 0, ruleDim, flagBuiltin},
	// These five reject every dimension suffix (§9.1).
	{OpThreadInSubgroup, "thread_in_subgroup", ConstraintNone, suffixNone, 0, ruleI64, flagBuiltin},
	{OpSubgroupInGroup, "subgroup_in_group", ConstraintNone, suffixNone, 0, ruleI64, flagBuiltin},
	{OpThreadsPerSubgroup, "threads_per_subgroup", ConstraintNone, suffixNone, 0, ruleI32, flagBuiltin},
	{OpSubgroupsPerGroup, "subgroups_per_group", ConstraintNone, suffixNone, 0, ruleI32, flagBuiltin},
	{OpDynamicGroupSize, "dynamic_group_size", ConstraintNone, suffixNone, 0, ruleI32, flagBuiltin},

	// --- Select, calls, debug (§11.7, §2) -----------------------------------
	// select's condition is i1 or vec[i1,N] depending on T; both arms are
	// always evaluated (§11.7).
	{OpSelect, "select", ConstraintNone, suffixType, 3, ruleSuffix, 0},
	// call's first operand is the callee ident; the result type comes from
	// the callee's declared return type, resolved in ir/verify.
	{OpCall, "call", ConstraintNone, suffixNone, -1, ruleSpecial, 0},
	{OpLoc, "loc", ConstraintNone, suffixNone, -1, ruleVoid, 0},
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
		if d.suffix == suffixDim && d.flags&flagBuiltin == 0 {
			panic(fmt.Sprintf("gvir: %q takes a dimension suffix but is not a builtin", d.name))
		}
		if d.flags&flagBuiltin != 0 && d.arity != 0 {
			panic(fmt.Sprintf("gvir: builtin %q must take no operands (§9)", d.name))
		}
		if d.flags&flagApprox != 0 && d.numeric != ConstraintFloat {
			panic(fmt.Sprintf("gvir: approximate opcode %q must be float-constrained (§11.6)", d.name))
		}
		seen[d.op] = true
		opNameTable[d.op] = d.name
		opMetaTable[d.op] = opMeta{
			numeric: d.numeric, suffix: d.suffix,
			arity: d.arity, result: d.result, flags: d.flags,
		}
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
// outside the closed vocabulary — including the terminator keywords, which
// are separate Go types (module.go).
func ParseOpcode(s string) (Opcode, bool) {
	op, ok := opByName[s]
	return op, ok
}

// meta looks up an opcode's registered metadata. ok is false only for
// OpInvalid or an out-of-range value.
func (o Opcode) meta() (opMeta, bool) {
	if int(o) <= 0 || int(o) >= len(opMetaTable) || opNameTable[o] == "" {
		return opMeta{}, false
	}
	return opMetaTable[o], true
}

// Arity returns the operand count the grammar pins for o, or -1 when the
// count is variable and checked in ir/verify.
func (o Opcode) Arity() int {
	m, _ := o.meta()
	return m.arity
}

func (o Opcode) IsBuiltin() bool      { m, _ := o.meta(); return m.flags&flagBuiltin != 0 }
func (o Opcode) RequiresApprox() bool { m, _ := o.meta(); return m.flags&flagApprox != 0 }
func (o Opcode) IsCollective() bool   { m, _ := o.meta(); return m.flags&flagCollective != 0 }
func (o Opcode) IsAtomic() bool       { m, _ := o.meta(); return m.flags&flagAtomic != 0 }
func (o Opcode) AcceptsAlign() bool   { m, _ := o.meta(); return m.flags&flagAlignable != 0 }

// AcceptsDim reports whether o may carry a .x/.y/.z suffix (§9.1).
func (o Opcode) AcceptsDim() bool { m, _ := o.meta(); return m.suffix == suffixDim }

// TakesTypeSuffix reports whether o is spelled with a type suffix.
func (o Opcode) TakesTypeSuffix() bool {
	m, _ := o.meta()
	return m.suffix == suffixType || m.suffix == suffixTypeOrPtrWord
}

// HasResult reports whether o produces a value binding.
func (o Opcode) HasResult() bool { m, _ := o.meta(); return m.result != ruleVoid }

// ResultType derives the type of o's result from its suffix and dimension
// suffix. ok is false for the three opcodes whose result depends on operands
// rather than spelling — call, index and field — which ir/verify computes,
// and for a missing suffix where one is required.
func (o Opcode) ResultType(suffix Type, dim Dim) (Type, bool) {
	m, ok := o.meta()
	if !ok {
		return nil, false
	}
	switch m.result {
	case ruleSuffix:
		if suffix == nil {
			return nil, false
		}
		return suffix, true
	case ruleVoid:
		return Void, true
	case ruleBool:
		if v, isVec := suffix.(VecType); isVec {
			return VecType{Elem: I1, Len: v.Len}, true
		}
		return I1, true
	case ruleElem:
		if suffix == nil {
			return nil, false
		}
		return ElemOrSelf(suffix), true
	case ruleI32:
		return I32, true
	case ruleI64:
		return I64, true
	case ruleDim:
		if dim != DimNone {
			return I32, true
		}
		return I64, true
	case ruleSubmask:
		return Submask, true
	case rulePrivatePtr:
		return PtrPrivate, true
	}
	return nil, false
}

// CheckSuffix enforces the suffix shape and element-type constraint an
// opcode registers. It is the exported entry point ir/verify uses so the
// table stays the only place these rules are written down.
func (o Opcode) CheckSuffix(suffix Type) error {
	m, ok := o.meta()
	if !ok {
		return fmt.Errorf("gvir: %s is not a registered opcode", o)
	}
	switch m.suffix {
	case suffixNone, suffixDim, suffixExec:
		if suffix != nil {
			return fmt.Errorf("%s takes no type suffix", o)
		}
		return nil
	case suffixPtrWord:
		if !IsPtrWord(suffix) {
			return fmt.Errorf("%s is spelled %s.ptr — the address space comes from its operand (§8.3)", o, o)
		}
		return nil
	}
	if suffix == nil {
		return fmt.Errorf("%s requires a type suffix", o)
	}
	if IsPtrWord(suffix) && m.suffix != suffixTypeOrPtrWord {
		return fmt.Errorf("%s does not accept the bare `ptr` suffix", o)
	}
	if IsSpacedPtr(suffix) && m.suffix == suffixTypeOrPtrWord {
		return fmt.Errorf("%s is spelled %s.ptr, not %s.%s (§11.4)", o, o, o, suffix)
	}
	if !constraintAllows(m.numeric, suffix) {
		return fmt.Errorf("%s legal only on %s (§11)", o, constraintDescription(m.numeric))
	}
	return nil
}

func constraintAllows(c operandConstraint, suffix Type) bool {
	elem := ElemOrSelf(suffix)
	switch c {
	case ConstraintNone:
		return true
	case ConstraintInt:
		return IsSInt(elem)
	case ConstraintFloat:
		return IsFloat(elem)
	case ConstraintIntOrFloat:
		return IsSInt(elem) || IsFloat(elem)
	case ConstraintIntOrPred:
		return IsSInt(elem) || IsBool(elem)
	case ConstraintIntOrPtr:
		return IsSInt(elem) || IsPtrWord(suffix)
	}
	return false
}

func constraintDescription(c operandConstraint) string {
	switch c {
	case ConstraintInt:
		return "iN / vec[iN,N], i1 excluded"
	case ConstraintFloat:
		return "fN / vec[fN,N]"
	case ConstraintIntOrFloat:
		return "iN or fN (incl. vector forms)"
	case ConstraintIntOrPred:
		return "iN / i1 / vec[iN,N] / vec[i1,N]"
	case ConstraintIntOrPtr:
		return "iN / vec[iN,N] or the bare ptr suffix"
	}
	return "a compatible type"
}