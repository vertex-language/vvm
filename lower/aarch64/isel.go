package aarch64

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/isa/aarch64/encoder"
)

// rIDX is this package's own scratch-register choice for the loop index
// used by memcopy/memset/memmove's byte-copy loops — a lowering decision
// (which otherwise-unused register to dedicate to this purpose), not an
// ISA fact, so it's declared here rather than smuggled into isa/aarch64.
const rIDX = encoder.X15

// typeFunc mirrors the verifier's result-type computation for the subset
// this backend supports, including asm out-bindings (§6 rule 6: a
// first-seen out ident's type is inferred from its bound register's
// width).
func (fl *fnLower) typeFunc() (map[string]vir.Type, error) {
	types := map[string]vir.Type{}
	for _, p := range fl.f.Params {
		if err := fl.checkValueType(p.Type); err != nil {
			return nil, err
		}
		types[p.Name] = p.Type
	}
	for _, b := range fl.f.AllBlocks() {
		for i := range b.Lines {
			line := &b.Lines[i]
			if line.Instruction != nil {
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
	}
	regTable := vir.RegisterTableForArchitecture(string(fl.arch))
	if regTable != nil {
		for _, b := range fl.f.AllBlocks() {
			for i := range b.Lines {
				asmBlk := b.Lines[i].Asm
				if asmBlk == nil {
					continue
				}
				for _, bind := range asmBlk.Bindings {
					if bind.Kind != vir.BindingOut {
						continue
					}
					if _, done := types[bind.Ident]; done {
						continue
					}
					info, ok := regTable[bind.Register]
					if !ok {
						continue
					}
					if info.WidthBits > 32 {
						types[bind.Ident] = vir.I64
					} else {
						types[bind.Ident] = vir.I32
					}
				}
			}
		}
	}
	return types, nil
}

func (fl *fnLower) checkValueType(t vir.Type) error {
	switch x := t.(type) {
	case vir.IntType:
		if x.Bits > 64 {
			return fmt.Errorf("i%d values not yet lowered on aarch64 (register pairs TODO)", x.Bits)
		}
		return nil
	case vir.PtrType:
		return nil
	case vir.FloatType:
		return fmt.Errorf("floating-point lowering not implemented on aarch64 (FP/SIMD tier TODO)")
	case vir.VecType:
		return fmt.Errorf("vector lowering not implemented on aarch64 (NEON/SVE tier TODO, §10.4)")
	}
	return fmt.Errorf("type %s cannot be a named value on aarch64", t)
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
	case in.Op == "syscall":
		return in.Suffix, nil
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
		return 64
	case nil:
		return 64
	}
	return 64
}

func szMachine(t vir.Type) int {
	if bitsOf(t) > 32 {
		return 8
	}
	return 4
}

func szOf(t vir.Type) int {
	b := bitsOf(t)
	switch {
	case b <= 8:
		return 1
	case b <= 16:
		return 2
	case b <= 32:
		return 4
	}
	return 8
}

func litBits(v int64, t vir.Type, signed bool) int64 {
	b := uint(bitsOf(t))
	if b >= 64 {
		return v
	}
	mask := uint64(1)<<b - 1
	u := uint64(v) & mask
	if signed && u&(1<<(b-1)) != 0 {
		if b <= 32 {
			u |= 0xFFFFFFFF &^ mask
		} else {
			u |= ^mask
		}
	}
	return int64(u)
}

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
		return fmt.Errorf("float operands not lowered on aarch64 (TODO)")
	case vir.OperandIdent:
		switch fl.kinds[o.Ident] {
		case "const":
			c := fl.consts[o.Ident]
			return fl.load(c.Value, c.Type, r, signed)
		case "global", "fn", "extern":
			if fl.tls[o.Ident] {
				return fmt.Errorf("address of tls global %q not lowered on aarch64 (TPIDR_EL0 + TLS relocs TODO)", o.Ident)
			}
			fl.emit(Inst{Op: "movsym", D: R(r), Sym: o.Ident})
		case "struct", "fnsig":
			return fmt.Errorf("entity %q used as runtime operand", o.Ident)
		default:
			vt, ok := fl.types[o.Ident]
			if !ok {
				return fmt.Errorf("value %q has no type (isel bug)", o.Ident)
			}
			fl.emit(Inst{Op: "ldr", D: R(r), S: Slot(o.Ident), Sz: 8})
			if signed {
				switch bitsOf(vt) {
				case 1:
					fl.emit(Inst{Op: "sxt1", D: R(r), S: R(r), Sz: 4})
				case 8:
					fl.emit(Inst{Op: "sxtb", D: R(r), S: R(r), Sz: 4})
				case 16:
					fl.emit(Inst{Op: "sxth", D: R(r), S: R(r), Sz: 4})
				}
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on aarch64", o.Kind)
	}
	return nil
}

func (fl *fnLower) st(name string, r encoder.Reg) {
	fl.emit(Inst{Op: "str", D: Slot(name), S: R(r), Sz: 8})
}

func (fl *fnLower) norm(r encoder.Reg, t vir.Type) {
	switch bitsOf(t) {
	case 1:
		fl.emit(Inst{Op: "and1", D: R(r), S: R(r)})
	case 8:
		fl.emit(Inst{Op: "uxtb", D: R(r), S: R(r)})
	case 16:
		fl.emit(Inst{Op: "uxth", D: R(r), S: R(r)})
	}
}

func (fl *fnLower) normReg(r encoder.Reg, t vir.Type) {
	switch bitsOf(t) {
	case 32:
		fl.emit(Inst{Op: "mov_r", D: R(r), S: R(r), Sz: 4})
	default:
		fl.norm(r, t)
	}
}

func (fl *fnLower) setcc(cc byte, r encoder.Reg) {
	fl.emit(Inst{Op: "cset", Cc: cc, D: R(r)})
}

func (fl *fnLower) trapIf(cc byte) {
	ok := fl.label()
	fl.emit(Inst{Op: "bcc", Cc: encoder.Invert(cc), Lbl: ok})
	fl.emit(Inst{Op: "brk"})
	fl.emit(Inst{Op: "label", Lbl: ok})
}

// ---------------------------------------------------------------------------
// Instruction selection
// ---------------------------------------------------------------------------

func lastOrd(args []vir.Operand) string {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i].Kind == vir.OperandOrdering {
			return args[i].Ordering
		}
	}
	return ""
}

func (fl *fnLower) selInst(in *vir.Instruction) error {
	op, t, a := in.Op, in.Suffix, in.Args
	sz := szMachine(t)
	signedCmp := map[string]byte{"slt": encoder.CondLT, "sle": encoder.CondLE, "sgt": encoder.CondGT, "sge": encoder.CondGE}
	unsignedCmp := map[string]byte{"eq": encoder.CondEQ, "ne": encoder.CondNE, "ult": encoder.CondLO, "ule": encoder.CondLS, "ugt": encoder.CondHI, "uge": encoder.CondHS}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		fl.st(in.Result, encoder.X0)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, false); err != nil {
			return err
		}
		armOp := map[string]string{"add": "add", "sub": "sub", "and": "and", "or": "orr", "xor": "eor"}[op]
		fl.alu(armOp, sz, R(encoder.X0), R(encoder.X1))
		if op == "add" || op == "sub" {
			fl.norm(encoder.X0, t)
		}
		fl.st(in.Result, encoder.X0)

	case op == "mul":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mul", Sz: sz, D: R(encoder.X2), S: R(encoder.X0), T: R(encoder.X1)})
		fl.norm(encoder.X2, t)
		fl.st(in.Result, encoder.X2)

	case op == "neg":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "neg", Sz: sz, D: R(encoder.X0), S: R(encoder.X0)})
		fl.norm(encoder.X0, t)
		fl.st(in.Result, encoder.X0)

	case op == "not":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mvn", Sz: sz, D: R(encoder.X0), S: R(encoder.X0)})
		fl.norm(encoder.X0, t)
		fl.st(in.Result, encoder.X0)

	case op == "abs":
		if err := fl.load(a[0], t, encoder.X0, true); err != nil {
			return err
		}
		mn := int64(8*sz - 1)
		fl.emit(Inst{Op: "asr_i", Sz: sz, D: R(encoder.X1), S: R(encoder.X0), Imm: mn})
		fl.alu("eor", sz, R(encoder.X0), R(encoder.X1))
		fl.alu("sub", sz, R(encoder.X0), R(encoder.X1))
		fl.norm(encoder.X0, t)
		fl.st(in.Result, encoder.X0)

	case op == "udiv" || op == "urem" || op == "sdiv" || op == "srem":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, encoder.X0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, signed); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cmp", Sz: sz, D: R(encoder.X1), S: Imm(0)})
		fl.trapIf(encoder.CondEQ)
		div := "udiv"
		if signed {
			div = "sdiv"
			if bitsOf(t) == 32 || bitsOf(t) == 64 {
				fl.emit(Inst{Op: "cmn", Sz: sz, D: R(encoder.X1), S: Imm(1)})
				skip := fl.label()
				fl.emit(Inst{Op: "bcc", Cc: encoder.CondNE, Lbl: skip})
				min := int64(math.MinInt64)
				if bitsOf(t) == 32 {
					min = math.MinInt32
				}
				fl.emit(Inst{Op: "movimm", D: R(encoder.X2), Imm: min})
				fl.emit(Inst{Op: "cmp", Sz: sz, D: R(encoder.X0), S: R(encoder.X2)})
				fl.trapIf(encoder.CondEQ)
				fl.emit(Inst{Op: "label", Lbl: skip})
			}
		}
		fl.emit(Inst{Op: div, Sz: sz, D: R(encoder.X2), S: R(encoder.X0), T: R(encoder.X1)})
		res := encoder.X2
		if op == "urem" || op == "srem" {
			fl.emit(Inst{Op: "msub", Sz: sz, D: R(encoder.X3), S: R(encoder.X2), T: R(encoder.X1), X: R(encoder.X0)})
			res = encoder.X3
		}
		fl.norm(res, t)
		fl.st(in.Result, res)

	case op == "shl" || op == "lshr" || op == "ashr":
		signedV := op == "ashr"
		if err := fl.load(a[0], t, encoder.X0, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, false); err != nil {
			return err
		}
		if b := bitsOf(t); b < 32 {
			fl.emit(Inst{Op: "movimm", D: R(encoder.X2), Imm: int64(b - 1)})
			fl.alu("and", 4, R(encoder.X1), R(encoder.X2))
		}
		armOp := map[string]string{"shl": "lslv", "lshr": "lsrv", "ashr": "asrv"}[op]
		fl.emit(Inst{Op: armOp, Sz: sz, D: R(encoder.X0), S: R(encoder.X0), T: R(encoder.X1)})
		fl.norm(encoder.X0, t)
		fl.st(in.Result, encoder.X0)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, false); err != nil {
			return err
		}
		b := bitsOf(t)
		if b == 32 || b == 64 {
			if op == "rotl" {
				fl.emit(Inst{Op: "neg", Sz: sz, D: R(encoder.X1), S: R(encoder.X1)})
			}
			fl.emit(Inst{Op: "rorv", Sz: sz, D: R(encoder.X0), S: R(encoder.X0), T: R(encoder.X1)})
			fl.st(in.Result, encoder.X0)
			return nil
		}
		fl.emit(Inst{Op: "movimm", D: R(encoder.X2), Imm: int64(b - 1)})
		fl.alu("and", 4, R(encoder.X1), R(encoder.X2))
		fl.emit(Inst{Op: "movimm", D: R(encoder.X2), Imm: int64(b)})
		fl.alu("sub", 4, R(encoder.X2), R(encoder.X1))
		lo, hi := "lsrv", "lslv"
		if op == "rotl" {
			lo, hi = "lslv", "lsrv"
		}
		fl.emit(Inst{Op: lo, Sz: 4, D: R(encoder.X3), S: R(encoder.X0), T: R(encoder.X1)})
		fl.emit(Inst{Op: hi, Sz: 4, D: R(encoder.X2), S: R(encoder.X0), T: R(encoder.X2)})
		fl.alu("orr", 4, R(encoder.X3), R(encoder.X2))
		fl.norm(encoder.X3, t)
		fl.st(in.Result, encoder.X3)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, encoder.X0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, signed); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cmp", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
		cc := map[string]byte{"smin": encoder.CondGT, "smax": encoder.CondLT, "umin": encoder.CondHI, "umax": encoder.CondLO}[op]
		fl.emit(Inst{Op: "csel", Cc: cc, Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
		fl.norm(encoder.X0, t)
		fl.st(in.Result, encoder.X0)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, encoder.X0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, signed); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cmp", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
		fl.setcc(cc, encoder.X2)
		fl.st(in.Result, encoder.X2)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on aarch64 (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, encoder.X0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, signed); err != nil {
			return err
		}
		switch bitsOf(t) {
		case 64:
			mop := "umulh"
			if signed {
				mop = "smulh"
			}
			fl.emit(Inst{Op: mop, D: R(encoder.X2), S: R(encoder.X0), T: R(encoder.X1)})
			fl.st(in.Result, encoder.X2)
		case 32:
			mop := "umull"
			if signed {
				mop = "smull"
			}
			fl.emit(Inst{Op: mop, D: R(encoder.X2), S: R(encoder.X0), T: R(encoder.X1)})
			fl.emit(Inst{Op: "lsr_i", Sz: 8, D: R(encoder.X2), S: R(encoder.X2), Imm: 32})
			fl.st(in.Result, encoder.X2)
		default:
			fl.emit(Inst{Op: "mul", Sz: 4, D: R(encoder.X2), S: R(encoder.X0), T: R(encoder.X1)})
			sh := "lsr_i"
			if signed {
				sh = "asr_i"
			}
			fl.emit(Inst{Op: sh, Sz: 4, D: R(encoder.X2), S: R(encoder.X2), Imm: int64(bitsOf(t))})
			fl.norm(encoder.X2, t)
			fl.st(in.Result, encoder.X2)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on aarch64 (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "clz", Sz: sz, D: R(encoder.X0), S: R(encoder.X0)})
		if b := bitsOf(t); b < 32 {
			fl.alu("sub", 4, R(encoder.X0), Imm(int64(32-b)))
		}
		fl.st(in.Result, encoder.X0)

	case op == "cttz":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "rbit", Sz: sz, D: R(encoder.X0), S: R(encoder.X0)})
		fl.emit(Inst{Op: "clz", Sz: sz, D: R(encoder.X0), S: R(encoder.X0)})
		if b := bitsOf(t); b < 32 {
			fl.emit(Inst{Op: "movimm", D: R(encoder.X1), Imm: int64(b)})
			fl.emit(Inst{Op: "cmp", Sz: 4, D: R(encoder.X0), S: R(encoder.X1)})
			fl.emit(Inst{Op: "csel", Cc: encoder.CondHI, Sz: 4, D: R(encoder.X0), S: R(encoder.X1)})
		}
		fl.st(in.Result, encoder.X0)

	case op == "popcnt":
		return fmt.Errorf("popcnt has no baseline scalar A64 instruction (FEAT_CSSC CNT / NEON tier TODO, §10.4)")

	case op == "bitrev":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "rbit", Sz: sz, D: R(encoder.X0), S: R(encoder.X0)})
		if b := bitsOf(t); b < 32 {
			fl.emit(Inst{Op: "lsr_i", Sz: 4, D: R(encoder.X0), S: R(encoder.X0), Imm: int64(32 - b)})
		}
		fl.st(in.Result, encoder.X0)
	case op == "bswap":
		if err := fl.load(a[0], t, encoder.X0, false); err != nil {
			return err
		}
		switch bitsOf(t) {
		case 64:
			fl.emit(Inst{Op: "rev", Sz: 8, D: R(encoder.X0), S: R(encoder.X0)})
		case 32:
			fl.emit(Inst{Op: "rev", Sz: 4, D: R(encoder.X0), S: R(encoder.X0)})
		default:
			fl.emit(Inst{Op: "rev16", Sz: 4, D: R(encoder.X0), S: R(encoder.X0)})
		}
		fl.st(in.Result, encoder.X0)

	case op == "select":
		if err := fl.load(a[0], vir.I1, encoder.X0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, encoder.X2, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cmp", Sz: 4, D: R(encoder.X0), S: Imm(0)})
		fl.emit(Inst{Op: "csel", Cc: encoder.CondEQ, Sz: 8, D: R(encoder.X1), S: R(encoder.X2)})
		fl.st(in.Result, encoder.X1)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, encoder.X1, false); err != nil {
			return err
		}
		if op == "atomic_load" {
			switch lastOrd(a) {
			case "acquire", "seqcst":
				fl.emit(Inst{Op: "ldar", D: R(encoder.X0), S: Mem(encoder.X1, 0), Sz: szOf(t)})
			default:
				fl.emit(Inst{Op: "ldr", D: R(encoder.X0), S: Mem(encoder.X1, 0), Sz: szOf(t)})
			}
			fl.st(in.Result, encoder.X0)
			return nil
		}
		fl.emit(Inst{Op: "ldr", D: R(encoder.X0), S: Mem(encoder.X1, 0), Sz: szOf(t)})
		fl.st(in.Result, encoder.X0)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, encoder.X1, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X0, false); err != nil {
			return err
		}
		if op == "atomic_store" {
			switch lastOrd(a) {
			case "release", "seqcst":
				fl.emit(Inst{Op: "stlr", D: Mem(encoder.X1, 0), S: R(encoder.X0), Sz: szOf(t)})
			default:
				fl.emit(Inst{Op: "str", D: Mem(encoder.X1, 0), S: R(encoder.X0), Sz: szOf(t)})
			}
			return nil
		}
		fl.emit(Inst{Op: "str", D: Mem(encoder.X1, 0), S: R(encoder.X0), Sz: szOf(t)})

	case op == "alloca":
		if err := fl.load(a[0], vir.I64, encoder.X0, false); err != nil {
			return err
		}
		fl.alu("add", 8, R(encoder.X0), Imm(15))
		fl.alu("bic", 8, R(encoder.X0), Imm(15))
		fl.emit(Inst{Op: "sub_sp_r", S: R(encoder.X0)})
		if in.Align > 16 {
			fl.emit(Inst{Op: "and_sp", Imm: int64(-in.Align)})
		}
		fl.emit(Inst{Op: "mov_r_sp", D: R(encoder.X0)})
		fl.st(in.Result, encoder.X0)

	case op == "field":
		off, err := fl.lay.FieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, encoder.X0, false); err != nil {
			return err
		}
		if off != 0 {
			fl.alu("add", 8, R(encoder.X0), Imm(int64(off)))
		}
		fl.st(in.Result, encoder.X0)

	case op == "index":
		esz, err := fl.lay.Size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, encoder.X0, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, encoder.X1, true); err != nil {
			return err
		}
		fl.emit(Inst{Op: "movimm", D: R(encoder.X2), Imm: int64(esz)})
		fl.emit(Inst{Op: "mul", Sz: 8, D: R(encoder.X3), S: R(encoder.X1), T: R(encoder.X2)})
		fl.alu("add", 8, R(encoder.X0), R(encoder.X3))
		fl.st(in.Result, encoder.X0)

	case op == "memcopy" || op == "memset":
		if err := fl.load(a[0], vir.Ptr, encoder.X0, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, encoder.X1, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, encoder.X1, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I64, encoder.X2, false); err != nil {
			return err
		}
		loop, done := fl.label(), fl.label()
		fl.emit(Inst{Op: "movimm", D: R(rIDX), Imm: 0})
		fl.emit(Inst{Op: "label", Lbl: loop})
		fl.emit(Inst{Op: "cmp", Sz: 8, D: R(rIDX), S: R(encoder.X2)})
		fl.emit(Inst{Op: "bcc", Cc: encoder.CondEQ, Lbl: done})
		if op == "memcopy" {
			fl.emit(Inst{Op: "ldrb_r", D: R(encoder.X3), S: R(encoder.X1), T: R(rIDX)})
			fl.emit(Inst{Op: "strb_r", D: R(encoder.X0), S: R(encoder.X3), T: R(rIDX)})
		} else {
			fl.emit(Inst{Op: "strb_r", D: R(encoder.X0), S: R(encoder.X1), T: R(rIDX)})
		}
		fl.alu("add", 8, R(rIDX), Imm(1))
		fl.emit(Inst{Op: "b", Lbl: loop})
		fl.emit(Inst{Op: "label", Lbl: done})

	case op == "memmove":
		if err := fl.load(a[0], vir.Ptr, encoder.X0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, encoder.X1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, encoder.X2, false); err != nil {
			return err
		}
		back, bloop, floop, done := fl.label(), fl.label(), fl.label()
		fl.emit(Inst{Op: "cmp", Sz: 8, D: R(encoder.X1), S: R(encoder.X0)})
		fl.emit(Inst{Op: "bcc", Cc: encoder.CondLO, Lbl: back})
		fl.emit(Inst{Op: "movimm", D: R(rIDX), Imm: 0})
		fl.emit(Inst{Op: "label", Lbl: floop})
		fl.emit(Inst{Op: "cmp", Sz: 8, D: R(rIDX), S: R(encoder.X2)})
		fl.emit(Inst{Op: "bcc", Cc: encoder.CondEQ, Lbl: done})
		fl.emit(Inst{Op: "ldrb_r", D: R(encoder.X3), S: R(encoder.X1), T: R(rIDX)})
		fl.emit(Inst{Op: "strb_r", D: R(encoder.X0), S: R(encoder.X3), T: R(rIDX)})
		fl.alu("add", 8, R(rIDX), Imm(1))
		fl.emit(Inst{Op: "b", Lbl: floop})
		fl.emit(Inst{Op: "label", Lbl: back})
		fl.emit(Inst{Op: "mov_r", Sz: 8, D: R(rIDX), S: R(encoder.X2)})
		fl.emit(Inst{Op: "label", Lbl: bloop})
		fl.emit(Inst{Op: "cmp", Sz: 8, D: R(rIDX), S: Imm(0)})
		fl.emit(Inst{Op: "bcc", Cc: encoder.CondEQ, Lbl: done})
		fl.alu("sub", 8, R(rIDX), Imm(1))
		fl.emit(Inst{Op: "ldrb_r", D: R(encoder.X3), S: R(encoder.X1), T: R(rIDX)})
		fl.emit(Inst{Op: "strb_r", D: R(encoder.X0), S: R(encoder.X3), T: R(rIDX)})
		fl.emit(Inst{Op: "b", Lbl: bloop})
		fl.emit(Inst{Op: "label", Lbl: done})

	case op == "prefetch":
		return nil

	case op == "fence":
		fl.emit(Inst{Op: "dmb"})
		return nil

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_and" ||
		op == "atomic_or" || op == "atomic_xor" || op == "atomic_xchg":
		if szOf(t) < 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on aarch64 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, encoder.X2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, false); err != nil {
			return err
		}
		retry := fl.label()
		fl.emit(Inst{Op: "label", Lbl: retry})
		fl.emit(Inst{Op: "ldaxr", D: R(encoder.X0), S: Mem(encoder.X2, 0), Sz: szOf(t)})
		switch op {
		case "atomic_xchg":
			fl.emit(Inst{Op: "mov_r", Sz: 8, D: R(encoder.X3), S: R(encoder.X1)})
		case "atomic_add", "atomic_sub":
			fl.emit(Inst{Op: "mov_r", Sz: 8, D: R(encoder.X3), S: R(encoder.X0)})
			armOp := "add"
			if op == "atomic_sub" {
				armOp = "sub"
			}
			fl.alu(armOp, sz, R(encoder.X3), R(encoder.X1))
		default:
			fl.emit(Inst{Op: "mov_r", Sz: 8, D: R(encoder.X3), S: R(encoder.X0)})
			fl.alu(map[string]string{"atomic_and": "and", "atomic_or": "orr", "atomic_xor": "eor"}[op], sz, R(encoder.X3), R(encoder.X1))
		}
		fl.emit(Inst{Op: "stlxr", X: R(encoder.X4), S: R(encoder.X3), D: Mem(encoder.X2, 0), Sz: szOf(t)})
		fl.emit(Inst{Op: "cbnz", Sz: 4, S: R(encoder.X4), Lbl: retry})
		fl.st(in.Result, encoder.X0)

	case op == "cmpxchg":
		if szOf(t) < 4 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on aarch64 (TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, encoder.X2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, encoder.X1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, encoder.X3, false); err != nil {
			return err
		}
		retry, fail, done := fl.label(), fl.label(), fl.label()
		fl.emit(Inst{Op: "label", Lbl: retry})
		fl.emit(Inst{Op: "ldaxr", D: R(encoder.X0), S: Mem(encoder.X2, 0), Sz: szOf(t)})
		fl.emit(Inst{Op: "cmp", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
		fl.emit(Inst{Op: "bcc", Cc: encoder.CondNE, Lbl: fail})
		fl.emit(Inst{Op: "stlxr", X: R(encoder.X4), S: R(encoder.X3), D: Mem(encoder.X2, 0), Sz: szOf(t)})
		fl.emit(Inst{Op: "cbnz", Sz: 4, S: R(encoder.X4), Lbl: retry})
		fl.emit(Inst{Op: "b", Lbl: done})
		fl.emit(Inst{Op: "label", Lbl: fail})
		fl.emit(Inst{Op: "clrex"})
		fl.emit(Inst{Op: "label", Lbl: done})
		fl.st(in.Result, encoder.X0)

	case op == "trunc":
		if err := fl.load(a[0], nil, encoder.X0, false); err != nil {
			return err
		}
		if bitsOf(t) == 32 {
			fl.emit(Inst{Op: "mov_r", Sz: 4, D: R(encoder.X0), S: R(encoder.X0)})
		} else {
			fl.norm(encoder.X0, t)
		}
		fl.st(in.Result, encoder.X0)

	case op == "zext":
		if err := fl.load(a[0], nil, encoder.X0, false); err != nil {
			return err
		}
		fl.st(in.Result, encoder.X0)

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		dsz := szMachine(t)
		switch bitsOf(st) {
		case 1:
			fl.emit(Inst{Op: "ldr", D: R(encoder.X0), S: Slot(a[0].Ident), Sz: 8})
			fl.emit(Inst{Op: "sxt1", Sz: dsz, D: R(encoder.X0), S: R(encoder.X0)})
		case 8:
			if err := fl.load(a[0], st, encoder.X0, false); err != nil {
				return err
			}
			fl.emit(Inst{Op: "sxtb", Sz: dsz, D: R(encoder.X0), S: R(encoder.X0)})
		case 16:
			if err := fl.load(a[0], st, encoder.X0, false); err != nil {
				return err
			}
			fl.emit(Inst{Op: "sxth", Sz: dsz, D: R(encoder.X0), S: R(encoder.X0)})
		case 32:
			if err := fl.load(a[0], st, encoder.X0, false); err != nil {
				return err
			}
			if dsz == 8 {
				fl.emit(Inst{Op: "sxtw", D: R(encoder.X0), S: R(encoder.X0)})
			}
		default:
			if err := fl.load(a[0], st, encoder.X0, false); err != nil {
				return err
			}
		}
		fl.norm(encoder.X0, t)
		fl.st(in.Result, encoder.X0)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on aarch64 (TODO)")
		}
		if err := fl.load(a[0], st, encoder.X0, false); err != nil {
			return err
		}
		fl.st(in.Result, encoder.X0)

	case op == "call":
		return fl.selCall(in)

	case op == "syscall":
		return fl.selSyscall(in)

	case op == "fdemote" || op == "fpromote" || op == "sfromint" || op == "ufromint" ||
		op == "stoint" || op == "utoint" || op == "stoint_sat" || op == "utoint_sat" ||
		op == "sqrt" || op == "fma" || op == "copysign" || op == "floor" || op == "ceil" ||
		op == "trunc_f" || op == "nearest" || op == "min" || op == "max":
		return fmt.Errorf("floating-point op %q not lowered on aarch64 (FP/SIMD tier TODO)", op)

	case op == "splat" || op == "extract" || op == "insert" || op == "shuffle" ||
		op == "masked_load" || op == "masked_store" || op == "gather" || op == "scatter" ||
		op == "reduce_add" || op == "reduce_min" || op == "reduce_max" ||
		op == "reduce_and" || op == "reduce_or" || op == "reduce_xor":
		return fmt.Errorf("vector op %q not lowered on aarch64 (NEON/SVE tier TODO, §10.4)", op)

	default:
		return fmt.Errorf("op %q not lowered on aarch64", op)
	}
	return nil
}

func osOf(m *vir.Module) string {
	if m.Target == nil {
		return ""
	}
	return m.Target.OS
}

// selSyscall lowers a vir.Instruction with Op == "syscall" using the
// target OS's syscall convention (syscall.go): number and args are
// staged into the convention's registers, the trap is a plain svc #0,
// and the result register is normalized/stored like any other call
// result.
func (fl *fnLower) selSyscall(in *vir.Instruction) error {
	os := osOf(fl.m)
	conv, ok := lookupSyscall(os)
	if !ok {
		return fmt.Errorf("syscall: no syscall convention registered for target OS %q", os)
	}
	args := in.Args
	if len(args)-1 > len(conv.ArgRegs) {
		return fmt.Errorf("syscall: %d arguments exceeds the %d the %q convention supports", len(args)-1, len(conv.ArgRegs), os)
	}
	if err := fl.load(args[0], vir.I64, conv.NumberReg, false); err != nil {
		return err
	}
	for i, arg := range args[1:] {
		if err := fl.load(arg, nil, conv.ArgRegs[i], false); err != nil {
			return err
		}
	}
	fl.emit(Inst{Op: "svc", Imm: 0})
	if in.Result != "" {
		fl.normReg(conv.ResultReg, in.Suffix)
		fl.st(in.Result, conv.ResultReg)
	}
	return nil
}

func (fl *fnLower) selOverflow(in *vir.Instruction) error {
	t, a := in.Suffix, in.Args
	sz := szMachine(t)
	signed := in.Op[0] == 's'
	if err := fl.load(a[0], t, encoder.X0, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, encoder.X1, signed); err != nil {
		return err
	}
	b := bitsOf(t)
	if b == 32 || b == 64 {
		switch in.Op {
		case "uaddo":
			fl.emit(Inst{Op: "adds", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
			fl.setcc(encoder.CondHS, encoder.X2)
		case "usubo":
			fl.emit(Inst{Op: "subs", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
			fl.setcc(encoder.CondLO, encoder.X2)
		case "saddo":
			fl.emit(Inst{Op: "adds", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
			fl.setcc(encoder.CondVS, encoder.X2)
		case "ssubo":
			fl.emit(Inst{Op: "subs", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
			fl.setcc(encoder.CondVS, encoder.X2)
		case "umulo":
			if b == 64 {
				fl.emit(Inst{Op: "umulh", D: R(encoder.X3), S: R(encoder.X0), T: R(encoder.X1)})
				fl.emit(Inst{Op: "cmp", Sz: 8, D: R(encoder.X3), S: Imm(0)})
				fl.setcc(encoder.CondNE, encoder.X2)
			} else {
				fl.emit(Inst{Op: "umull", D: R(encoder.X3), S: R(encoder.X0), T: R(encoder.X1)})
				fl.emit(Inst{Op: "lsr_i", Sz: 8, D: R(encoder.X3), S: R(encoder.X3), Imm: 32})
				fl.emit(Inst{Op: "cmp", Sz: 8, D: R(encoder.X3), S: Imm(0)})
				fl.setcc(encoder.CondNE, encoder.X2)
			}
		case "smulo":
			if b == 64 {
				fl.emit(Inst{Op: "smulh", D: R(encoder.X3), S: R(encoder.X0), T: R(encoder.X1)})
				fl.emit(Inst{Op: "mul", Sz: 8, D: R(encoder.X4), S: R(encoder.X0), T: R(encoder.X1)})
				fl.emit(Inst{Op: "asr_i", Sz: 8, D: R(encoder.X4), S: R(encoder.X4), Imm: 63})
				fl.emit(Inst{Op: "cmp", Sz: 8, D: R(encoder.X3), S: R(encoder.X4)})
				fl.setcc(encoder.CondNE, encoder.X2)
			} else {
				fl.emit(Inst{Op: "smull", D: R(encoder.X3), S: R(encoder.X0), T: R(encoder.X1)})
				fl.emit(Inst{Op: "sxtw", D: R(encoder.X4), S: R(encoder.X3)})
				fl.emit(Inst{Op: "cmp", Sz: 8, D: R(encoder.X4), S: R(encoder.X3)})
				fl.setcc(encoder.CondNE, encoder.X2)
			}
		}
	} else {
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", 4, R(encoder.X0), R(encoder.X1))
		case "usubo", "ssubo":
			fl.alu("sub", 4, R(encoder.X0), R(encoder.X1))
		case "umulo", "smulo":
			fl.emit(Inst{Op: "mul", Sz: 4, D: R(encoder.X2), S: R(encoder.X0), T: R(encoder.X1)})
			fl.emit(Inst{Op: "mov_r", Sz: 8, D: R(encoder.X0), S: R(encoder.X2)})
		}
		ext := "uxtb"
		if signed {
			ext = "sxtb"
		}
		if szOf(t) == 2 {
			ext = "uxth"
			if signed {
				ext = "sxth"
			}
		}
		fl.emit(Inst{Op: "mov_r", Sz: 8, D: R(encoder.X1), S: R(encoder.X0)})
		fl.norm(encoder.X1, t)
		fl.emit(Inst{Op: ext, Sz: 4, D: R(encoder.X1), S: R(encoder.X1)})
		fl.emit(Inst{Op: "cmp", Sz: 4, D: R(encoder.X1), S: R(encoder.X0)})
		fl.setcc(encoder.CondNE, encoder.X2)
	}
	fl.st(in.Result, encoder.X2)
	return nil
}

func (fl *fnLower) typeOfOperand(o vir.Operand) (vir.Type, error) {
	if o.Kind != vir.OperandIdent {
		return nil, fmt.Errorf("conversion source must be a named value or const on aarch64")
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

// ---------------------------------------------------------------------------
// Calls (AAPCS64) and terminators
// ---------------------------------------------------------------------------

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
				return fmt.Errorf("byval/sret call arguments not yet lowered on aarch64 (TODO)")
			}
		}
		args = args[1:]
	}
	if !vir.IsVoid(ret) {
		if err := fl.checkValueType(ret); err != nil {
			return err
		}
	}

	stage := int64(StageBytes(len(args)))
	if stage > 0 {
		fl.emit(Inst{Op: "sub_sp", Imm: stage})
	}
	for i, a := range args {
		if err := fl.load(a, nil, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "str", D: Mem(encoder.SP, int32(8*i)), S: R(encoder.X0), Sz: 8})
	}
	if indirect {
		if err := fl.load(in.Args[0], vir.Ptr, encoder.IP1, false); err != nil {
			return err
		}
	}
	nreg := RegArgs(len(args))
	for i := 0; i < nreg; i++ {
		fl.emit(Inst{Op: "ldr", D: R(encoder.Reg(i)), S: Mem(encoder.SP, int32(8*i)), Sz: 8})
	}
	cleanup := stage
	if len(args) > 8 {
		fl.emit(Inst{Op: "add_sp", Imm: 64})
		cleanup = stage - 64
	} else if stage > 0 {
		fl.emit(Inst{Op: "add_sp", Imm: stage})
		cleanup = 0
	}
	if indirect {
		fl.emit(Inst{Op: "blr_r", S: R(encoder.IP1)})
	} else {
		fl.emit(Inst{Op: "bl_sym", Sym: in.Args[0].Ident})
	}
	if cleanup > 0 {
		fl.emit(Inst{Op: "add_sp", Imm: cleanup})
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.normReg(encoder.X0, ret)
		fl.st(in.Result, encoder.X0)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		fl.emit(Inst{Op: "b", Lbl: x.Label})
	case vir.BranchIf:
		if err := fl.load(x.Cond, vir.I1, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cbnz", Sz: 4, S: R(encoder.X0), Lbl: x.Then})
		fl.emit(Inst{Op: "b", Lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I64
		}
		sz := szMachine(vt)
		if err := fl.load(x.Value, vt, encoder.X0, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.emit(Inst{Op: "movimm", D: R(encoder.X1), Imm: litBits(c.Value, vt, false)})
			fl.emit(Inst{Op: "cmp", Sz: sz, D: R(encoder.X0), S: R(encoder.X1)})
			fl.emit(Inst{Op: "bcc", Cc: encoder.CondEQ, Lbl: c.Label})
		}
		fl.emit(Inst{Op: "b", Lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, encoder.X0, false); err != nil {
				return err
			}
		}
		fl.emit(Inst{Op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(Inst{Op: "brk"})
	case vir.Unreachable:
		fl.emit(Inst{Op: "brk"})
	default:
		return fmt.Errorf("terminator %T not lowered on aarch64", t)
	}
	return nil
}

func (fl *fnLower) selTailCall(x vir.TailCall) error {
	args := x.Args
	indirect := x.Callee == ""
	if indirect {
		args = args[1:]
	}
	if len(args) > 8 {
		return fmt.Errorf("tailcall with %d args exceeds the x0-x7 register set (stack-arg tailcalls TODO)", len(args))
	}
	stage := int64(StageBytes(len(args)))
	if stage > 0 {
		fl.emit(Inst{Op: "sub_sp", Imm: stage})
	}
	for i, a := range args {
		if err := fl.load(a, nil, encoder.X0, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "str", D: Mem(encoder.SP, int32(8*i)), S: R(encoder.X0), Sz: 8})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, encoder.IP1, false); err != nil {
			return err
		}
	}
	for i := range args {
		fl.emit(Inst{Op: "ldr", D: R(encoder.Reg(i)), S: Mem(encoder.SP, int32(8*i)), Sz: 8})
	}
	if stage > 0 {
		fl.emit(Inst{Op: "add_sp", Imm: stage})
	}
	if indirect {
		fl.emit(Inst{Op: "epi_jmp_r", S: R(encoder.IP1)})
	} else {
		fl.emit(Inst{Op: "epi_jmp_sym", Sym: x.Callee})
	}
	return nil
}