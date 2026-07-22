// format.go
// Package binary implements .vbyte per file_formats.md §F2–§F4: the
// magic+header+section-table+trailer container, hash-consed STRT/TYPE
// tables, and the fixed §F4.1 section order. This is a from-scratch
// rewrite against that spec — the previous version of this package was a
// flat tagged-varint stream with none of the container/table machinery.
//
// Deliberate deviations/extensions where file_formats.md is silent or in
// tension with the actual vir data model (flagged here once, rather than
// scattered as ten small comments):
//
//  1. STRUCT_N (type-table kind 0x08) is documented as "struct-decl index"
//     only, but vir.StructType also supports a cross-module Import path
//     (§7.3/§7.4 byval[S]/sret[S], field.ptr on an imported struct). We
//     extend the payload with a leading origin byte: 0 = local (uleb
//     index into this module's STRU), 1 = imported (str import-path,
//     str name). Untagged STRUCT_N would be ambiguous otherwise.
//  2. F2.7's `literal` grammar lists INT/FLOAT/STRING/NULL only, but
//     operand.go/module.go also need bool consts (i1) and vector consts
//     (shuffle masks, §9.31). We add 0x05 BOOL and 0x06 VECTOR to the
//     value encoding used both for const/global literals and instruction
//     LITERAL operands (renumbering the tag space; see valueTag* below).
//  3. F4.2's const_init grammar omits a byte-string form, but module.go
//     has a distinct InitByteString (the README's quoted-string global
//     initializer, e.g. `global fmt array[i8,14] = "...\n\0"`). We add
//     tag 0x04 BYTE_STRING to const_init.
//  4. F4.4 stores asm `code:` bodies as re-parsed dialect text. This
//     package's own original design note said the opposite — ".vbyte...
//     pre-parsed and portable, with no textual re-lexing needed on
//     load" — and vir.AsmCodeLine/AsmOperand are already structured Go
//     values. We keep asm code lines structurally encoded (mnemonic +
//     typed operands) instead of as opaque re-parsed text; the
//     memory-operand text is still carried verbatim in AsmOperand.Memory
//     either way, so nothing is lost, and decode needs no dialect parser.
//  5. F4.5's LOCS deltas are specified as uleb, but block/inst indices
//     reset to 0 at each new function/block, which a delta needs signed
//     arithmetic to express without inflating values on every reset.
//     d_func stays uleb (monotonically non-decreasing); d_block/d_inst
//     are sleb.
//  6. `loc` lines are ordinary OpLoc instructions in the vir model
//     (module.go BodyLine has no separate slot for them), but
//     file_formats.md keys LOCS entries by (func,block,inst) as if they
//     were pulled out of the instruction stream. We extract OpLoc body
//     lines out of FUNC's inst[] into LOCS at encode time (keyed by the
//     index of the real instruction they precede) and re-splice them
//     back in at decode time, preserving relative order.
//  7. Inline-asm blocks are referenced from a function body via a
//     reserved pseudo-opcode in the §F7.2 0x0600–0x06FF ("asm") range,
//     whose single operand is the F4.3 ASM(uleb ASMB index) operand
//     kind — F4.3 defines that operand kind but never says what
//     instruction carries it; this is the natural place.
//  8. Multiple `clobber` bindings in one asm block are coalesced into a
//     single AsmBinding{Kind: Clobber, Registers: [...]} on decode,
//     matching how AsmBuilder.Clobber(regs...) actually constructs them.
//  9. f16 bit-pattern conversion is a simplified truncating conversion,
//     not a correctly-rounded float32→float16 (TODO: round-to-nearest-even).
package binary

import "github.com/vertex-language/vvm/ir/vir"

// ---------------------------------------------------------------------------
// Container constants (§F2.2, §F2.3, §F4.1)
// ---------------------------------------------------------------------------

var vbyteMagic = []byte{0x00, 'V', 'B', 'Y', 0x0D, 0x0A, 0x1A, 0x0A} // \0VBY\r\n\x1a\n

const (
	formatMajor uint16 = 1
	formatMinor uint16 = 0
	irMajor     uint16 = 1
	irMinor     uint16 = 9
)

const (
	headerSize     = 32
	sectionEntry   = 24
	trailerSize    = 40
	hashAlgoSHA256 = 2 // per file_formats.md §F2.2 trailer: hash_algo values are open; 2 = SHA-256
)

// Section tags, in the mandatory §F4.1 order.
const (
	tagSTRT = "STRT"
	tagTYPE = "TYPE"
	tagMODU = "MODU"
	tagTARG = "TARG"
	tagASMD = "ASMD"
	tagSTRU = "STRU"
	tagFSIG = "FSIG"
	tagCNST = "CNST"
	tagGLOB = "GLOB"
	tagLINK = "LINK"
	tagEXTN = "EXTN"
	tagIMPT = "IMPT"
	tagASMB = "ASMB"
	tagFUNC = "FUNC"
	tagLOCS = "LOCS"
	tagHASH = "HASH"
)

var sectionOrder = []string{
	tagSTRT, tagTYPE, tagMODU, tagTARG, tagASMD, tagSTRU, tagFSIG, tagCNST,
	tagGLOB, tagLINK, tagEXTN, tagIMPT, tagASMB, tagFUNC, tagLOCS, tagHASH,
}

const (
	sectionFlagRequired    uint32 = 1 << 0
	sectionFlagZSTD        uint32 = 1 << 1
	sectionFlagNonSemantic uint32 = 1 << 2
)

// ---------------------------------------------------------------------------
// Type table (§F2.6) — kind bytes. STRUCT_S (0x09) is .vmeta-only; not used here.
// ---------------------------------------------------------------------------

const (
	typeKindVoid    byte = 0x01
	typeKindInt     byte = 0x02
	typeKindFloat   byte = 0x03
	typeKindPtr     byte = 0x04
	typeKindValist  byte = 0x05
	typeKindVec     byte = 0x06
	typeKindArray   byte = 0x07
	typeKindStructN byte = 0x08
)

const (
	structOriginLocal byte = 0 // extension #1
	structOriginImport byte = 1
)

// ---------------------------------------------------------------------------
// Value encoding — used for const/global literals AND instruction LITERAL
// operands (extension #2 adds BOOL/VECTOR beyond F2.7's INT/FLOAT/STRING/NULL).
// ---------------------------------------------------------------------------

const (
	valueTagInt    byte = 0x01
	valueTagFloat  byte = 0x02
	valueTagString byte = 0x03
	valueTagNull   byte = 0x04
	valueTagBool   byte = 0x05
	valueTagVector byte = 0x06
)

// const_init tags (§F4.2, extension #3 adds BYTE_STRING).
const (
	initTagZero       byte = 0x00
	initTagLiteral    byte = 0x01
	initTagAddr       byte = 0x02
	initTagAggregate  byte = 0x03
	initTagByteString byte = 0x04
)

// Instruction operand kinds (§F4.3).
const (
	operandLocal    byte = 0x00
	operandDecl     byte = 0x01
	operandImport   byte = 0x02
	operandLiteral  byte = 0x03
	operandType     byte = 0x04
	operandOrdering byte = 0x05
	operandLabel    byte = 0x06
	operandAsm      byte = 0x07
)

// DECL operand kind byte (§F4.3: "struct/fnsig/const/global/fn/extern").
const (
	declStruct byte = 0
	declFnSig  byte = 1
	declConst  byte = 2
	declGlobal byte = 3
	declFn     byte = 4
	declExtern byte = 5
)

var orderingCodes = map[string]byte{
	"relaxed": 0, "acquire": 1, "release": 2, "acqrel": 3, "seqcst": 4,
}
var orderingNames = [5]string{"relaxed", "acquire", "release", "acqrel", "seqcst"}

// Link kind byte (§F4.2).
var linkKindCode = map[vir.LinkKind]byte{
	vir.LinkStatic: 0, vir.LinkShared: 1, vir.LinkFramework: 2,
}
var linkKindByCode = map[byte]vir.LinkKind{
	0: vir.LinkStatic, 1: vir.LinkShared, 2: vir.LinkFramework,
}

// AsmDialect byte (§F4.1 ASMD).
var asmDialectCode = map[vir.AsmDialect]byte{
	vir.DialectIntel: 0, vir.DialectATT: 1, vir.DialectA32: 2, vir.DialectT32: 3, vir.DialectNative: 4,
}
var asmDialectByCode = map[byte]vir.AsmDialect{
	0: vir.DialectIntel, 1: vir.DialectATT, 2: vir.DialectA32, 3: vir.DialectT32, 4: vir.DialectNative,
}

// Asm binding kind byte (§F4.4).
var bindingKindCode = map[vir.AsmBindingKind]byte{
	vir.BindingIn: 0, vir.BindingOut: 1, vir.BindingClobber: 2,
}
var bindingKindByCode = map[byte]vir.AsmBindingKind{
	0: vir.BindingIn, 1: vir.BindingOut, 2: vir.BindingClobber,
}

// Structural asm code-line encoding (extension #4): isLabel byte, then
// either a label string or a mnemonic + operand list.
const (
	asmOperandKindRegister  byte = 0
	asmOperandKindImmediate byte = 1
	asmOperandKindMemory    byte = 2
	asmOperandKindLabel     byte = 3
)

// Terminator "opcodes" (§F7.2 range 0x0000–0x00FF).
const (
	termBranch      uint64 = 0x0000
	termBranchIf    uint64 = 0x0001
	termSwitch      uint64 = 0x0002
	termReturn      uint64 = 0x0003
	termTailCall    uint64 = 0x0004
	termTrap        uint64 = 0x0005
	termUnreachable uint64 = 0x0006
)

// TailCall discriminator: direct-callee vs. fnsig-indirect (own encoding, not
// spec'd at this granularity by file_formats.md).
const (
	tailCallDirect   byte = 0
	tailCallIndirect byte = 1
)

// Reserved pseudo-opcode for "this body line is an asm block" (extension #7),
// in the §F7.2 0x0600–0x06FF ("asm") range.
const pseudoOpcodeAsmBlock uint64 = 0x06FE

// ---------------------------------------------------------------------------
// Opcode numbering (§F7.2). vir.Opcode's own iota ordering is an internal
// implementation detail of package vir, not a stable wire format, so we
// assign our own stable numbers here, grouped into the spec's ranges.
// ---------------------------------------------------------------------------

var opcodeRangeMath = []vir.Opcode{
	vir.OpAdd, vir.OpSub, vir.OpMul, vir.OpUDiv, vir.OpSDiv, vir.OpURem, vir.OpSRem,
	vir.OpNeg, vir.OpAbs, vir.OpSqrt,
	vir.OpUAddO, vir.OpSAddO, vir.OpUSubO, vir.OpSSubO, vir.OpUMulO, vir.OpSMulO,
	vir.OpUMulH, vir.OpSMulH,
	vir.OpUAddSat, vir.OpSAddSat, vir.OpUSubSat, vir.OpSSubSat,
	vir.OpAnd, vir.OpOr, vir.OpXor, vir.OpNot, vir.OpShl, vir.OpLShr, vir.OpAShr,
	vir.OpRotl, vir.OpRotr, vir.OpCtlz, vir.OpCttz, vir.OpPopcnt,
	vir.OpMin, vir.OpMax,
	vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
	vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge, vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe,
	vir.OpSelect,
}

var opcodeRangeConvVecIntrinsic = []vir.Opcode{
	vir.OpTrunc, vir.OpSext, vir.OpZext, vir.OpFdemote, vir.OpFpromote, vir.OpBitcast,
	vir.OpSfromint, vir.OpUfromint, vir.OpStoint, vir.OpUtoint, vir.OpStointSat, vir.OpUtointSat,
	vir.OpSplat, vir.OpExtract, vir.OpInsert, vir.OpShuffle,
	vir.OpMaskedLoad, vir.OpMaskedStore, vir.OpGather, vir.OpScatter,
	vir.OpFma, vir.OpCopysign, vir.OpFloor, vir.OpCeil, vir.OpTruncF, vir.OpNearest,
	vir.OpSMin, vir.OpSMax, vir.OpUMin, vir.OpUMax, vir.OpBSwap, vir.OpBitrev,
	vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax, vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor,
	vir.OpPrefetch,
}

var opcodeRangeMemory = []vir.Opcode{
	vir.OpAlloca, vir.OpLoad, vir.OpStore, vir.OpLoadVol, vir.OpStoreVol,
	vir.OpMemcopy, vir.OpMemmove, vir.OpMemset, vir.OpField, vir.OpIndex,
}

var opcodeRangeAtomics = []vir.Opcode{
	vir.OpAtomicLoad, vir.OpAtomicStore, vir.OpAtomicAdd, vir.OpAtomicSub,
	vir.OpAtomicAnd, vir.OpAtomicOr, vir.OpAtomicXor, vir.OpAtomicXchg,
	vir.OpCmpxchg, vir.OpFence,
}

var opcodeRangeCalls = []vir.Opcode{
	vir.OpCall, vir.OpSyscall, vir.OpVaStart, vir.OpVaArg, vir.OpVaEnd, vir.OpLoc,
}

var (
	opcodeNumber   = map[vir.Opcode]uint64{}
	opcodeByNumber = map[uint64]vir.Opcode{}
)

func init() {
	assign := func(base uint64, ops []vir.Opcode) {
		for i, op := range ops {
			n := base + uint64(i)
			opcodeNumber[op] = n
			opcodeByNumber[n] = op
		}
	}
	assign(0x0100, opcodeRangeMath)
	assign(0x0300, opcodeRangeConvVecIntrinsic)
	assign(0x0400, opcodeRangeMemory)
	assign(0x0500, opcodeRangeAtomics)
	assign(0x0600, opcodeRangeCalls)
}

// ---------------------------------------------------------------------------
// Function / declaration flag bits (§F4.2, §F4.3)
// ---------------------------------------------------------------------------

const (
	declFlagExport   byte = 1 << 0
	declFlagTLS      byte = 1 << 1 // GLOB only
	declFlagVariadic byte = 1 << 2 // FSIG only
)

const (
	fnFlagExport   uint64 = 1 << 0
	fnFlagNoReturn uint64 = 1 << 1
	fnFlagReadonly uint64 = 1 << 2
	fnFlagInline   uint64 = 1 << 3
	fnFlagNoInline uint64 = 1 << 4
	fnFlagCold     uint64 = 1 << 5
	fnFlagEntry    uint64 = 1 << 6
	fnFlagExternC  uint64 = 1 << 7
	fnFlagVariadic uint64 = 1 << 8
)

// Simpler attribute-only bitset, used for extern-group functions (which
// carry Attrs but no export/variadic bits of their own — Variadic is a
// separate bool field on vir.ExternFunction).
const (
	attrBitNoReturn uint64 = 1 << 0
	attrBitReadonly uint64 = 1 << 1
	attrBitInline   uint64 = 1 << 2
	attrBitNoInline uint64 = 1 << 3
	attrBitCold     uint64 = 1 << 4
	attrBitEntry    uint64 = 1 << 5
	attrBitExternC  uint64 = 1 << 6
)

func encodeAttrBits(attrs []vir.FunctionAttribute) uint64 {
	var v uint64
	for _, a := range attrs {
		switch a {
		case vir.AttributeNoReturn:
			v |= attrBitNoReturn
		case vir.AttributeReadonly:
			v |= attrBitReadonly
		case vir.AttributeInline:
			v |= attrBitInline
		case vir.AttributeNoInline:
			v |= attrBitNoInline
		case vir.AttributeCold:
			v |= attrBitCold
		case vir.AttributeEntry:
			v |= attrBitEntry
		case vir.AttributeExternC:
			v |= attrBitExternC
		}
	}
	return v
}

func decodeAttrBits(v uint64) []vir.FunctionAttribute {
	var out []vir.FunctionAttribute
	add := func(bit uint64, a vir.FunctionAttribute) {
		if v&bit != 0 {
			out = append(out, a)
		}
	}
	add(attrBitNoReturn, vir.AttributeNoReturn)
	add(attrBitReadonly, vir.AttributeReadonly)
	add(attrBitInline, vir.AttributeInline)
	add(attrBitNoInline, vir.AttributeNoInline)
	add(attrBitCold, vir.AttributeCold)
	add(attrBitEntry, vir.AttributeEntry)
	add(attrBitExternC, vir.AttributeExternC)
	return out
}

func encodeFuncFlags(f *vir.Function) uint64 {
	var v uint64
	if f.Export {
		v |= fnFlagExport
	}
	if f.Variadic {
		v |= fnFlagVariadic
	}
	for _, a := range f.Attrs {
		switch a {
		case vir.AttributeNoReturn:
			v |= fnFlagNoReturn
		case vir.AttributeReadonly:
			v |= fnFlagReadonly
		case vir.AttributeInline:
			v |= fnFlagInline
		case vir.AttributeNoInline:
			v |= fnFlagNoInline
		case vir.AttributeCold:
			v |= fnFlagCold
		case vir.AttributeEntry:
			v |= fnFlagEntry
		case vir.AttributeExternC:
			v |= fnFlagExternC
		}
	}
	return v
}

func decodeFuncFlags(v uint64) (export, variadic bool, attrs []vir.FunctionAttribute) {
	export = v&fnFlagExport != 0
	variadic = v&fnFlagVariadic != 0
	add := func(bit uint64, a vir.FunctionAttribute) {
		if v&bit != 0 {
			attrs = append(attrs, a)
		}
	}
	add(fnFlagNoReturn, vir.AttributeNoReturn)
	add(fnFlagReadonly, vir.AttributeReadonly)
	add(fnFlagInline, vir.AttributeInline)
	add(fnFlagNoInline, vir.AttributeNoInline)
	add(fnFlagCold, vir.AttributeCold)
	add(fnFlagEntry, vir.AttributeEntry)
	add(fnFlagExternC, vir.AttributeExternC)
	return
}

// Param attr byte (§F4.3).
const (
	paramAttrNone  byte = 0
	paramAttrByVal byte = 1
	paramAttrSRet  byte = 2
)

func alignTo8(n int) int { return (n + 7) &^ 7 }