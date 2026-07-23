// opinfo.go
package verify

import "github.com/vertex-language/vvm/ir/vir"

// Opcode metadata for instruction-shape checking (§4). This table is
// verify's own — vir's internal opTable (opcode.go) is unexported and
// backs only Opcode.String/ParseOpcode. verify.md: "opcode arity/
// operand-constraint/result-rule checks ... now live here instead."
type numConstraint int

const (
	cNone numConstraint = iota
	cInt
	cFloat
	cIntOrFloat
	cIntOrPtr
)

type resultRule int

const (
	rSuffix resultRule = iota // result type == Suffix
	rVoid                     // op never produces a value
	rBool                     // i1, or vec[i1,N] when Suffix is a vector
	rSpecial                  // computed by resultTypeSpecial
)

type opInfo struct {
	arity  int // -1 = variable/checked structurally elsewhere
	num    numConstraint
	result resultRule
}

var opInfoTable = map[vir.Opcode]opInfo{
	vir.OpAdd: {2, cIntOrFloat, rSuffix}, vir.OpSub: {2, cIntOrFloat, rSuffix}, vir.OpMul: {2, cIntOrFloat, rSuffix},
	vir.OpUDiv: {2, cInt, rSuffix}, vir.OpSDiv: {2, cInt, rSuffix}, vir.OpURem: {2, cInt, rSuffix}, vir.OpSRem: {2, cInt, rSuffix},
	vir.OpNeg: {1, cIntOrFloat, rSuffix}, vir.OpAbs: {1, cIntOrFloat, rSuffix}, vir.OpSqrt: {1, cFloat, rSuffix},

	vir.OpUAddO: {2, cInt, rBool}, vir.OpSAddO: {2, cInt, rBool}, vir.OpUSubO: {2, cInt, rBool},
	vir.OpSSubO: {2, cInt, rBool}, vir.OpUMulO: {2, cInt, rBool}, vir.OpSMulO: {2, cInt, rBool},

	vir.OpUMulH: {2, cInt, rSuffix}, vir.OpSMulH: {2, cInt, rSuffix},

	vir.OpUAddSat: {2, cInt, rSuffix}, vir.OpSAddSat: {2, cInt, rSuffix},
	vir.OpUSubSat: {2, cInt, rSuffix}, vir.OpSSubSat: {2, cInt, rSuffix},

	vir.OpAnd: {2, cInt, rSuffix}, vir.OpOr: {2, cInt, rSuffix}, vir.OpXor: {2, cInt, rSuffix}, vir.OpNot: {1, cInt, rSuffix},
	vir.OpShl: {2, cInt, rSuffix}, vir.OpLShr: {2, cInt, rSuffix}, vir.OpAShr: {2, cInt, rSuffix},
	vir.OpRotl: {2, cInt, rSuffix}, vir.OpRotr: {2, cInt, rSuffix},
	vir.OpCtlz: {1, cInt, rSuffix}, vir.OpCttz: {1, cInt, rSuffix}, vir.OpPopcnt: {1, cInt, rSuffix},

	// Bare min/max: numeric constraint left at cNone — the int-rejection
	// (§9.17) needs a dedicated error message, handled in resultTypeSpecial.
	vir.OpMin: {2, cNone, rSpecial}, vir.OpMax: {2, cNone, rSpecial},

	vir.OpEq: {2, cIntOrPtr, rBool}, vir.OpNe: {2, cIntOrPtr, rBool},
	vir.OpSlt: {2, cInt, rBool}, vir.OpSgt: {2, cInt, rBool}, vir.OpSle: {2, cInt, rBool}, vir.OpSge: {2, cInt, rBool},
	vir.OpUlt: {2, cIntOrPtr, rBool}, vir.OpUgt: {2, cIntOrPtr, rBool}, vir.OpUle: {2, cIntOrPtr, rBool}, vir.OpUge: {2, cIntOrPtr, rBool},
	vir.OpLt: {2, cFloat, rBool}, vir.OpGt: {2, cFloat, rBool}, vir.OpLe: {2, cFloat, rBool}, vir.OpGe: {2, cFloat, rBool},

	vir.OpSelect: {3, cNone, rSuffix},

	// OpAlloca's arity depends on its Suffix (alloca.ptr: 1 operand for
	// size; alloca.valist: 0 operands, target-defined layout) — not a
	// single fixed number the generic arity check in checkInstruction can
	// express, so it's registered here as -1 ("checked structurally
	// elsewhere") and the real per-variant check lives in
	// resultTypeSpecial (body.go), which already branches on Suffix for
	// this opcode.
	vir.OpAlloca: {-1, cNone, rSpecial},
	vir.OpLoad: {1, cNone, rSuffix}, vir.OpStore: {2, cNone, rVoid},
	vir.OpLoadVol: {1, cNone, rSuffix}, vir.OpStoreVol: {2, cNone, rVoid},
	vir.OpMemcopy: {3, cNone, rVoid}, vir.OpMemmove: {3, cNone, rVoid}, vir.OpMemset: {3, cNone, rVoid},
	vir.OpField: {3, cNone, rSuffix}, vir.OpIndex: {3, cNone, rSuffix},

	vir.OpAtomicLoad: {2, cIntOrPtr, rSuffix}, vir.OpAtomicStore: {3, cIntOrPtr, rVoid},
	vir.OpAtomicAdd: {3, cInt, rSuffix}, vir.OpAtomicSub: {3, cInt, rSuffix}, vir.OpAtomicAnd: {3, cInt, rSuffix},
	vir.OpAtomicOr: {3, cInt, rSuffix}, vir.OpAtomicXor: {3, cInt, rSuffix}, vir.OpAtomicXchg: {3, cInt, rSuffix},
	vir.OpCmpxchg: {5, cIntOrPtr, rSuffix}, vir.OpFence: {1, cNone, rVoid},

	vir.OpTrunc: {1, cNone, rSuffix}, vir.OpSext: {1, cNone, rSuffix}, vir.OpZext: {1, cNone, rSuffix},
	vir.OpFdemote: {1, cNone, rSuffix}, vir.OpFpromote: {1, cNone, rSuffix}, vir.OpBitcast: {1, cNone, rSuffix},
	vir.OpSfromint: {1, cFloat, rSuffix}, vir.OpUfromint: {1, cFloat, rSuffix},
	vir.OpStoint: {1, cInt, rSuffix}, vir.OpUtoint: {1, cInt, rSuffix},
	vir.OpStointSat: {1, cInt, rSuffix}, vir.OpUtointSat: {1, cInt, rSuffix},

	vir.OpSplat: {-1, cNone, rSuffix}, vir.OpExtract: {-1, cNone, rSpecial}, vir.OpInsert: {-1, cNone, rSuffix},
	vir.OpShuffle: {3, cNone, rSuffix}, vir.OpMaskedLoad: {3, cNone, rSuffix}, vir.OpMaskedStore: {3, cNone, rVoid},
	vir.OpGather: {3, cNone, rSuffix}, vir.OpScatter: {3, cNone, rVoid},

	vir.OpFma: {3, cFloat, rSuffix}, vir.OpCopysign: {2, cFloat, rSuffix},
	vir.OpFloor: {1, cFloat, rSuffix}, vir.OpCeil: {1, cFloat, rSuffix}, vir.OpTruncF: {1, cFloat, rSuffix}, vir.OpNearest: {1, cFloat, rSuffix},
	vir.OpSMin: {2, cInt, rSuffix}, vir.OpSMax: {2, cInt, rSuffix}, vir.OpUMin: {2, cInt, rSuffix}, vir.OpUMax: {2, cInt, rSuffix},
	vir.OpBSwap: {1, cInt, rSuffix}, vir.OpBitrev: {1, cInt, rSuffix},

	vir.OpReduceAdd: {1, cNone, rSpecial}, vir.OpReduceMin: {1, cNone, rSpecial}, vir.OpReduceMax: {1, cNone, rSpecial},
	vir.OpReduceAnd: {1, cNone, rSpecial}, vir.OpReduceOr: {1, cNone, rSpecial}, vir.OpReduceXor: {1, cNone, rSpecial},

	vir.OpPrefetch: {-1, cNone, rVoid},

	vir.OpCall: {-1, cNone, rSpecial}, vir.OpSyscall: {-1, cNone, rSpecial},

	vir.OpVaStart: {2, cNone, rVoid}, vir.OpVaArg: {1, cNone, rSuffix}, vir.OpVaEnd: {1, cNone, rVoid},

	vir.OpLoc: {-1, cNone, rVoid},
}

func numericConstraintOK(t vir.Type, c numConstraint) bool {
	if c == cNone {
		return true
	}
	if t == nil {
		return false
	}
	elem := vir.ElemOrSelf(t)
	switch c {
	case cInt:
		return vir.IsInt(elem)
	case cFloat:
		return vir.IsFloat(elem)
	case cIntOrFloat:
		return vir.IsInt(elem) || vir.IsFloat(elem)
	case cIntOrPtr:
		return vir.IsInt(elem) || vir.IsPtr(t)
	}
	return false
}