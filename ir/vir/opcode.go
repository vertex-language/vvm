// opcode.go
package vir

import "fmt"

// Opcode identifies a core Vertex IR instruction mnemonic (§4). It replaces
// bare strings so that (a) a typo is a compile error instead of a silent
// pass-through, and (b) every opcode's arity/operand-constraint/result-rule
// is registered exactly once in opTable below — init() panics at package
// load if any constant in the block below is missing an entry, so a new
// opcode can never quietly skip verification the way ctlz/cttz/popcnt did
// when they were absent from a hand-maintained string set.
//
// Strings are still used elsewhere in this package, deliberately: struct/
// field/label/link/fn names are open-ended user identifiers, and asm
// mnemonics/registers are per-dialect *data* (§4 "ships as predefined
// data, requiring no underlying grammar changes") — an open, extensible
// vocabulary, not a fixed instruction set the verifier reasons about.
// Opcode is for the closed, spec-fixed vocabulary of §4; that distinction
// is the point.
type Opcode uint16

const (
	OpInvalid Opcode = iota // zero value; never a valid instruction opcode

	// Arithmetic (§4 Math).
	OpAdd
	OpSub
	OpMul
	OpUDiv
	OpSDiv
	OpURem
	OpSRem
	OpNeg
	OpAbs
	OpSqrt

	// Overflow predicates (§4).
	OpUAddO
	OpSAddO
	OpUSubO
	OpSSubO
	OpUMulO
	OpSMulO

	// Widening multiply (§4).
	OpUMulH
	OpSMulH

	// Saturating add/sub (§4).
	OpUAddSat
	OpSAddSat
	OpUSubSat
	OpSSubSat

	// Bitwise (§4 Bits) — integer only.
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

	// Bare float min/max (§4 Float Semantics) — rejected on integers (§9.17);
	// use OpSMin/OpSMax/OpUMin/OpUMax there instead.
	OpMin
	OpMax

	// Comparisons (§4) — return i1 / vec[i1,N].
	OpEq
	OpNe
	OpSlt
	OpSgt
	OpSle
	OpSge
	OpUlt
	OpUgt
	OpUle
	OpUge
	OpLt
	OpGt
	OpLe
	OpGe

	// Selection (§4).
	OpSelect

	// Memory & addresses (§4).
	OpAlloca
	OpLoad
	OpStore
	OpLoadVol
	OpStoreVol
	OpMemcopy
	OpMemmove
	OpMemset
	OpField
	OpIndex

	// Atomics (§4).
	OpAtomicLoad
	OpAtomicStore
	OpAtomicAdd
	OpAtomicSub
	OpAtomicAnd
	OpAtomicOr
	OpAtomicXor
	OpAtomicXchg
	OpCmpxchg
	OpFence

	// Conversions (§4) — suffix is destination type.
	OpTrunc
	OpSext
	OpZext
	OpFdemote
	OpFpromote
	OpBitcast
	OpSfromint
	OpUfromint
	OpStoint
	OpUtoint
	OpStointSat
	OpUtointSat

	// Vectors (§4).
	OpSplat
	OpExtract
	OpInsert
	OpShuffle
	OpMaskedLoad
	OpMaskedStore
	OpGather
	OpScatter

	// Intrinsics (§4) — must compile to 1-2 CPU instructions, no libcalls.
	OpFma
	OpCopysign
	OpFloor
	OpCeil
	OpTruncF
	OpNearest
	OpSMin
	OpSMax
	OpUMin
	OpUMax
	OpBSwap
	OpBitrev

	// Reductions (§4).
	OpReduceAdd
	OpReduceMin
	OpReduceMax
	OpReduceAnd
	OpReduceOr
	OpReduceXor

	// Hints (§4).
	OpPrefetch

	// Calls & control (§4).
	OpCall
	OpSyscall

	// Debug (§1.3 rule 8).
	OpLoc

	opcodeCount // sentinel: total defined opcodes; must stay last
)

// operandConstraint restricts which element type (§9.18) an opcode's type
// suffix may name. Checked against ElemOrSelf(suffix), so it applies
// uniformly to scalar and vector forms.
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
	ruleSuffix  resultRule = iota // result type == Suffix, once Suffix is validated present
	ruleVoid                      // op never produces a value
	ruleBool                      // i1, or vec[i1,N] when Suffix is a vector
	ruleSpecial                   // computed by dedicated code in resultType (call, syscall, extract,
	// reduce_*, alloca, bare min/max) — never reached generically; every
	// ruleSpecial opcode has an explicit case in resultType's switch.
)

type opMeta struct {
	numeric operandConstraint
	arity   int // -1 == not pinned by the grammar text; checked elsewhere or left permissive
	result  resultRule
}

type opDef struct {
	op      Opcode
	name    string
	numeric operandConstraint
	arity   int
	result  resultRule
}

// opTable is the single source of truth for every core opcode (§4): its
// textual spelling, the §9.18 operand-type constraint, its operand count
// where the spec text pins one exactly (-1 where it doesn't — see the
// inline notes), and how its result type is computed. Every Opcode
// constant above must appear here exactly once; init() enforces that.
var opTable = []opDef{
	// Arithmetic. add/sub/mul/neg/abs are legal on both iN and fN — the
	// spec's Math list mixes signed/unsigned-tagged int ops with the
	// generic arithmetic mnemonics that apply to floats too; ptr is
	// excluded (address arithmetic goes through index.ptr, §4).
	{OpAdd, "add", ConstraintIntOrFloat, 2, ruleSuffix},
	{OpSub, "sub", ConstraintIntOrFloat, 2, ruleSuffix},
	{OpMul, "mul", ConstraintIntOrFloat, 2, ruleSuffix},
	{OpUDiv, "udiv", ConstraintInt, 2, ruleSuffix},
	{OpSDiv, "sdiv", ConstraintInt, 2, ruleSuffix},
	{OpURem, "urem", ConstraintInt, 2, ruleSuffix},
	{OpSRem, "srem", ConstraintInt, 2, ruleSuffix},
	{OpNeg, "neg", ConstraintIntOrFloat, 1, ruleSuffix},
	{OpAbs, "abs", ConstraintIntOrFloat, 1, ruleSuffix},
	{OpSqrt, "sqrt", ConstraintFloat, 1, ruleSuffix},

	// Overflow predicates — "take the same two operands ... Legal on iN
	// and vec[iN, W]" (§4): explicit arity 2, int-only.
	{OpUAddO, "uaddo", ConstraintInt, 2, ruleBool},
	{OpSAddO, "saddo", ConstraintInt, 2, ruleBool},
	{OpUSubO, "usubo", ConstraintInt, 2, ruleBool},
	{OpSSubO, "ssubo", ConstraintInt, 2, ruleBool},
	{OpUMulO, "umulo", ConstraintInt, 2, ruleBool},
	{OpSMulO, "smulo", ConstraintInt, 2, ruleBool},

	{OpUMulH, "umulh", ConstraintInt, 2, ruleSuffix},
	{OpSMulH, "smulh", ConstraintInt, 2, ruleSuffix},

	{OpUAddSat, "uadd_sat", ConstraintInt, 2, ruleSuffix},
	{OpSAddSat, "sadd_sat", ConstraintInt, 2, ruleSuffix},
	{OpUSubSat, "usub_sat", ConstraintInt, 2, ruleSuffix},
	{OpSSubSat, "ssub_sat", ConstraintInt, 2, ruleSuffix},

	// Bits — int only, including ctlz/cttz/popcnt (the opcodes that
	// motivated this rewrite: previously absent from any classification
	// set, so silently unchecked).
	{OpAnd, "and", ConstraintInt, 2, ruleSuffix},
	{OpOr, "or", ConstraintInt, 2, ruleSuffix},
	{OpXor, "xor", ConstraintInt, 2, ruleSuffix},
	{OpNot, "not", ConstraintInt, 1, ruleSuffix},
	{OpShl, "shl", ConstraintInt, 2, ruleSuffix},
	{OpLShr, "lshr", ConstraintInt, 2, ruleSuffix},
	{OpAShr, "ashr", ConstraintInt, 2, ruleSuffix},
	{OpRotl, "rotl", ConstraintInt, 2, ruleSuffix},
	{OpRotr, "rotr", ConstraintInt, 2, ruleSuffix},
	{OpCtlz, "ctlz", ConstraintInt, 1, ruleSuffix},
	{OpCttz, "cttz", ConstraintInt, 1, ruleSuffix},
	{OpPopcnt, "popcnt", ConstraintInt, 1, ruleSuffix},

	// Bare min/max: numeric constraint left at None here because the
	// int-rejection (§9.17) needs a specific error message ("use
	// smin/smax/..."); resultType special-cases these explicitly instead.
	{OpMin, "min", ConstraintNone, 2, ruleSpecial},
	{OpMax, "max", ConstraintNone, 2, ruleSpecial},

	// Comparisons. eq/ne and the unsigned orderings apply to int OR ptr
	// (§4 "Pointers: eq.ptr, ne.ptr, and the unsigned orderings ...");
	// signed orderings are int-only (ptr has no signedness); lt/gt/le/ge
	// are the float row.
	{OpEq, "eq", ConstraintIntOrPtr, 2, ruleBool},
	{OpNe, "ne", ConstraintIntOrPtr, 2, ruleBool},
	{OpSlt, "slt", ConstraintInt, 2, ruleBool},
	{OpSgt, "sgt", ConstraintInt, 2, ruleBool},
	{OpSle, "sle", ConstraintInt, 2, ruleBool},
	{OpSge, "sge", ConstraintInt, 2, ruleBool},
	{OpUlt, "ult", ConstraintIntOrPtr, 2, ruleBool},
	{OpUgt, "ugt", ConstraintIntOrPtr, 2, ruleBool},
	{OpUle, "ule", ConstraintIntOrPtr, 2, ruleBool},
	{OpUge, "uge", ConstraintIntOrPtr, 2, ruleBool},
	{OpLt, "lt", ConstraintFloat, 2, ruleBool},
	{OpGt, "gt", ConstraintFloat, 2, ruleBool},
	{OpLe, "le", ConstraintFloat, 2, ruleBool},
	{OpGe, "ge", ConstraintFloat, 2, ruleBool},

	{OpSelect, "select", ConstraintNone, 3, ruleSuffix}, // cond, a, b

	// Memory & addresses.
	{OpAlloca, "alloca", ConstraintNone, 1, ruleSpecial}, // "bare alloca.ptr size" (§4): 1 operand; suffix must be .ptr, checked in resultType
	{OpLoad, "load", ConstraintNone, 1, ruleSuffix},
	{OpStore, "store", ConstraintNone, 2, ruleVoid},
	{OpLoadVol, "load_vol", ConstraintNone, 1, ruleSuffix},
	{OpStoreVol, "store_vol", ConstraintNone, 2, ruleVoid},
	{OpMemcopy, "memcopy", ConstraintNone, 3, ruleVoid},
	{OpMemmove, "memmove", ConstraintNone, 3, ruleVoid},
	{OpMemset, "memset", ConstraintNone, 3, ruleVoid},
	{OpField, "field", ConstraintNone, 3, ruleSuffix}, // p, S, f
	{OpIndex, "index", ConstraintNone, 3, ruleSuffix},  // p, T, i

	// Atomics. atomic_load/store/cmpxchg use "<T>" (int or ptr, §4);
	// the RMW ops (add/sub/and/or/xor/xchg) are pinned to "<iN>" in the
	// spec text explicitly, so ptr is excluded there.
	{OpAtomicLoad, "atomic_load", ConstraintIntOrPtr, 2, ruleSuffix},   // p, ord
	{OpAtomicStore, "atomic_store", ConstraintIntOrPtr, 3, ruleVoid},   // p, v, ord
	{OpAtomicAdd, "atomic_add", ConstraintInt, 3, ruleSuffix},
	{OpAtomicSub, "atomic_sub", ConstraintInt, 3, ruleSuffix},
	{OpAtomicAnd, "atomic_and", ConstraintInt, 3, ruleSuffix},
	{OpAtomicOr, "atomic_or", ConstraintInt, 3, ruleSuffix},
	{OpAtomicXor, "atomic_xor", ConstraintInt, 3, ruleSuffix},
	{OpAtomicXchg, "atomic_xchg", ConstraintInt, 3, ruleSuffix},
	{OpCmpxchg, "cmpxchg", ConstraintIntOrPtr, 5, ruleSuffix}, // p, expected, desired, ord_ok, ord_fail
	{OpFence, "fence", ConstraintNone, 1, ruleVoid},           // ord only, no type suffix at all

	// Conversions — deeper source/dest unification is a known TODO
	// (§9.16); the destination-side int/float constraint below is what's
	// cheaply and correctly checkable from the suffix alone.
	{OpTrunc, "trunc", ConstraintNone, 1, ruleSuffix},
	{OpSext, "sext", ConstraintNone, 1, ruleSuffix},
	{OpZext, "zext", ConstraintNone, 1, ruleSuffix},
	{OpFdemote, "fdemote", ConstraintNone, 1, ruleSuffix},
	{OpFpromote, "fpromote", ConstraintNone, 1, ruleSuffix},
	{OpBitcast, "bitcast", ConstraintNone, 1, ruleSuffix},
	{OpSfromint, "sfromint", ConstraintFloat, 1, ruleSuffix},
	{OpUfromint, "ufromint", ConstraintFloat, 1, ruleSuffix},
	{OpStoint, "stoint", ConstraintInt, 1, ruleSuffix},
	{OpUtoint, "utoint", ConstraintInt, 1, ruleSuffix},
	{OpStointSat, "stoint_sat", ConstraintInt, 1, ruleSuffix},
	{OpUtointSat, "utoint_sat", ConstraintInt, 1, ruleSuffix},

	// Vectors. Arity left at -1 for splat/extract/insert: §4 lists the
	// mnemonics but gives no inline operand-list example the way
	// shuffle/masked_load/gather/scatter get (those are pinned exactly).
	{OpSplat, "splat", ConstraintNone, -1, ruleSuffix},
	{OpExtract, "extract", ConstraintNone, -1, ruleSpecial},
	{OpInsert, "insert", ConstraintNone, -1, ruleSuffix},
	{OpShuffle, "shuffle", ConstraintNone, 3, ruleSuffix}, // a, b, mask
	{OpMaskedLoad, "masked_load", ConstraintNone, 3, ruleSuffix},
	{OpMaskedStore, "masked_store", ConstraintNone, 3, ruleVoid},
	{OpGather, "gather", ConstraintNone, 3, ruleSuffix},
	{OpScatter, "scatter", ConstraintNone, 3, ruleVoid},

	// Intrinsics. fma/copysign have unambiguous standard arities (fused
	// multiply-add; sign-copy) even though §4 doesn't spell out an
	// operand list — floor/ceil/trunc_f/nearest/bswap/bitrev are unary by
	// the same reasoning. These, along with sqrt above, were previously
	// covered only by the dead `floatOnlyUnary` set — declared, never
	// referenced, so never enforced.
	{OpFma, "fma", ConstraintFloat, 3, ruleSuffix},
	{OpCopysign, "copysign", ConstraintFloat, 2, ruleSuffix},
	{OpFloor, "floor", ConstraintFloat, 1, ruleSuffix},
	{OpCeil, "ceil", ConstraintFloat, 1, ruleSuffix},
	{OpTruncF, "trunc_f", ConstraintFloat, 1, ruleSuffix},
	{OpNearest, "nearest", ConstraintFloat, 1, ruleSuffix},
	{OpSMin, "smin", ConstraintInt, 2, ruleSuffix},
	{OpSMax, "smax", ConstraintInt, 2, ruleSuffix},
	{OpUMin, "umin", ConstraintInt, 2, ruleSuffix},
	{OpUMax, "umax", ConstraintInt, 2, ruleSuffix},
	{OpBSwap, "bswap", ConstraintInt, 1, ruleSuffix}, // also rejected on i8 specifically, checked separately (§9.20)
	{OpBitrev, "bitrev", ConstraintInt, 1, ruleSuffix},

	// Reductions — §4 gives no operand-count or element-constraint text;
	// left permissive (arity -1, no numeric constraint) rather than
	// invented, consistent with the "mark it, don't guess" TODO style
	// used elsewhere in this verifier.
	{OpReduceAdd, "reduce_add", ConstraintNone, -1, ruleSpecial},
	{OpReduceMin, "reduce_min", ConstraintNone, -1, ruleSpecial},
	{OpReduceMax, "reduce_max", ConstraintNone, -1, ruleSpecial},
	{OpReduceAnd, "reduce_and", ConstraintNone, -1, ruleSpecial},
	{OpReduceOr, "reduce_or", ConstraintNone, -1, ruleSpecial},
	{OpReduceXor, "reduce_xor", ConstraintNone, -1, ruleSpecial},

	{OpPrefetch, "prefetch", ConstraintNone, -1, ruleVoid},

	{OpCall, "call", ConstraintNone, -1, ruleSpecial},
	{OpSyscall, "syscall", ConstraintNone, -1, ruleSpecial},

	{OpLoc, "loc", ConstraintNone, -1, ruleVoid},
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
			panic(fmt.Sprintf("vir: opTable entry %q has out-of-range opcode %d", d.name, d.op))
		}
		if seen[d.op] {
			panic(fmt.Sprintf("vir: opcode %d registered twice in opTable (duplicate %q)", d.op, d.name))
		}
		if d.name == "" {
			panic(fmt.Sprintf("vir: opcode %d has an empty name in opTable", d.op))
		}
		if _, dup := opByName[d.name]; dup {
			panic(fmt.Sprintf("vir: opcode name %q registered twice in opTable", d.name))
		}
		seen[d.op] = true
		opNameTable[d.op] = d.name
		opMetaTable[d.op] = opMeta{numeric: d.numeric, arity: d.arity, result: d.result}
		opByName[d.name] = d.op
	}
	for i := 1; i < int(opcodeCount); i++ {
		if !seen[i] {
			panic(fmt.Sprintf("vir: Opcode constant %d has no opTable entry — every opcode must be registered (see opcode.go)", i))
		}
	}
}

// String returns the canonical §4 textual spelling of o.
func (o Opcode) String() string {
	if o == OpInvalid {
		return "<invalid opcode>"
	}
	if int(o) > 0 && int(o) < len(opNameTable) && opNameTable[o] != "" {
		return opNameTable[o]
	}
	return fmt.Sprintf("<opcode %d>", int(o))
}

// ParseOpcode resolves a §4 mnemonic (e.g. from a decoder) to its Opcode.
// Returns false for anything not in the closed §4 vocabulary — including
// terminator/asm keywords, which are not Opcodes (they're separate Go
// types / dialect-table data respectively).
func ParseOpcode(s string) (Opcode, bool) {
	op, ok := opByName[s]
	return op, ok
}

// meta looks up an opcode's registered metadata. ok is false only for
// OpInvalid or an out-of-range value — every in-range constant is
// guaranteed present by init()'s completeness check above.
func (o Opcode) meta() (opMeta, bool) {
	if int(o) <= 0 || int(o) >= len(opMetaTable) || opNameTable[o] == "" {
		return opMeta{}, false
	}
	return opMetaTable[o], true
}

// checkNumericConstraint enforces §9.18 for a single instruction's suffix
// against its opcode's registered operandConstraint.
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
		return fmt.Errorf("%s legal only on %s (§9.18)", op, constraintDescription(c))
	}
	return nil
}

func constraintDescription(c operandConstraint) string {
	switch c {
	case ConstraintInt:
		return "iN / vec[iN, W]"
	case ConstraintFloat:
		return "fN / vec[fN, W]"
	case ConstraintIntOrFloat:
		return "iN or fN (incl. vector forms)"
	case ConstraintIntOrPtr:
		return "iN / vec[iN, W] or ptr"
	}
	return "a compatible type"
}