package ptx

// Op identifies a PTX instruction. Each Op carries its base mnemonic, the
// canonical order in which its qualifier slots are emitted, how many type
// specifiers it takes, and the ISA version and target it requires.
//
// Qualifier ordering lives here and nowhere else. If a mnemonic ever prints
// its qualifiers in the wrong order, this table is the only place to fix it.
type Op uint16

const (
	OpCustom Op = iota // Emit: mnemonic supplied by the caller

	// Integer and floating-point arithmetic.
	OpAdd
	OpSub
	OpMul
	OpMad
	OpFma
	OpDiv
	OpRem
	OpAbs
	OpNeg
	OpMin
	OpMax
	OpSqrt
	OpRcp
	OpRsqrt
	OpSin
	OpCos
	OpEx2
	OpLg2
	OpTanh
	OpTestp
	OpCopysign
	OpSad
	OpMul24
	OpMad24

	// Extended-precision integer arithmetic.
	OpAddCC
	OpAddC
	OpSubCC
	OpSubC
	OpMadCC
	OpMadC

	// Bit manipulation.
	OpPopc
	OpClz
	OpBrev
	OpBfind
	OpFns
	OpBfe
	OpBfi
	OpSzext
	OpBmsk
	OpDp4a
	OpDp2a
	OpPrmt
	OpLop3

	// Comparison and selection.
	OpSet
	OpSetp
	OpSelp
	OpSlct

	// Logic and shift.
	OpAnd
	OpOr
	OpXor
	OpNot
	OpCnot
	OpShl
	OpShr
	OpShf

	// Data movement and conversion.
	OpMov
	OpCvt
	OpCvtPack
	OpCvta
	OpLd
	OpLdu
	OpSt
	OpPrefetch
	OpPrefetchU
	OpIsspacep
	OpMapa
	OpGetctarank
	OpApplyPriority
	OpDiscard

	// Control flow.
	OpBra
	OpBrxIdx
	OpCall
	OpRet
	OpExit

	// Parallel synchronization and communication.
	OpBarSync
	OpBarArrive
	OpBarRed
	OpBarWarpSync
	OpBarrierSync
	OpBarrierArrive
	OpBarrierClusterArrive
	OpBarrierClusterWait
	OpMembar
	OpFence
	OpAtom
	OpRed
	OpShflSync
	OpVoteSync
	OpMatchSync
	OpActivemask
	OpReduxSync
	OpElectSync
	OpGridDepLaunch
	OpGridDepWait

	// Stack manipulation.
	OpAlloca
	OpStackSave
	OpStackRestore

	// Miscellaneous.
	OpBrkpt
	OpNanosleep
	OpPmevent
	OpTrap
	OpSetMaxNReg
)

type opInfo struct {
	mnemonic string
	quals    []qualKind
	ntypes   int
	minISA   ISAVersion
	minSM    int
}

// Common qualifier orders, named so the table below stays readable.
var (
	qArith  = []qualKind{qRound, qFtz, qSat}
	qMulish = []qualKind{qWidth, qRound, qFtz, qSat}
	qUnFP   = []qualKind{qRound, qApprox, qFtz}
	qLoad   = []qualKind{qSem, qScope, qSpace, qNC, qCache, qEvict, qVec}
	qStore  = []qualKind{qSem, qScope, qSpace, qCache, qEvict, qVec}
	qAtomic = []qualKind{qSem, qScope, qSpace, qAtom, qCache}
	qNone   []qualKind
)

var opTable = map[Op]opInfo{
	OpCustom: {"", []qualKind{qSem, qScope, qSpace, qCache, qVec}, 0, ISAVersion{}, 0},

	OpAdd:      {"add", qArith, 1, ISAVersion{}, 0},
	OpSub:      {"sub", qArith, 1, ISAVersion{}, 0},
	OpMul:      {"mul", qMulish, 1, ISAVersion{}, 0},
	OpMad:      {"mad", qMulish, 1, ISAVersion{}, 0},
	OpFma:      {"fma", qArith, 1, ISAVersion{}, 0},
	OpDiv:      {"div", []qualKind{qRound, qApprox, qFull, qFtz}, 1, ISAVersion{}, 0},
	OpRem:      {"rem", qNone, 1, ISAVersion{}, 0},
	OpAbs:      {"abs", []qualKind{qFtz}, 1, ISAVersion{}, 0},
	OpNeg:      {"neg", []qualKind{qFtz}, 1, ISAVersion{}, 0},
	OpMin:      {"min", []qualKind{qAbs, qNaN, qFtz}, 1, ISAVersion{}, 0},
	OpMax:      {"max", []qualKind{qAbs, qNaN, qFtz}, 1, ISAVersion{}, 0},
	OpSqrt:     {"sqrt", qUnFP, 1, ISAVersion{}, 0},
	OpRcp:      {"rcp", qUnFP, 1, ISAVersion{}, 0},
	OpRsqrt:    {"rsqrt", qUnFP, 1, ISAVersion{}, 0},
	OpSin:      {"sin", qUnFP, 1, ISAVersion{}, 0},
	OpCos:      {"cos", qUnFP, 1, ISAVersion{}, 0},
	OpEx2:      {"ex2", qUnFP, 1, ISAVersion{}, 0},
	OpLg2:      {"lg2", qUnFP, 1, ISAVersion{}, 0},
	OpTanh:     {"tanh", []qualKind{qApprox, qFtz}, 1, ISA70, 75},
	OpTestp:    {"testp", []qualKind{qTestp}, 1, ISAVersion{}, 0},
	OpCopysign: {"copysign", qNone, 1, ISAVersion{}, 0},
	OpSad:      {"sad", qNone, 1, ISAVersion{}, 0},
	OpMul24:    {"mul24", []qualKind{qWidth}, 1, ISAVersion{}, 0},
	OpMad24:    {"mad24", []qualKind{qWidth, qSat}, 1, ISAVersion{}, 0},

	OpAddCC: {"add.cc", qNone, 1, ISAVersion{}, 0},
	OpAddC:  {"addc", []qualKind{qCmp}, 1, ISAVersion{}, 0},
	OpSubCC: {"sub.cc", qNone, 1, ISAVersion{}, 0},
	OpSubC:  {"subc", qNone, 1, ISAVersion{}, 0},
	OpMadCC: {"mad.cc", []qualKind{qWidth}, 1, ISAVersion{}, 0},
	OpMadC:  {"madc", []qualKind{qWidth}, 1, ISAVersion{}, 0},

	OpPopc:  {"popc", qNone, 1, ISAVersion{}, 0},
	OpClz:   {"clz", qNone, 1, ISAVersion{}, 0},
	OpBrev:  {"brev", qNone, 1, ISAVersion{}, 0},
	OpBfind: {"bfind", []qualKind{qShiftAmt}, 1, ISAVersion{}, 0},
	OpFns:   {"fns", qNone, 1, ISA76, 30},
	OpBfe:   {"bfe", qNone, 1, ISAVersion{}, 0},
	OpBfi:   {"bfi", qNone, 1, ISAVersion{}, 0},
	OpSzext: {"szext", []qualKind{qShf}, 1, ISA76, 70},
	OpBmsk:  {"bmsk", []qualKind{qShf}, 1, ISA76, 70},
	OpDp4a:  {"dp4a", qNone, 2, ISAetc(ISA61()), 61},
	OpDp2a:  {"dp2a", []qualKind{qWidth}, 2, ISAetc(ISA61()), 61},
	OpPrmt:  {"prmt", qNone, 1, ISAVersion{}, 0},
	OpLop3:  {"lop3", qNone, 1, ISA70, 50},

	OpSet:  {"set", []qualKind{qCmp, qBool, qFtz}, 2, ISAVersion{}, 0},
	OpSetp: {"setp", []qualKind{qCmp, qBool, qFtz}, 1, ISAVersion{}, 0},
	OpSelp: {"selp", qNone, 1, ISAVersion{}, 0},
	OpSlct: {"slct", []qualKind{qFtz}, 2, ISAVersion{}, 0},

	OpAnd:  {"and", qNone, 1, ISAVersion{}, 0},
	OpOr:   {"or", qNone, 1, ISAVersion{}, 0},
	OpXor:  {"xor", qNone, 1, ISAVersion{}, 0},
	OpNot:  {"not", qNone, 1, ISAVersion{}, 0},
	OpCnot: {"cnot", qNone, 1, ISAVersion{}, 0},
	OpShl:  {"shl", qNone, 1, ISAVersion{}, 0},
	OpShr:  {"shr", qNone, 1, ISAVersion{}, 0},
	OpShf:  {"shf", []qualKind{qDir, qShf}, 1, ISAVersion{}, 60},

	OpMov:     {"mov", qNone, 1, ISAVersion{}, 0},
	OpCvt:     {"cvt", []qualKind{qRound, qFtz, qSatFinite, qSat, qRelu}, 2, ISAVersion{}, 0},
	OpCvtPack: {"cvt.pack", []qualKind{qSat, qRound}, 2, ISA70, 72},
	OpCvta:    {"cvta", []qualKind{qTo, qSpace}, 1, ISAVersion{}, 0},

	OpLd:            {"ld", qLoad, 1, ISAVersion{}, 0},
	OpLdu:           {"ldu", []qualKind{qSpace, qVec}, 1, ISAVersion{}, 0},
	OpSt:            {"st", qStore, 1, ISAVersion{}, 0},
	OpPrefetch:      {"prefetch", []qualKind{qSpace, qLevel, qEvict}, 0, ISAVersion{}, 20},
	OpPrefetchU:     {"prefetchu", []qualKind{qLevel}, 0, ISAVersion{}, 20},
	OpIsspacep:      {"isspacep", []qualKind{qSpace}, 0, ISAVersion{}, 20},
	OpMapa:          {"mapa", []qualKind{qSpace}, 1, ISA80, 90},
	OpGetctarank:    {"getctarank", []qualKind{qSpace}, 1, ISA80, 90},
	OpApplyPriority: {"applypriority", []qualKind{qSpace, qEvict}, 0, ISA74, 80},
	OpDiscard:       {"discard", []qualKind{qSpace, qLevel}, 0, ISA74, 80},

	OpBra:    {"bra", []qualKind{qUni}, 0, ISAVersion{}, 0},
	OpBrxIdx: {"brx.idx", []qualKind{qUni}, 0, ISA60, 30},
	OpCall:   {"call", []qualKind{qUni}, 0, ISAVersion{}, 0},
	OpRet:    {"ret", []qualKind{qUni}, 0, ISAVersion{}, 0},
	OpExit:   {"exit", qNone, 0, ISAVersion{}, 0},

	OpBarSync:              {"bar.sync", qNone, 0, ISAVersion{}, 0},
	OpBarArrive:            {"bar.arrive", qNone, 0, ISAVersion{}, 0},
	OpBarRed:               {"bar.red", []qualKind{qBool}, 1, ISAVersion{}, 20},
	OpBarWarpSync:          {"bar.warp.sync", qNone, 0, ISA60, 30},
	OpBarrierSync:          {"barrier.sync", []qualKind{qAligned}, 0, ISA60, 30},
	OpBarrierArrive:        {"barrier.arrive", []qualKind{qAligned}, 0, ISA60, 30},
	OpBarrierClusterArrive: {"barrier.cluster.arrive", []qualKind{qSem, qAligned}, 0, ISA78, 90},
	OpBarrierClusterWait:   {"barrier.cluster.wait", []qualKind{qSem, qAligned}, 0, ISA78, 90},
	OpMembar:               {"membar", []qualKind{qLevel}, 0, ISAVersion{}, 0},
	OpFence:                {"fence", []qualKind{qProxy, qSem, qScope, qSpace}, 0, ISA60, 70},
	OpAtom:                 {"atom", qAtomic, 1, ISAVersion{}, 0},
	OpRed:                  {"red", qAtomic, 1, ISAVersion{}, 0},
	OpShflSync:             {"shfl.sync", []qualKind{qShfl}, 1, ISA60, 30},
	OpVoteSync:             {"vote.sync", []qualKind{qVote}, 1, ISA60, 30},
	OpMatchSync:            {"match.sync", []qualKind{qMatch}, 1, ISA60, 70},
	OpActivemask:           {"activemask", qNone, 1, ISA62, 30},
	OpReduxSync:            {"redux.sync", []qualKind{qRedux, qAbs, qNaN}, 1, ISA70, 80},
	OpElectSync:            {"elect.sync", qNone, 1, ISA80, 90},
	OpGridDepLaunch:        {"griddepcontrol.launch_dependents", qNone, 0, ISA78, 90},
	OpGridDepWait:          {"griddepcontrol.wait", qNone, 0, ISA78, 90},

	OpAlloca:       {"alloca", qNone, 1, ISA73, 52},
	OpStackSave:    {"stacksave", qNone, 1, ISA73, 52},
	OpStackRestore: {"stackrestore", qNone, 1, ISA73, 52},

	OpBrkpt:      {"brkpt", qNone, 0, ISAVersion{}, 0},
	OpNanosleep:  {"nanosleep", qNone, 1, ISA63, 70},
	OpPmevent:    {"pmevent", []qualKind{qAligned}, 0, ISAVersion{}, 20},
	OpTrap:       {"trap", qNone, 0, ISAVersion{}, 0},
	OpSetMaxNReg: {"setmaxnreg", []qualKind{qDir, qAligned}, 1, ISA80, 90},
}

// ISA60/ISA61/ISA62/ISA63 are declared here rather than in version.go
// because only the opcode table needs them.
var (
	ISA60 = ISAVersion{6, 0}
	ISA62 = ISAVersion{6, 2}
	ISA63 = ISAVersion{6, 3}
)

func ISA61() ISAVersion             { return ISAVersion{6, 1} }
func ISAetc(v ISAVersion) ISAVersion { return v }

// Mnemonic returns the base mnemonic without qualifiers or types.
func (o Op) Mnemonic() string { return opTable[o].mnemonic }

// NTypes returns how many type specifiers the opcode takes.
func (o Op) NTypes() int { return opTable[o].ntypes }

// MinISA returns the ISA version the opcode requires.
func (o Op) MinISA() ISAVersion { return opTable[o].minISA }

// MinSM returns the sm_* number the opcode requires, or 0 if unrestricted.
func (o Op) MinSM() int { return opTable[o].minSM }

// IsValid reports whether o names a modelled opcode.
func (o Op) IsValid() bool { _, ok := opTable[o]; return ok }