package arm

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/arm/encoder"
)

// ---------------------------------------------------------------------------
// Function-level type inference (mirrors the verifier's result-type
// computation for the subset this backend supports; input is verified, so
// lookups cannot fail semantically).
// ---------------------------------------------------------------------------

type fnLower struct {
	*lowerer
	f     *vir.Function
	types map[string]vir.Type
	b     []Inst
	nlbl  int
}

func (fl *fnLower) emit(i Inst)             { fl.b = append(fl.b, i) }
func (fl *fnLower) alu(op string, d, s Opr) { fl.emit(Inst{Op: op, D: d, S: s}) }
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}

func (fl *fnLower) typeFunc() (map[string]vir.Type, error) {
	types := map[string]vir.Type{}
	for _, p := range fl.f.Params {
		if err := fl.checkValueType(p.Type); err != nil {
			return nil, err
		}
		types[p.Name] = p.Type
	}
	for _, b := range fl.f.AllBlocks() {
		for _, line := range b.Lines {
			if line.Instruction == nil {
				continue
			}
			in := line.Instruction
			if in.Op == "loc" || in.Result == "" {
				continue
			}
			if _, done := types[in.Result]; done {
				continue
			}
			rt, err := fl.resultType(in)
			if err != nil {
				return nil, err
			}
			if err := fl.checkValueType(rt); err != nil {
				return nil, fmt.Errorf("value %s: %w", in.Result, err)
			}
			types[in.Result] = rt
		}
	}
	return types, nil
}

func (fl *fnLower) checkValueType(t vir.Type) error {
	switch x := t.(type) {
	case vir.IntType:
		if x.Bits > 32 {
			return fmt.Errorf("i%d values not yet lowered on arm (register pairs TODO)", x.Bits)
		}
		return nil
	case vir.PtrType:
		return nil
	case vir.FloatType:
		return fmt.Errorf("floating-point lowering not implemented on arm (VFP tier TODO)")
	case vir.VecType:
		return fmt.Errorf("vector lowering not implemented on arm (NEON tier TODO, §10.4)")
	}
	return fmt.Errorf("type %s cannot be a named value on arm", t)
}

var voidOps = map[string]bool{
	"store": true, "store_vol": true, "atomic_store": true,
	"memcopy": true, "memmove": true, "memset": true,
	"fence": true, "prefetch": true, "masked_store": true, "scatter": true,
}
var cmpOps = map[string]bool{
	"eq": true, "ne": true, "slt": true, "sgt": true, "sle": true, "sge": true,
	"ult": true, "ugt": true, "ule": true, "uge": true,
	"lt": true, "gt": true, "le": true, "ge": true,
	"uaddo": true, "saddo": true, "usubo": true, "ssubo": true, "umulo": true, "smulo": true,
}

func (fl *fnLower) resultType(in *vir.Instruction) (vir.Type, error) {
	switch {
	case voidOps[in.Op]:
		return vir.Void, nil
	case cmpOps[in.Op]:
		return vir.I1, nil
	case in.Op == "call":
		if in.Sig != "" {
			for _, s := range fl.m.FunctionSignatures {
				if s.Name == in.Sig {
					return s.Ret, nil
				}
			}
			return nil, fmt.Errorf("fnsig %q not declared", in.Sig)
		}
		ret, _, ok := fl.lookupCallable(in.Args[0].Ident)
		if !ok {
			return nil, fmt.Errorf("callee %q not declared", in.Args[0].Ident)
		}
		return ret, nil
	case in.Suffix != nil:
		return in.Suffix, nil
	}
	return nil, fmt.Errorf("op %q has no result type", in.Op)
}

// ---------------------------------------------------------------------------
// Operand loading / storing
// ---------------------------------------------------------------------------

func bitsOf(t vir.Type) int {
	switch x := t.(type) {
	case vir.IntType:
		return x.Bits
	case vir.PtrType:
		return 32
	}
	return 32
}

func szOf(t vir.Type) int {
	b := bitsOf(t)
	switch {
	case b <= 8:
		return 1
	case b <= 16:
		return 2
	}
	return 4
}

func litBits(v int64, t vir.Type, signed bool) int64 {
	b := uint(bitsOf(t))
	if b >= 32 {
		return int64(int32(v))
	}
	mask := uint32(1)<<b - 1
	u := uint32(v) & mask
	if signed && u&(1<<(b-1)) != 0 {
		u |= ^mask
	}
	return int64(int32(u))
}

// load materializes operand o (of type t) into r as a 32-bit value.
func (fl *fnLower) load(o vir.Operand, t vir.Type, r encoder.Reg, signed bool) error {
	switch o.Kind {
	case vir.OperandInt:
		fl.emit(Inst{Op: "movimm", D: R(r), Imm: litBits(o.Int, t, signed)})
	case vir.OperandBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		fl.emit(Inst{Op: "movimm", D: R(r), Imm: v})
	case vir.OperandNull:
		fl.emit(Inst{Op: "movimm", D: R(r), Imm: 0})
	case vir.OperandFloat:
		return fmt.Errorf("float operands not lowered on arm (TODO)")
	case vir.OperandIdent:
		switch fl.kinds[o.Ident] {
		case "const":
			c := fl.consts[o.Ident]
			return fl.load(c.Value, c.Type, r, signed)
		case "global", "fn", "extern":
			fl.emit(Inst{Op: "movsym", D: R(r), Sym: o.Ident})
		case "struct", "fnsig":
			return fmt.Errorf("entity %q used as runtime operand", o.Ident)
		default:
			vt, ok := fl.types[o.Ident]
			if !ok {
				return fmt.Errorf("value %q has no type (isel bug)", o.Ident)
			}
			fl.emit(Inst{Op: "ldr", D: R(r), S: Slot(o.Ident)})
			if signed {
				switch szOf(vt) {
				case 1:
					fl.emit(Inst{Op: "sxtb", D: R(r), S: R(r)})
				case 2:
					fl.emit(Inst{Op: "sxth", D: R(r), S: R(r)})
				}
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on arm", o.Kind)
	}
	return nil
}

func (fl *fnLower) st(name string, r encoder.Reg) {
	fl.emit(Inst{Op: "str", D: Slot(name), S: R(r)})
}

func (fl *fnLower) norm(r encoder.Reg, t vir.Type) {
	if it, ok := t.(vir.IntType); ok && it.Bits == 1 {
		fl.alu("and", R(r), Imm(1))
		return
	}
	switch szOf(t) {
	case 1:
		fl.emit(Inst{Op: "uxtb", D: R(r), S: R(r)})
	case 2:
		fl.emit(Inst{Op: "uxth", D: R(r), S: R(r)})
	}
}

func (fl *fnLower) setcc(cc byte, r encoder.Reg) {
	fl.emit(Inst{Op: "movimm", D: R(r), Imm: 0})
	fl.emit(Inst{Op: "movcc", CC: cc, D: R(r), S: Imm(1)})
}

func (fl *fnLower) trapIf(cc byte) {
	ok := fl.label()
	fl.emit(Inst{Op: "bcc", CC: cc ^ 1, Lbl: ok})
	fl.emit(Inst{Op: "udf"})
	fl.emit(Inst{Op: "label", Lbl: ok})
}

// ---------------------------------------------------------------------------
// Instruction selection
// ---------------------------------------------------------------------------

func (fl *fnLower) selInst(in *vir.Instruction) error {
	op, t, a := in.Op, in.Suffix, in.Args
	signedCmp := map[string]byte{
		"slt": encoder.CondLT, "sle": encoder.CondLE, "sgt": encoder.CondGT, "sge": encoder.CondGE,
	}
	unsignedCmp := map[string]byte{
		"eq": encoder.CondEQ, "ne": encoder.CondNE, "ult": encoder.CondLO,
		"ule": encoder.CondLS, "ugt": encoder.CondHI, "uge": encoder.CondHS,
	}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		fl.st(in.Result, encoder.R0)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, false); err != nil {
			return err
		}
		armOp := map[string]string{"add": "add", "sub": "sub", "and": "and", "or": "orr", "xor": "eor"}[op]
		fl.alu(armOp, R(encoder.R0), R(encoder.R1))
		if op == "add" || op == "sub" {
			fl.norm(encoder.R0, t)
		}
		fl.st(in.Result, encoder.R0)

	case op == "mul":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mul", D: R(encoder.R2), S: R(encoder.R0), T: R(encoder.R1)})
		fl.norm(encoder.R2, t)
		fl.st(in.Result, encoder.R2)

	case op == "neg":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "rsb", D: R(encoder.R0), S: Imm(0)})
		fl.norm(encoder.R0, t)
		fl.st(in.Result, encoder.R0)

	case op == "not":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mvn", D: R(encoder.R0), S: R(encoder.R0)})
		fl.norm(encoder.R0, t)
		fl.st(in.Result, encoder.R0)

	case op == "abs":
		if err := fl.load(a[0], t, encoder.R0, true); err != nil {
			return err
		}
		fl.emit(Inst{Op: "asr", D: R(encoder.R1), S: R(encoder.R0), T: Imm(31)})
		fl.alu("eor", R(encoder.R0), R(encoder.R1))
		fl.alu("sub", R(encoder.R0), R(encoder.R1))
		fl.norm(encoder.R0, t)
		fl.st(in.Result, encoder.R0)

	case op == "udiv" || op == "urem" || op == "sdiv" || op == "srem":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, encoder.R0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(encoder.R1), Imm(0))
		fl.trapIf(encoder.CondEQ)
		div := "udiv"
		if signed {
			div = "sdiv"
			if szOf(t) == 4 {
				fl.alu("cmn", R(encoder.R1), Imm(1))
				skip := fl.label()
				fl.emit(Inst{Op: "bcc", CC: encoder.CondNE, Lbl: skip})
				fl.emit(Inst{Op: "movimm", D: R(encoder.R2), Imm: int64(int32(math.MinInt32))})
				fl.alu("cmp", R(encoder.R0), R(encoder.R2))
				fl.trapIf(encoder.CondEQ)
				fl.emit(Inst{Op: "label", Lbl: skip})
			}
			// TODO(§6.1): narrow INT_MIN/-1 wraps via the widened 32-bit
			// sdiv instead of trapping; same known gap as lower/x86.
		}
		fl.emit(Inst{Op: div, D: R(encoder.R2), S: R(encoder.R0), T: R(encoder.R1)})
		res := encoder.R2
		if op == "urem" || op == "srem" {
			fl.emit(Inst{Op: "mls", D: R(encoder.R3), S: R(encoder.R2), T: R(encoder.R1), X: R(encoder.R0)})
			res = encoder.R3
		}
		fl.norm(res, t)
		fl.st(in.Result, res)

	case op == "shl" || op == "lshr" || op == "ashr":
		signedV := op == "ashr"
		if err := fl.load(a[0], t, encoder.R0, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, false); err != nil {
			return err
		}
		fl.alu("and", R(encoder.R1), Imm(int64(bitsOf(t)-1)))
		armOp := map[string]string{"shl": "lsl", "lshr": "lsr", "ashr": "asr"}[op]
		fl.emit(Inst{Op: armOp, D: R(encoder.R0), S: R(encoder.R0), T: R(encoder.R1)})
		fl.norm(encoder.R0, t)
		fl.st(in.Result, encoder.R0)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, false); err != nil {
			return err
		}
		fl.alu("and", R(encoder.R1), Imm(int64(bitsOf(t)-1)))
		fl.emit(Inst{Op: "movimm", D: R(encoder.R2), Imm: int64(bitsOf(t))})
		fl.alu("sub", R(encoder.R2), R(encoder.R1))
		lo, hi := "lsr", "lsl"
		if op == "rotl" {
			lo, hi = "lsl", "lsr"
		}
		fl.emit(Inst{Op: lo, D: R(encoder.R3), S: R(encoder.R0), T: R(encoder.R1)})
		fl.emit(Inst{Op: hi, D: R(encoder.R2), S: R(encoder.R0), T: R(encoder.R2)})
		fl.alu("orr", R(encoder.R3), R(encoder.R2))
		fl.norm(encoder.R3, t)
		fl.st(in.Result, encoder.R3)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, encoder.R0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(encoder.R0), R(encoder.R1))
		cc := map[string]byte{
			"smin": encoder.CondGT, "smax": encoder.CondLT,
			"umin": encoder.CondHI, "umax": encoder.CondLO,
		}[op]
		fl.emit(Inst{Op: "movcc", CC: cc, D: R(encoder.R0), S: R(encoder.R1)})
		fl.norm(encoder.R0, t)
		fl.st(in.Result, encoder.R0)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, encoder.R0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(encoder.R0), R(encoder.R1))
		fl.setcc(cc, encoder.R2)
		fl.st(in.Result, encoder.R2)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on arm (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, encoder.R0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, signed); err != nil {
			return err
		}
		if szOf(t) == 4 {
			m := "umull"
			if signed {
				m = "smull"
			}
			fl.emit(Inst{Op: m, D: R(encoder.R2), X: R(encoder.R3), S: R(encoder.R0), T: R(encoder.R1)})
			fl.st(in.Result, encoder.R3)
		} else {
			fl.emit(Inst{Op: "mul", D: R(encoder.R2), S: R(encoder.R0), T: R(encoder.R1)})
			sh := "lsr"
			if signed {
				sh = "asr"
			}
			fl.emit(Inst{Op: sh, D: R(encoder.R2), S: R(encoder.R2), T: Imm(int64(bitsOf(t)))})
			fl.norm(encoder.R2, t)
			fl.st(in.Result, encoder.R2)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on arm (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "clz", D: R(encoder.R0), S: R(encoder.R0)})
		if bitsOf(t) < 32 {
			fl.alu("sub", R(encoder.R0), Imm(int64(32-bitsOf(t))))
		}
		fl.st(in.Result, encoder.R0)

	case op == "cttz":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "rbit", D: R(encoder.R0), S: R(encoder.R0)})
		fl.emit(Inst{Op: "clz", D: R(encoder.R0), S: R(encoder.R0)})
		if bitsOf(t) < 32 {
			fl.emit(Inst{Op: "movimm", D: R(encoder.R1), Imm: int64(bitsOf(t))})
			fl.alu("cmp", R(encoder.R0), R(encoder.R1))
			fl.emit(Inst{Op: "movcc", CC: encoder.CondHI, D: R(encoder.R0), S: R(encoder.R1)})
		}
		fl.st(in.Result, encoder.R0)

	case op == "popcnt":
		return fmt.Errorf("popcnt has no scalar A32 instruction (NEON vcnt tier TODO, §10.4)")

	case op == "bitrev":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "rbit", D: R(encoder.R0), S: R(encoder.R0)})
		if bitsOf(t) < 32 {
			fl.emit(Inst{Op: "lsr", D: R(encoder.R0), S: R(encoder.R0), T: Imm(int64(32 - bitsOf(t)))})
		}
		fl.st(in.Result, encoder.R0)

	case op == "bswap":
		if err := fl.load(a[0], t, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "rev", D: R(encoder.R0), S: R(encoder.R0)})
		if szOf(t) == 2 {
			fl.emit(Inst{Op: "lsr", D: R(encoder.R0), S: R(encoder.R0), T: Imm(16)})
		}
		fl.st(in.Result, encoder.R0)

	case op == "select":
		if err := fl.load(a[0], vir.I1, encoder.R0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, encoder.R2, false); err != nil {
			return err
		}
		fl.alu("cmp", R(encoder.R0), Imm(0))
		fl.emit(Inst{Op: "movcc", CC: encoder.CondEQ, D: R(encoder.R1), S: R(encoder.R2)})
		fl.st(in.Result, encoder.R1)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, encoder.R1, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 1:
			fl.emit(Inst{Op: "ldrb", D: R(encoder.R0), S: Mem(encoder.R1, 0)})
		case 2:
			fl.emit(Inst{Op: "ldrh", D: R(encoder.R0), S: Mem(encoder.R1, 0)})
		default:
			fl.emit(Inst{Op: "ldr", D: R(encoder.R0), S: Mem(encoder.R1, 0)})
		}
		if op == "atomic_load" {
			switch lastOrd(a) {
			case "acquire", "seqcst":
				fl.emit(Inst{Op: "dmb"})
			}
		}
		fl.st(in.Result, encoder.R0)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, encoder.R1, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R0, false); err != nil {
			return err
		}
		if op == "atomic_store" {
			switch lastOrd(a) {
			case "release", "seqcst":
				fl.emit(Inst{Op: "dmb"})
			}
		}
		switch szOf(t) {
		case 1:
			fl.emit(Inst{Op: "strb", D: Mem(encoder.R1, 0), S: R(encoder.R0)})
		case 2:
			fl.emit(Inst{Op: "strh", D: Mem(encoder.R1, 0), S: R(encoder.R0)})
		default:
			fl.emit(Inst{Op: "str", D: Mem(encoder.R1, 0), S: R(encoder.R0)})
		}
		if op == "atomic_store" && lastOrd(a) == "seqcst" {
			fl.emit(Inst{Op: "dmb"})
		}

	case op == "alloca":
		if err := fl.load(a[0], vir.I32, encoder.R0, false); err != nil {
			return err
		}
		fl.alu("add", R(encoder.R0), Imm(7)) // round size up, keep SP 8-aligned (AAPCS)
		fl.alu("bic", R(encoder.R0), Imm(7))
		// isa/arm/encoder has no sub_sp_r/and_sp/mov_r_sp pseudo-ops (unlike
		// the old mcode.Encode) — RSP is an ordinary register operand to
		// plain sub/and/mov_r.
		fl.emit(Inst{Op: "sub", D: R(encoder.RSP), S: R(encoder.R0)})
		if in.Align > 8 {
			fl.emit(Inst{Op: "and", D: R(encoder.RSP), S: Imm(int64(-in.Align))})
		}
		fl.emit(Inst{Op: "mov_r", D: R(encoder.R0), S: R(encoder.RSP)})
		fl.st(in.Result, encoder.R0)

	case op == "field":
		off, err := fl.lay.FieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, encoder.R0, false); err != nil {
			return err
		}
		if off != 0 {
			fl.emit(Inst{Op: "movimm", D: R(encoder.R1), Imm: int64(off)})
			fl.alu("add", R(encoder.R0), R(encoder.R1))
		}
		fl.st(in.Result, encoder.R0)

	case op == "index":
		esz, err := fl.lay.Size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, encoder.R0, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, encoder.R1, true); err != nil {
			return err
		}
		fl.emit(Inst{Op: "movimm", D: R(encoder.R2), Imm: int64(esz)})
		fl.emit(Inst{Op: "mul", D: R(encoder.R3), S: R(encoder.R1), T: R(encoder.R2)})
		fl.alu("add", R(encoder.R0), R(encoder.R3))
		fl.st(in.Result, encoder.R0)

	case op == "memcopy" || op == "memset":
		// No rep-string hardware: index loop over r12 (IP). dst r0, src/byte
		// r1, len r2, scratch r3. ldrb_r/strb_r take the loop index folded
		// into a MemIndexed operand (isa/arm/encoder's shape), not three
		// bare registers the way the old mcode.Encode read them.
		if err := fl.load(a[0], vir.Ptr, encoder.R0, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, encoder.R1, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, encoder.R1, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I32, encoder.R2, false); err != nil {
			return err
		}
		loop, done := fl.label(), fl.label()
		fl.emit(Inst{Op: "movimm", D: R(encoder.RIP), Imm: 0})
		fl.emit(Inst{Op: "label", Lbl: loop})
		fl.alu("cmp", R(encoder.RIP), R(encoder.R2))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondEQ, Lbl: done})
		if op == "memcopy" {
			fl.emit(Inst{Op: "ldrb_r", D: R(encoder.R3), S: MemIndexed(encoder.R1, encoder.RIP)})
			fl.emit(Inst{Op: "strb_r", D: MemIndexed(encoder.R0, encoder.RIP), S: R(encoder.R3)})
		} else {
			fl.emit(Inst{Op: "strb_r", D: MemIndexed(encoder.R0, encoder.RIP), S: R(encoder.R1)})
		}
		fl.alu("add", R(encoder.RIP), Imm(1))
		fl.emit(Inst{Op: "b", Lbl: loop})
		fl.emit(Inst{Op: "label", Lbl: done})

	case op == "memmove":
		if err := fl.load(a[0], vir.Ptr, encoder.R0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, encoder.R1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, encoder.R2, false); err != nil {
			return err
		}
		back, bloop, floop, done := fl.label(), fl.label(), fl.label(), fl.label()
		fl.alu("cmp", R(encoder.R1), R(encoder.R0))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondLO, Lbl: back}) // src < dst: copy backward
		fl.emit(Inst{Op: "movimm", D: R(encoder.RIP), Imm: 0})
		fl.emit(Inst{Op: "label", Lbl: floop})
		fl.alu("cmp", R(encoder.RIP), R(encoder.R2))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondEQ, Lbl: done})
		fl.emit(Inst{Op: "ldrb_r", D: R(encoder.R3), S: MemIndexed(encoder.R1, encoder.RIP)})
		fl.emit(Inst{Op: "strb_r", D: MemIndexed(encoder.R0, encoder.RIP), S: R(encoder.R3)})
		fl.alu("add", R(encoder.RIP), Imm(1))
		fl.emit(Inst{Op: "b", Lbl: floop})
		fl.emit(Inst{Op: "label", Lbl: back})
		fl.emit(Inst{Op: "mov_r", D: R(encoder.RIP), S: R(encoder.R2)})
		fl.emit(Inst{Op: "label", Lbl: bloop})
		fl.alu("cmp", R(encoder.RIP), Imm(0))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondEQ, Lbl: done})
		fl.alu("sub", R(encoder.RIP), Imm(1))
		fl.emit(Inst{Op: "ldrb_r", D: R(encoder.R3), S: MemIndexed(encoder.R1, encoder.RIP)})
		fl.emit(Inst{Op: "strb_r", D: MemIndexed(encoder.R0, encoder.RIP), S: R(encoder.R3)})
		fl.emit(Inst{Op: "b", Lbl: bloop})
		fl.emit(Inst{Op: "label", Lbl: done})

	case op == "prefetch":
		return nil

	case op == "fence":
		fl.emit(Inst{Op: "dmb"})
		return nil

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_and" ||
		op == "atomic_or" || op == "atomic_xor" || op == "atomic_xchg":
		if szOf(t) != 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on arm (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, encoder.R2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "dmb"})
		retry := fl.label()
		fl.emit(Inst{Op: "label", Lbl: retry})
		fl.emit(Inst{Op: "ldrex", D: R(encoder.R0), S: Mem(encoder.R2, 0)})
		switch op {
		case "atomic_xchg":
			fl.emit(Inst{Op: "mov_r", D: R(encoder.R3), S: R(encoder.R1)})
		case "atomic_add", "atomic_sub":
			fl.emit(Inst{Op: "mov_r", D: R(encoder.R3), S: R(encoder.R0)})
			armOp := "add"
			if op == "atomic_sub" {
				armOp = "sub"
			}
			fl.alu(armOp, R(encoder.R3), R(encoder.R1))
		default:
			fl.emit(Inst{Op: "mov_r", D: R(encoder.R3), S: R(encoder.R0)})
			fl.alu(map[string]string{"atomic_and": "and", "atomic_or": "orr", "atomic_xor": "eor"}[op], R(encoder.R3), R(encoder.R1))
		}
		fl.emit(Inst{Op: "strex", X: R(encoder.RIP), S: R(encoder.R3), D: Mem(encoder.R2, 0)})
		fl.alu("cmp", R(encoder.RIP), Imm(0))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondNE, Lbl: retry})
		fl.emit(Inst{Op: "dmb"})
		fl.st(in.Result, encoder.R0)

	case op == "cmpxchg":
		if szOf(t) != 4 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on arm (TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, encoder.R2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.R1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, encoder.R3, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "dmb"})
		retry, fail, done := fl.label(), fl.label(), fl.label()
		fl.emit(Inst{Op: "label", Lbl: retry})
		fl.emit(Inst{Op: "ldrex", D: R(encoder.R0), S: Mem(encoder.R2, 0)})
		fl.alu("cmp", R(encoder.R0), R(encoder.R1))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondNE, Lbl: fail})
		fl.emit(Inst{Op: "strex", X: R(encoder.RIP), S: R(encoder.R3), D: Mem(encoder.R2, 0)})
		fl.alu("cmp", R(encoder.RIP), Imm(0))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondNE, Lbl: retry})
		fl.emit(Inst{Op: "b", Lbl: done})
		fl.emit(Inst{Op: "label", Lbl: fail})
		fl.emit(Inst{Op: "clrex"})
		fl.emit(Inst{Op: "label", Lbl: done})
		fl.emit(Inst{Op: "dmb"})
		fl.st(in.Result, encoder.R0)

	case op == "trunc":
		if err := fl.load(a[0], nil, encoder.R0, false); err != nil {
			return err
		}
		fl.norm(encoder.R0, t)
		fl.st(in.Result, encoder.R0)

	case op == "zext":
		if err := fl.load(a[0], nil, encoder.R0, false); err != nil {
			return err
		}
		fl.st(in.Result, encoder.R0)

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if it, ok := st.(vir.IntType); ok && it.Bits == 1 {
			if err := fl.load(a[0], st, encoder.R0, false); err != nil {
				return err
			}
			fl.emit(Inst{Op: "rsb", D: R(encoder.R0), S: Imm(0)})
		} else {
			if err := fl.load(a[0], st, encoder.R0, true); err != nil {
				return err
			}
		}
		fl.norm(encoder.R0, t)
		fl.st(in.Result, encoder.R0)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on arm (TODO)")
		}
		if err := fl.load(a[0], st, encoder.R0, false); err != nil {
			return err
		}
		fl.st(in.Result, encoder.R0)

	case op == "call":
		return fl.selCall(in)

	case op == "fdemote" || op == "fpromote" || op == "sfromint" || op == "ufromint" ||
		op == "stoint" || op == "utoint" || op == "stoint_sat" || op == "utoint_sat" ||
		op == "sqrt" || op == "fma" || op == "copysign" || op == "floor" || op == "ceil" ||
		op == "trunc_f" || op == "nearest" || op == "min" || op == "max":
		return fmt.Errorf("floating-point op %q not lowered on arm (VFP tier TODO)", op)

	case op == "splat" || op == "extract" || op == "insert" || op == "shuffle" ||
		op == "masked_load" || op == "masked_store" || op == "gather" || op == "scatter" ||
		op == "reduce_add" || op == "reduce_min" || op == "reduce_max" ||
		op == "reduce_and" || op == "reduce_or" || op == "reduce_xor":
		return fmt.Errorf("vector op %q not lowered on arm (NEON tier TODO, §10.4)", op)

	case op == "syscall":
		return fmt.Errorf("syscalls not lowered on arm (no syscall ABI convention yet, TODO)")

	default:
		return fmt.Errorf("op %q not lowered on arm", op)
	}
	return nil
}

func (fl *fnLower) selOverflow(in *vir.Instruction) error {
	t, a := in.Suffix, in.Args
	signed := in.Op[0] == 's'
	if err := fl.load(a[0], t, encoder.R0, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, encoder.R1, signed); err != nil {
		return err
	}
	if szOf(t) == 4 {
		switch in.Op {
		case "uaddo":
			fl.emit(Inst{Op: "adds", D: R(encoder.R0), S: R(encoder.R1)})
			fl.setcc(encoder.CondHS, encoder.R2)
		case "usubo":
			fl.emit(Inst{Op: "subs", D: R(encoder.R0), S: R(encoder.R1)})
			fl.setcc(encoder.CondLO, encoder.R2)
		case "saddo":
			fl.emit(Inst{Op: "adds", D: R(encoder.R0), S: R(encoder.R1)})
			fl.setcc(encoder.CondVS, encoder.R2)
		case "ssubo":
			fl.emit(Inst{Op: "subs", D: R(encoder.R0), S: R(encoder.R1)})
			fl.setcc(encoder.CondVS, encoder.R2)
		case "umulo":
			fl.emit(Inst{Op: "umull", D: R(encoder.R2), X: R(encoder.R3), S: R(encoder.R0), T: R(encoder.R1)})
			fl.alu("cmp", R(encoder.R3), Imm(0))
			fl.setcc(encoder.CondNE, encoder.R2)
		case "smulo":
			fl.emit(Inst{Op: "smull", D: R(encoder.R2), X: R(encoder.R3), S: R(encoder.R0), T: R(encoder.R1)})
			fl.emit(Inst{Op: "cmp_asr31", D: R(encoder.R3), S: R(encoder.R2)})
			fl.setcc(encoder.CondNE, encoder.R2)
		}
	} else {
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(encoder.R0), R(encoder.R1))
		case "usubo", "ssubo":
			fl.alu("sub", R(encoder.R0), R(encoder.R1))
		case "umulo", "smulo":
			fl.emit(Inst{Op: "mul", D: R(encoder.R2), S: R(encoder.R0), T: R(encoder.R1)})
			fl.emit(Inst{Op: "mov_r", D: R(encoder.R0), S: R(encoder.R2)})
		}
		ext, sz := "uxtb", szOf(t)
		if signed {
			ext = "sxtb"
		}
		if sz == 2 {
			ext = "uxth"
			if signed {
				ext = "sxth"
			}
		}
		fl.emit(Inst{Op: "mov_r", D: R(encoder.R1), S: R(encoder.R0)})
		fl.norm(encoder.R1, t)
		fl.emit(Inst{Op: ext, D: R(encoder.R1), S: R(encoder.R1)})
		fl.alu("cmp", R(encoder.R1), R(encoder.R0))
		fl.setcc(encoder.CondNE, encoder.R2)
	}
	fl.st(in.Result, encoder.R2)
	return nil
}

func (fl *fnLower) typeOfOperand(o vir.Operand) (vir.Type, error) {
	if o.Kind != vir.OperandIdent {
		return nil, fmt.Errorf("conversion source must be a named value or const on arm")
	}
	switch fl.kinds[o.Ident] {
	case "const":
		return fl.consts[o.Ident].Type, nil
	case "global", "fn", "extern":
		return vir.Ptr, nil
	}
	t, ok := fl.types[o.Ident]
	if !ok {
		return nil, fmt.Errorf("value %q has no type", o.Ident)
	}
	return t, nil
}

func lastOrd(args []vir.Operand) string {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i].Kind == vir.OperandOrdering {
			return args[i].Ordering
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Calls (AAPCS) and terminators
// ---------------------------------------------------------------------------

// selCall stages every argument in a stack area, then lifts the first four
// into r0-r3 and releases the staging bytes that duplicated them, leaving
// any remaining arguments contiguous at SP for the call. Caller cleans up.
// SP adjustment is ordinary add/sub on RSP — isa/arm/encoder has no
// sub_sp/add_sp pseudo-ops.
func (fl *fnLower) selCall(in *vir.Instruction) error {
	args := in.Args
	var ret vir.Type
	indirect := in.Sig != ""
	if indirect {
		found := false
		for _, s := range fl.m.FunctionSignatures {
			if s.Name == in.Sig {
				ret, found = s.Ret, true
			}
		}
		if !found {
			return fmt.Errorf("fnsig %q not declared", in.Sig)
		}
		args = args[1:]
	} else {
		var params []vir.Param
		ret, params, _ = fl.lookupCallable(args[0].Ident)
		for _, p := range params {
			if p.ByVal != "" || p.SRet != "" {
				return fmt.Errorf("byval/sret call arguments not yet lowered on arm (TODO)")
			}
		}
		args = args[1:]
	}
	if !vir.IsVoid(ret) {
		if err := fl.checkValueType(ret); err != nil {
			return err
		}
	}

	stage := int64((4*len(args) + 7) &^ 7)
	if stage > 0 {
		fl.emit(Inst{Op: "sub", D: R(encoder.RSP), S: Imm(stage)})
	}
	for i, a := range args {
		if err := fl.load(a, nil, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "str", D: Mem(encoder.RSP, int32(4*i)), S: R(encoder.R0)})
	}
	if indirect { // callee ptr survives in IP across the register loads
		if err := fl.load(in.Args[0], vir.Ptr, encoder.RIP, false); err != nil {
			return err
		}
	}
	nreg := len(args)
	if nreg > 4 {
		nreg = 4
	}
	for i := 0; i < nreg; i++ {
		fl.emit(Inst{Op: "ldr", D: R(encoder.Reg(i)), S: Mem(encoder.RSP, int32(4*i))})
	}
	cleanup := stage
	if len(args) > 4 {
		fl.emit(Inst{Op: "add", D: R(encoder.RSP), S: Imm(16)}) // stack args now start at SP
		cleanup = stage - 16
	} else if stage > 0 {
		fl.emit(Inst{Op: "add", D: R(encoder.RSP), S: Imm(stage)})
		cleanup = 0
	}
	if indirect {
		fl.emit(Inst{Op: "blx_r", S: R(encoder.RIP)})
	} else {
		fl.emit(Inst{Op: "bl_sym", Sym: in.Args[0].Ident})
	}
	if cleanup > 0 {
		fl.emit(Inst{Op: "add", D: R(encoder.RSP), S: Imm(cleanup)}) // caller cleans up
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.norm(encoder.R0, ret)
		fl.st(in.Result, encoder.R0)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		fl.emit(Inst{Op: "b", Lbl: x.Label})
	case vir.BranchIf:
		if err := fl.load(x.Cond, vir.I1, encoder.R0, false); err != nil {
			return err
		}
		fl.alu("cmp", R(encoder.R0), Imm(0))
		fl.emit(Inst{Op: "bcc", CC: encoder.CondNE, Lbl: x.Then})
		fl.emit(Inst{Op: "b", Lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I32
		}
		if err := fl.load(x.Value, vt, encoder.R0, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.emit(Inst{Op: "movimm", D: R(encoder.R1), Imm: litBits(c.Value, vt, false)})
			fl.alu("cmp", R(encoder.R0), R(encoder.R1))
			fl.emit(Inst{Op: "bcc", CC: encoder.CondEQ, Lbl: c.Label})
		}
		fl.emit(Inst{Op: "b", Lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, encoder.R0, false); err != nil {
				return err
			}
		}
		fl.emit(Inst{Op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(Inst{Op: "udf"})
	case vir.Unreachable:
		fl.emit(Inst{Op: "udf"})
	default:
		return fmt.Errorf("terminator %T not lowered on arm", t)
	}
	return nil
}

// selTailCall implements guaranteed tail calls (§5) for the eligible shape
// this backend supports: at most four arguments, all in registers.
func (fl *fnLower) selTailCall(x vir.TailCall) error {
	args := x.Args
	indirect := x.Callee == ""
	if indirect {
		args = args[1:]
	}
	if len(args) > 4 {
		return fmt.Errorf("tailcall with %d args exceeds the r0-r3 register set (stack-arg tailcalls TODO)", len(args))
	}
	stage := int64((4*len(args) + 7) &^ 7)
	if stage > 0 {
		fl.emit(Inst{Op: "sub", D: R(encoder.RSP), S: Imm(stage)})
	}
	for i, a := range args {
		if err := fl.load(a, nil, encoder.R0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "str", D: Mem(encoder.RSP, int32(4*i)), S: R(encoder.R0)})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, encoder.RIP, false); err != nil {
			return err
		}
	}
	for i := range args {
		fl.emit(Inst{Op: "ldr", D: R(encoder.Reg(i)), S: Mem(encoder.RSP, int32(4*i))})
	}
	if stage > 0 {
		fl.emit(Inst{Op: "add", D: R(encoder.RSP), S: Imm(stage)})
	}
	if indirect {
		fl.emit(Inst{Op: "epi_jmp_r", S: R(encoder.RIP)}) // IP survives the epilogue
	} else {
		fl.emit(Inst{Op: "epi_jmp_sym", Sym: x.Callee})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Globals (static data + relocations). Scalars are serialized in the
// requested arch's byte order; layout offsets are identical either way.
// ---------------------------------------------------------------------------

func (lw *lowerer) lowerGlobal(g *vir.Global) (Global, error) {
	sz, err := lw.lay.Size(g.Type)
	if err != nil {
		return Global{}, err
	}
	al, err := lw.lay.AlignOf(g.Type)
	if err != nil {
		return Global{}, err
	}
	if g.Align > al {
		al = g.Align
	}
	out := Global{Name: g.Name, Size: uint32(sz), Align: uint32(al), Export: g.Export, TLS: g.TLS}
	if _, zero := g.Init.(vir.InitZero); zero {
		return out, nil
	}
	w := &dataw{lay: lw.lay, be: lw.arch.Big()}
	if err := w.emit(g.Init, g.Type); err != nil {
		return Global{}, err
	}
	for len(w.b) < sz {
		w.b = append(w.b, 0)
	}
	out.Data, out.Fixups = w.b, w.fx
	return out, nil
}

type dataw struct {
	lay *Layout
	be  bool
	b   []byte
	fx  []Fixup
}

func (w *dataw) pad(to int) {
	for len(w.b) < to {
		w.b = append(w.b, 0)
	}
}

func (w *dataw) scalar(v uint64, n int) {
	if w.be {
		for i := n - 1; i >= 0; i-- {
			w.b = append(w.b, byte(v>>(8*i)))
		}
		return
	}
	for i := 0; i < n; i++ {
		w.b = append(w.b, byte(v>>(8*i)))
	}
}

func (w *dataw) emit(init vir.ConstInit, t vir.Type) error {
	switch x := init.(type) {
	case vir.InitZero:
		sz, err := w.lay.Size(t)
		if err != nil {
			return err
		}
		w.pad(len(w.b) + sz)
		return nil
	case vir.InitByteString:
		w.b = append(w.b, x.Data...)
		return nil
	case vir.InitAddressOf:
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: x.Name, Kind: FixupAbs32})
		w.scalar(0, 4)
		return nil
	case vir.InitLiteral:
		return w.lit(x.Value, t)
	case vir.InitAggregate:
		switch tt := t.(type) {
		case vir.StructType:
			base := len(w.b)
			sz, _, offs, err := w.lay.StructLayout(tt.Name)
			if err != nil {
				return err
			}
			s := w.lay.StructByName(tt.Name)
			for i, e := range x.Elems {
				w.pad(base + offs[s.Fields[i].Name])
				if err := w.emit(e, s.Fields[i].Type); err != nil {
					return err
				}
			}
			w.pad(base + sz)
			return nil
		case vir.ArrayType:
			base := len(w.b)
			es, err := w.lay.Size(tt.Elem)
			if err != nil {
				return err
			}
			for _, e := range x.Elems {
				if err := w.emit(e, tt.Elem); err != nil {
					return err
				}
			}
			w.pad(base + es*tt.Len)
			return nil
		}
		return fmt.Errorf("aggregate initializer for %s", t)
	}
	return fmt.Errorf("unknown initializer form")
}

func (w *dataw) lit(o vir.Operand, t vir.Type) error {
	switch o.Kind {
	case vir.OperandInt:
		sz, err := w.lay.Size(t)
		if err != nil {
			return err
		}
		w.scalar(uint64(o.Int), sz)
		return nil
	case vir.OperandBool:
		v := uint64(0)
		if o.Bool {
			v = 1
		}
		w.scalar(v, 1)
		return nil
	case vir.OperandNull:
		w.scalar(0, 4)
		return nil
	case vir.OperandFloat:
		switch t {
		case vir.F64:
			w.scalar(math.Float64bits(o.Float), 8)
			return nil
		case vir.F32:
			w.scalar(uint64(math.Float32bits(float32(o.Float))), 4)
			return nil
		}
		return fmt.Errorf("f16 initializers not yet emitted on arm (TODO)")
	case vir.OperandVector:
		vt, ok := t.(vir.VecType)
		if !ok {
			return fmt.Errorf("vector literal for %s", t)
		}
		es, err := w.lay.Size(vt.Elem)
		if err != nil {
			return err
		}
		for _, v := range o.Vector {
			w.scalar(uint64(v), es)
		}
		return nil
	}
	return fmt.Errorf("literal kind %d in initializer", o.Kind)
}