package arm

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Lower converts a verified module into an arm Program (arrow 3) for the
// given arch ("arm" or "armeb" — the §10.1 canonical spellings, arch.go).
// The module must have passed vir.Verify; Lower assumes the §9 obligations.
// A module with no target-decl is lowered for whichever arch is requested
// (target selection is a build input, §10); a module that declares a target
// must agree with the requested arch.
func Lower(m *vir.Module, arch Arch) (*Program, error) {
	if !arch.valid() {
		return nil, fmt.Errorf("lower/arm: unknown arch %q (want %q or %q)", arch, ArchARM, ArchARMEB)
	}
	if m.Target != nil && string(arch) != m.Target.Arch {
		return nil, fmt.Errorf("lower/arm: module targets arch %q, build requested %q (§10.6: the two must agree)", m.Target.Arch, arch)
	}
	lw := &lowerer{
		m: m, lay: newLayout(m), arch: arch,
		kinds:  map[string]string{},
		consts: map[string]*vir.Const{},
	}
	for _, s := range m.Structs {
		lw.kinds[s.Name] = "struct"
	}
	for _, s := range m.FnSigs {
		lw.kinds[s.Name] = "fnsig"
	}
	for _, c := range m.Consts {
		lw.kinds[c.Name] = "const"
		lw.consts[c.Name] = c
	}
	for _, g := range m.Globals {
		lw.kinds[g.Name] = "global"
	}
	for _, g := range m.Externs {
		for _, f := range g.Fns {
			lw.kinds[f.Name] = "extern"
		}
	}
	for _, f := range m.Funcs {
		lw.kinds[f.Name] = "fn"
	}

	p := &Program{Arch: arch}
	for _, g := range m.Globals {
		pg, err := lw.lowerGlobal(g)
		if err != nil {
			return nil, fmt.Errorf("global %s: %w", g.Name, err)
		}
		p.Globals = append(p.Globals, pg)
	}
	for _, f := range m.Funcs {
		pf, err := lw.lowerFunc(f)
		if err != nil {
			return nil, fmt.Errorf("fn %s: %w", f.Name, err)
		}
		p.Funcs = append(p.Funcs, pf)
	}
	return p, nil
}

type lowerer struct {
	m      *vir.Module
	lay    *layout
	arch   Arch
	kinds  map[string]string
	consts map[string]*vir.Const
}

func (lw *lowerer) lookupCallable(name string) (ret vir.Type, params []vir.Param, ok bool) {
	for _, g := range lw.m.Externs {
		for _, e := range g.Fns {
			if e.Name == name {
				return e.Ret, e.Params, true
			}
		}
	}
	for _, f := range lw.m.Funcs {
		if f.Name == name {
			return f.Ret, f.Params, true
		}
	}
	return nil, nil, false
}

// ---------------------------------------------------------------------------
// Function lowering
// ---------------------------------------------------------------------------

func (lw *lowerer) lowerFunc(f *vir.Func) (Func, error) {
	fl := &fnLower{lowerer: lw, f: f}
	var err error
	if fl.types, err = fl.typeFunc(); err != nil {
		return Func{}, err
	}
	for _, p := range f.Params {
		if p.ByVal != "" || p.SRet != "" {
			return Func{}, fmt.Errorf("byval/sret not yet lowered on arm (AAPCS aggregate passing TODO)")
		}
	}
	// Spill the register arguments into their home slots before anything
	// can clobber r0-r3 (frame.go).
	for i, p := range f.Params {
		if i < 4 {
			fl.emit(minst{op: "str", d: Slot(p.Name), s: R(reg(i)), sz: 4})
		}
	}
	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(minst{op: "label", lbl: b.Label})
		}
		for i := range b.Insts {
			if err := fl.selInst(&b.Insts[i]); err != nil {
				return Func{}, fmt.Errorf("block %s: %s: %w", labelName(b), b.Insts[i].Op, err)
			}
		}
		if err := fl.selTerm(b.Term); err != nil {
			return Func{}, fmt.Errorf("block %s: terminator: %w", labelName(b), err)
		}
	}
	fr := buildFrame(f, fl.b)
	if err := resolveSlots(fl.b, fr); err != nil {
		return Func{}, err
	}
	code, fixups, err := encodeFunc(fl.b, fr.local, fl.arch)
	if err != nil {
		return Func{}, err
	}
	return Func{Name: f.Name, Code: code, Align: 4, Export: f.Export, Fixups: fixups}, nil
}

func labelName(b *vir.Block) string {
	if b.Label == "" {
		return "<entry>"
	}
	return b.Label
}

type fnLower struct {
	*lowerer
	f     *vir.Func
	types map[string]vir.Type
	b     []minst
	nlbl  int
}

func (fl *fnLower) emit(i minst)             { fl.b = append(fl.b, i) }
func (fl *fnLower) alu(op string, d, s opr)  { fl.emit(minst{op: op, d: d, s: s}) }
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}

// typeFunc mirrors the verifier's result-type computation for the subset
// this backend supports (input is verified, so lookups cannot fail
// semantically). Identical to lower/x86.
func (fl *fnLower) typeFunc() (map[string]vir.Type, error) {
	types := map[string]vir.Type{}
	for _, p := range fl.f.Params {
		if err := fl.checkValueType(p.Type); err != nil {
			return nil, err
		}
		types[p.Name] = p.Type
	}
	for _, b := range fl.f.AllBlocks() {
		for i := range b.Insts {
			in := &b.Insts[i]
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

func (fl *fnLower) resultType(in *vir.Inst) (vir.Type, error) {
	switch {
	case voidOps[in.Op]:
		return vir.Void, nil
	case cmpOps[in.Op]:
		return vir.I1, nil
	case in.Op == "call":
		if in.Sig != "" {
			for _, s := range fl.m.FnSigs {
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

// litBits masks v to t's width, sign- or zero-extending back to 32 bits.
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
// Values narrower than 32 bits live zero-extended in their slots; signed
// requests a sign-extended materialization (signed compares/div/shifts).
func (fl *fnLower) load(o vir.Operand, t vir.Type, r reg, signed bool) error {
	switch o.Kind {
	case vir.OInt:
		fl.emit(minst{op: "movimm", d: R(r), imm: litBits(o.Int, t, signed)})
	case vir.OBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		fl.emit(minst{op: "movimm", d: R(r), imm: v})
	case vir.ONull:
		fl.emit(minst{op: "movimm", d: R(r), imm: 0})
	case vir.OFloat:
		return fmt.Errorf("float operands not lowered on arm (TODO)")
	case vir.OIdent:
		switch fl.kinds[o.Ident] {
		case "const":
			c := fl.consts[o.Ident]
			return fl.load(c.Value, c.Type, r, signed)
		case "global", "fn", "extern":
			// A name in operand position yields its address (§4, Addresses).
			fl.emit(minst{op: "movsym", d: R(r), sym: o.Ident})
		case "struct", "fnsig":
			return fmt.Errorf("entity %q used as runtime operand", o.Ident)
		default: // local value slot (always 4-byte, zero-extended)
			vt, ok := fl.types[o.Ident]
			if !ok {
				return fmt.Errorf("value %q has no type (isel bug)", o.Ident)
			}
			fl.emit(minst{op: "ldr", d: R(r), s: Slot(o.Ident), sz: 4})
			if signed {
				switch szOf(vt) {
				case 1:
					fl.emit(minst{op: "sxtb", d: R(r), s: R(r)})
				case 2:
					fl.emit(minst{op: "sxth", d: R(r), s: R(r)})
				}
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on arm", o.Kind)
	}
	return nil
}

// st writes r (already normalized) into name's 4-byte home slot.
func (fl *fnLower) st(name string, r reg) {
	fl.emit(minst{op: "str", d: Slot(name), s: R(r), sz: 4})
}

// norm re-establishes the zero-extended-slot invariant after wrapping ops.
func (fl *fnLower) norm(r reg, t vir.Type) {
	if it, ok := t.(vir.IntType); ok && it.Bits == 1 {
		fl.alu("and", R(r), Imm(1))
		return
	}
	switch szOf(t) {
	case 1:
		fl.emit(minst{op: "uxtb", d: R(r), s: R(r)})
	case 2:
		fl.emit(minst{op: "uxth", d: R(r), s: R(r)})
	}
}

// setcc materializes an i1 from the current flags into r.
func (fl *fnLower) setcc(cc byte, r reg) {
	fl.emit(minst{op: "movimm", d: R(r), imm: 0}) // movw: flag-free
	fl.emit(minst{op: "movcc", cc: cc, d: R(r), s: Imm(1)})
}

// trapIf emits a conditional deterministic halt: branch around a udf.
func (fl *fnLower) trapIf(cc byte) {
	ok := fl.label()
	fl.emit(minst{op: "bcc", cc: cc ^ 1, lbl: ok}) // inverted condition skips
	fl.emit(minst{op: "udf"})
	fl.emit(minst{op: "label", lbl: ok})
}

// ---------------------------------------------------------------------------
// Instruction selection
// ---------------------------------------------------------------------------

func (fl *fnLower) selInst(in *vir.Inst) error {
	op, t, a := in.Op, in.Suffix, in.Args
	signedCmp := map[string]byte{"slt": ccLT, "sle": ccLE, "sgt": ccGT, "sge": ccGE}
	unsignedCmp := map[string]byte{"eq": ccEQ, "ne": ccNE, "ult": ccLO, "ule": ccLS, "ugt": ccHI, "uge": ccHS}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		fl.st(in.Result, r0)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, false); err != nil {
			return err
		}
		armOp := map[string]string{"add": "add", "sub": "sub", "and": "and", "or": "orr", "xor": "eor"}[op]
		fl.alu(armOp, R(r0), R(r1))
		if op == "add" || op == "sub" {
			fl.norm(r0, t) // wrap mod 2^N (§4)
		}
		fl.st(in.Result, r0)

	case op == "mul":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, false); err != nil {
			return err
		}
		fl.emit(minst{op: "mul", d: R(r2), s: R(r0), t: R(r1)})
		fl.norm(r2, t)
		fl.st(in.Result, r2)

	case op == "neg":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "rsb", d: R(r0), s: Imm(0)}) // r0 := 0 - r0
		fl.norm(r0, t)
		fl.st(in.Result, r0)

	case op == "not":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "mvn", d: R(r0), s: R(r0)})
		fl.norm(r0, t)
		fl.st(in.Result, r0)

	case op == "abs": // signed; abs(INT_MIN) wraps (§4)
		if err := fl.load(a[0], t, r0, true); err != nil {
			return err
		}
		fl.emit(minst{op: "asr", d: R(r1), s: R(r0), t: Imm(31)})
		fl.alu("eor", R(r0), R(r1))
		fl.alu("sub", R(r0), R(r1))
		fl.norm(r0, t)
		fl.st(in.Result, r0)

	case op == "udiv" || op == "urem" || op == "sdiv" || op == "srem":
		// ARM UDIV/SDIV never trap: zero divisor yields 0 and INT_MIN/-1
		// yields INT_MIN, so §6.1's traps must be explicit here.
		signed := op[0] == 's'
		if err := fl.load(a[0], t, r0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(r1), Imm(0))
		fl.trapIf(ccEQ) // zero divisor traps (§6.1)
		div := "udiv"
		if signed {
			div = "sdiv"
			if szOf(t) == 4 {
				// INT_MIN / -1 traps (§6.1).
				fl.alu("cmn", R(r1), Imm(1)) // flags for r1 == -1
				skip := fl.label()
				fl.emit(minst{op: "bcc", cc: ccNE, lbl: skip})
				fl.emit(minst{op: "movimm", d: R(r2), imm: int64(int32(math.MinInt32))})
				fl.alu("cmp", R(r0), R(r2))
				fl.trapIf(ccEQ)
				fl.emit(minst{op: "label", lbl: skip})
			}
			// TODO(§6.1): narrow INT_MIN/-1 (e.g. i8 -128/-1) wraps via the
			// widened 32-bit sdiv instead of trapping; needs a check for sz<4
			// (same known gap as lower/x86).
		}
		fl.emit(minst{op: div, d: R(r2), s: R(r0), t: R(r1)})
		res := r2
		if op == "urem" || op == "srem" {
			// rem := a - (a/b)*b
			fl.emit(minst{op: "mls", d: R(r3), s: R(r2), t: R(r1), x: R(r0)})
			res = r3
		}
		fl.norm(res, t)
		fl.st(in.Result, res)

	case op == "shl" || op == "lshr" || op == "ashr":
		// ARM register shifts read the count's low byte and yield 0 for
		// counts >= 32, so §4's count-mod-N masking is emitted for every
		// width, including 32.
		signedV := op == "ashr"
		if err := fl.load(a[0], t, r0, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, false); err != nil {
			return err
		}
		fl.alu("and", R(r1), Imm(int64(bitsOf(t)-1)))
		armOp := map[string]string{"shl": "lsl", "lshr": "lsr", "ashr": "asr"}[op]
		fl.emit(minst{op: armOp, d: R(r0), s: R(r0), t: R(r1)})
		fl.norm(r0, t)
		fl.st(in.Result, r0)

	case op == "rotl" || op == "rotr":
		// Generic two-shift form works at every width N <= 32: the
		// complementary shift by N-c degenerates correctly at c = 0 because
		// a register shift by N (<= 32) of a zero-extended value yields 0.
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, false); err != nil {
			return err
		}
		fl.alu("and", R(r1), Imm(int64(bitsOf(t)-1))) // count mod N (§4)
		fl.emit(minst{op: "movimm", d: R(r2), imm: int64(bitsOf(t))})
		fl.alu("sub", R(r2), R(r1)) // N - c
		lo, hi := "lsr", "lsl"      // rotr: (x >> c) | (x << (N-c))
		if op == "rotl" {
			lo, hi = "lsl", "lsr" // rotl: (x << c) | (x >> (N-c))
		}
		fl.emit(minst{op: lo, d: R(r3), s: R(r0), t: R(r1)})
		fl.emit(minst{op: hi, d: R(r2), s: R(r0), t: R(r2)})
		fl.alu("orr", R(r3), R(r2))
		fl.norm(r3, t)
		fl.st(in.Result, r3)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, r0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(r0), R(r1))
		cc := map[string]byte{"smin": ccGT, "smax": ccLT, "umin": ccHI, "umax": ccLO}[op]
		fl.emit(minst{op: "movcc", cc: cc, d: R(r0), s: R(r1)})
		fl.norm(r0, t)
		fl.st(in.Result, r0)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, r0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(r0), R(r1))
		fl.setcc(cc, r2)
		fl.st(in.Result, r2)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on arm (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, r0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, signed); err != nil {
			return err
		}
		if szOf(t) == 4 {
			m := "umull"
			if signed {
				m = "smull"
			}
			fl.emit(minst{op: m, d: R(r2), x: R(r3), s: R(r0), t: R(r1)})
			fl.st(in.Result, r3)
		} else { // narrow: full product fits in 32 bits; shift the high half down
			fl.emit(minst{op: "mul", d: R(r2), s: R(r0), t: R(r1)})
			sh := "lsr"
			if signed {
				sh = "asr"
			}
			fl.emit(minst{op: sh, d: R(r2), s: R(r2), t: Imm(int64(bitsOf(t)))})
			fl.norm(r2, t)
			fl.st(in.Result, r2)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on arm (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "clz", d: R(r0), s: R(r0)})
		if bitsOf(t) < 32 { // leading zeros at width N = clz32 - (32-N)
			fl.alu("sub", R(r0), Imm(int64(32-bitsOf(t))))
		}
		fl.st(in.Result, r0)

	case op == "cttz":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "rbit", d: R(r0), s: R(r0)})
		fl.emit(minst{op: "clz", d: R(r0), s: R(r0)})
		if bitsOf(t) < 32 { // zero input gives 32; clamp to N
			fl.emit(minst{op: "movimm", d: R(r1), imm: int64(bitsOf(t))})
			fl.alu("cmp", R(r0), R(r1))
			fl.emit(minst{op: "movcc", cc: ccHI, d: R(r0), s: R(r1)})
		}
		fl.st(in.Result, r0)

	case op == "popcnt":
		return fmt.Errorf("popcnt has no scalar A32 instruction (NEON vcnt tier TODO, §10.4)")

	case op == "bitrev": // rbit is baseline ARMv7 — lowered, unlike x86
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "rbit", d: R(r0), s: R(r0)})
		if bitsOf(t) < 32 {
			fl.emit(minst{op: "lsr", d: R(r0), s: R(r0), t: Imm(int64(32 - bitsOf(t)))})
		}
		fl.st(in.Result, r0)

	case op == "bswap":
		if err := fl.load(a[0], t, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "rev", d: R(r0), s: R(r0)})
		if szOf(t) == 2 { // i16: high halfword holds the swapped bytes
			fl.emit(minst{op: "lsr", d: R(r0), s: R(r0), t: Imm(16)})
		}
		fl.st(in.Result, r0) // i8 rejected by the verifier (§9.20)

	case op == "select":
		if err := fl.load(a[0], vir.I1, r0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, r2, false); err != nil {
			return err
		}
		fl.alu("cmp", R(r0), Imm(0))
		fl.emit(minst{op: "movcc", cc: ccEQ, d: R(r1), s: R(r2)})
		fl.st(in.Result, r1)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, r1, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 1:
			fl.emit(minst{op: "ldrb", d: R(r0), s: Mem(r1, 0)})
		case 2:
			fl.emit(minst{op: "ldrh", d: R(r0), s: Mem(r1, 0)})
		default:
			fl.emit(minst{op: "ldr", d: R(r0), s: Mem(r1, 0), sz: 4})
		}
		if op == "atomic_load" {
			switch lastOrd(a) {
			case "acquire", "seqcst":
				fl.emit(minst{op: "dmb"})
			}
		}
		fl.st(in.Result, r0)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, r1, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r0, false); err != nil {
			return err
		}
		if op == "atomic_store" {
			switch lastOrd(a) {
			case "release", "seqcst":
				fl.emit(minst{op: "dmb"})
			}
		}
		switch szOf(t) {
		case 1:
			fl.emit(minst{op: "strb", d: Mem(r1, 0), s: R(r0)})
		case 2:
			fl.emit(minst{op: "strh", d: Mem(r1, 0), s: R(r0)})
		default:
			fl.emit(minst{op: "str", d: Mem(r1, 0), s: R(r0), sz: 4})
		}
		if op == "atomic_store" && lastOrd(a) == "seqcst" {
			fl.emit(minst{op: "dmb"})
		}

	case op == "alloca":
		if err := fl.load(a[0], vir.I32, r0, false); err != nil {
			return err
		}
		fl.alu("add", R(r0), Imm(7)) // round size up, keep SP 8-aligned (AAPCS)
		fl.alu("bic", R(r0), Imm(7))
		fl.emit(minst{op: "sub_sp_r", s: R(r0)})
		if in.Align > 8 {
			fl.emit(minst{op: "and_sp", imm: int64(-in.Align)})
		}
		fl.emit(minst{op: "mov_r_sp", d: R(r0)})
		fl.st(in.Result, r0)

	case op == "field":
		off, err := fl.lay.fieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, r0, false); err != nil {
			return err
		}
		if off != 0 {
			fl.emit(minst{op: "movimm", d: R(r1), imm: int64(off)})
			fl.alu("add", R(r0), R(r1))
		}
		fl.st(in.Result, r0)

	case op == "index":
		esz, err := fl.lay.size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, r0, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, r1, true); err != nil { // index is signed (§4)
			return err
		}
		fl.emit(minst{op: "movimm", d: R(r2), imm: int64(esz)})
		fl.emit(minst{op: "mul", d: R(r3), s: R(r1), t: R(r2)})
		fl.alu("add", R(r0), R(r3)) // address arithmetic wraps (§6.2)
		fl.st(in.Result, r0)

	case op == "memcopy" || op == "memset":
		// No rep-string hardware: index loop over r12. dst r0, src/byte r1,
		// len r2, scratch r3.
		if err := fl.load(a[0], vir.Ptr, r0, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, r1, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, r1, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I32, r2, false); err != nil {
			return err
		}
		loop, done := fl.label(), fl.label()
		fl.emit(minst{op: "movimm", d: R(rIP), imm: 0})
		fl.emit(minst{op: "label", lbl: loop})
		fl.alu("cmp", R(rIP), R(r2))
		fl.emit(minst{op: "bcc", cc: ccEQ, lbl: done})
		if op == "memcopy" {
			fl.emit(minst{op: "ldrb_r", d: R(r3), s: R(r1), t: R(rIP)})
			fl.emit(minst{op: "strb_r", d: R(r0), s: R(r3), t: R(rIP)})
		} else {
			fl.emit(minst{op: "strb_r", d: R(r0), s: R(r1), t: R(rIP)})
		}
		fl.alu("add", R(rIP), Imm(1))
		fl.emit(minst{op: "b", lbl: loop})
		fl.emit(minst{op: "label", lbl: done})

	case op == "memmove":
		if err := fl.load(a[0], vir.Ptr, r0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, r1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, r2, false); err != nil {
			return err
		}
		back, bloop, floop, done := fl.label(), fl.label(), fl.label(), fl.label()
		fl.alu("cmp", R(r1), R(r0))
		fl.emit(minst{op: "bcc", cc: ccLO, lbl: back}) // src < dst: copy backward
		fl.emit(minst{op: "movimm", d: R(rIP), imm: 0})
		fl.emit(minst{op: "label", lbl: floop})
		fl.alu("cmp", R(rIP), R(r2))
		fl.emit(minst{op: "bcc", cc: ccEQ, lbl: done})
		fl.emit(minst{op: "ldrb_r", d: R(r3), s: R(r1), t: R(rIP)})
		fl.emit(minst{op: "strb_r", d: R(r0), s: R(r3), t: R(rIP)})
		fl.alu("add", R(rIP), Imm(1))
		fl.emit(minst{op: "b", lbl: floop})
		fl.emit(minst{op: "label", lbl: back}) // descending index
		fl.emit(minst{op: "mov_r", d: R(rIP), s: R(r2)})
		fl.emit(minst{op: "label", lbl: bloop})
		fl.alu("cmp", R(rIP), Imm(0))
		fl.emit(minst{op: "bcc", cc: ccEQ, lbl: done})
		fl.alu("sub", R(rIP), Imm(1))
		fl.emit(minst{op: "ldrb_r", d: R(r3), s: R(r1), t: R(rIP)})
		fl.emit(minst{op: "strb_r", d: R(r0), s: R(r3), t: R(rIP)})
		fl.emit(minst{op: "b", lbl: bloop})
		fl.emit(minst{op: "label", lbl: done})

	case op == "prefetch":
		return nil // advisory (§4); dropped in this bring-up (PLD TODO)

	case op == "fence":
		// Every §4 fence ordering needs a real barrier on ARM (unlike x86-TSO).
		fl.emit(minst{op: "dmb"})
		return nil

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_and" ||
		op == "atomic_or" || op == "atomic_xor" || op == "atomic_xchg":
		if szOf(t) != 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on arm (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, r2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, false); err != nil {
			return err
		}
		fl.emit(minst{op: "dmb"})
		retry := fl.label()
		fl.emit(minst{op: "label", lbl: retry})
		fl.emit(minst{op: "ldrex", d: R(r0), s: Mem(r2, 0)}) // old value
		switch op {
		case "atomic_xchg":
			fl.emit(minst{op: "mov_r", d: R(r3), s: R(r1)})
		case "atomic_add", "atomic_sub":
			fl.emit(minst{op: "mov_r", d: R(r3), s: R(r0)})
			armOp := "add"
			if op == "atomic_sub" {
				armOp = "sub"
			}
			fl.alu(armOp, R(r3), R(r1))
		default:
			fl.emit(minst{op: "mov_r", d: R(r3), s: R(r0)})
			fl.alu(map[string]string{"atomic_and": "and", "atomic_or": "orr", "atomic_xor": "eor"}[op], R(r3), R(r1))
		}
		fl.emit(minst{op: "strex", x: R(rIP), s: R(r3), d: Mem(r2, 0)})
		fl.alu("cmp", R(rIP), Imm(0))
		fl.emit(minst{op: "bcc", cc: ccNE, lbl: retry})
		fl.emit(minst{op: "dmb"})
		fl.st(in.Result, r0) // old value (§4)

	case op == "cmpxchg":
		if szOf(t) != 4 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on arm (TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, r2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, r1, false); err != nil { // expected
			return err
		}
		if err := fl.load(a[2], t, r3, false); err != nil { // desired
			return err
		}
		fl.emit(minst{op: "dmb"})
		retry, fail, done := fl.label(), fl.label(), fl.label()
		fl.emit(minst{op: "label", lbl: retry})
		fl.emit(minst{op: "ldrex", d: R(r0), s: Mem(r2, 0)})
		fl.alu("cmp", R(r0), R(r1))
		fl.emit(minst{op: "bcc", cc: ccNE, lbl: fail})
		fl.emit(minst{op: "strex", x: R(rIP), s: R(r3), d: Mem(r2, 0)})
		fl.alu("cmp", R(rIP), Imm(0))
		fl.emit(minst{op: "bcc", cc: ccNE, lbl: retry})
		fl.emit(minst{op: "b", lbl: done})
		fl.emit(minst{op: "label", lbl: fail})
		fl.emit(minst{op: "clrex"})
		fl.emit(minst{op: "label", lbl: done})
		fl.emit(minst{op: "dmb"})
		fl.st(in.Result, r0) // old value; caller compares with eq (§4)

	case op == "trunc":
		if err := fl.load(a[0], nil, r0, false); err != nil {
			return err
		}
		fl.norm(r0, t)
		fl.st(in.Result, r0)

	case op == "zext":
		if err := fl.load(a[0], nil, r0, false); err != nil {
			return err
		}
		fl.st(in.Result, r0) // slots are already zero-extended

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if it, ok := st.(vir.IntType); ok && it.Bits == 1 {
			if err := fl.load(a[0], st, r0, false); err != nil {
				return err
			}
			fl.emit(minst{op: "rsb", d: R(r0), s: Imm(0)}) // i1 sext: 1 -> -1
		} else {
			if err := fl.load(a[0], st, r0, true); err != nil {
				return err
			}
		}
		fl.norm(r0, t)
		fl.st(in.Result, r0)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on arm (TODO)")
		}
		if err := fl.load(a[0], st, r0, false); err != nil {
			return err
		}
		fl.st(in.Result, r0) // ptr <-> i32 register bits (§4, §19)

	case op == "call":
		return fl.selCall(in)

	case op == "asm":
		return fmt.Errorf("inline asm not lowered on arm (reserved, §4)")

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

	default:
		return fmt.Errorf("op %q not lowered on arm", op)
	}
	return nil
}

func (fl *fnLower) selOverflow(in *vir.Inst) error {
	t, a := in.Suffix, in.Args
	signed := in.Op[0] == 's'
	if err := fl.load(a[0], t, r0, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, r1, signed); err != nil {
		return err
	}
	if szOf(t) == 4 { // hardware flags are exact at 32 bits
		switch in.Op {
		case "uaddo":
			fl.emit(minst{op: "adds", d: R(r0), s: R(r1)})
			fl.setcc(ccHS, r2) // carry set = unsigned overflow
		case "usubo":
			fl.emit(minst{op: "subs", d: R(r0), s: R(r1)})
			fl.setcc(ccLO, r2) // ARM borrow = carry clear
		case "saddo":
			fl.emit(minst{op: "adds", d: R(r0), s: R(r1)})
			fl.setcc(ccVS, r2)
		case "ssubo":
			fl.emit(minst{op: "subs", d: R(r0), s: R(r1)})
			fl.setcc(ccVS, r2)
		case "umulo":
			fl.emit(minst{op: "umull", d: R(r2), x: R(r3), s: R(r0), t: R(r1)})
			fl.alu("cmp", R(r3), Imm(0))
			fl.setcc(ccNE, r2)
		case "smulo":
			fl.emit(minst{op: "smull", d: R(r2), x: R(r3), s: R(r0), t: R(r1)})
			fl.emit(minst{op: "cmp_asr31", d: R(r3), s: R(r2)}) // hi ?= lo >> 31
			fl.setcc(ccNE, r2)
		}
	} else {
		// Narrow widths: compute exactly at 32 bits on extended operands,
		// then overflow iff re-extending the truncated result changes it.
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(r0), R(r1))
		case "usubo", "ssubo":
			fl.alu("sub", R(r0), R(r1))
		case "umulo", "smulo":
			fl.emit(minst{op: "mul", d: R(r2), s: R(r0), t: R(r1)})
			fl.emit(minst{op: "mov_r", d: R(r0), s: R(r2)})
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
		// Extend a masked copy and compare against the full result.
		fl.emit(minst{op: "mov_r", d: R(r1), s: R(r0)})
		fl.norm(r1, t)
		fl.emit(minst{op: ext, d: R(r1), s: R(r1)})
		fl.alu("cmp", R(r1), R(r0))
		fl.setcc(ccNE, r2)
	}
	fl.st(in.Result, r2)
	return nil
}

func (fl *fnLower) typeOfOperand(o vir.Operand) (vir.Type, error) {
	if o.Kind != vir.OIdent {
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
		if args[i].Kind == vir.OOrdering {
			return args[i].Ord
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Calls (AAPCS) and terminators
// ---------------------------------------------------------------------------

// selCall stages every argument in a stack area, then lifts the first four
// into r0-r3 and releases the staging bytes that duplicated them, leaving
// any remaining arguments contiguous at SP for the call (AAPCS: first
// stacked argument at SP). Caller cleans up. Variadic promotion is the
// frontend's job (§4); core-register rules are identical for variadics.
func (fl *fnLower) selCall(in *vir.Inst) error {
	args := in.Args
	var ret vir.Type
	indirect := in.Sig != ""
	if indirect {
		found := false
		for _, s := range fl.m.FnSigs {
			if s.Name == in.Sig {
				ret, found = s.Ret, true
			}
		}
		if !found {
			return fmt.Errorf("fnsig %q not declared", in.Sig)
		}
		args = args[1:] // Args[0] is the callee ptr
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

	stage := int64((4*len(args) + 7) &^ 7) // staging area, SP kept 8-aligned
	if stage > 0 {
		fl.emit(minst{op: "sub_sp", imm: stage})
	}
	for i, a := range args {
		if err := fl.load(a, nil, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "str", d: Mem(rSP, int32(4*i)), s: R(r0), sz: 4})
	}
	if indirect { // callee ptr survives in IP across the register loads
		if err := fl.load(in.Args[0], vir.Ptr, rIP, false); err != nil {
			return err
		}
	}
	nreg := len(args)
	if nreg > 4 {
		nreg = 4
	}
	for i := 0; i < nreg; i++ {
		fl.emit(minst{op: "ldr", d: R(reg(i)), s: Mem(rSP, int32(4*i)), sz: 4})
	}
	cleanup := stage
	if len(args) > 4 {
		fl.emit(minst{op: "add_sp", imm: 16}) // stack args now start at SP
		cleanup = stage - 16
	} else if stage > 0 {
		fl.emit(minst{op: "add_sp", imm: stage})
		cleanup = 0
	}
	if indirect {
		fl.emit(minst{op: "blx_r", s: R(rIP)})
	} else {
		fl.emit(minst{op: "bl_sym", sym: in.Args[0].Ident})
	}
	if cleanup > 0 {
		fl.emit(minst{op: "add_sp", imm: cleanup}) // caller cleans up
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.norm(r0, ret)
		fl.st(in.Result, r0)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Br:
		fl.emit(minst{op: "b", lbl: x.Label})
	case vir.BrIf:
		if err := fl.load(x.Cond, vir.I1, r0, false); err != nil {
			return err
		}
		fl.alu("cmp", R(r0), Imm(0))
		fl.emit(minst{op: "bcc", cc: ccNE, lbl: x.Then})
		fl.emit(minst{op: "b", lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I32
		}
		if err := fl.load(x.Value, vt, r0, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.emit(minst{op: "movimm", d: R(r1), imm: litBits(c.Value, vt, false)})
			fl.alu("cmp", R(r0), R(r1))
			fl.emit(minst{op: "bcc", cc: ccEQ, lbl: c.Label})
		}
		fl.emit(minst{op: "b", lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, r0, false); err != nil {
				return err
			}
		}
		fl.emit(minst{op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(minst{op: "udf"}) // canonical deterministic halt (§6.1)
	case vir.Unreachable:
		fl.emit(minst{op: "udf"}) // defensive; executing it is UB anyway (§6.3)
	default:
		return fmt.Errorf("terminator %T not lowered on arm", t)
	}
	return nil
}

// selTailCall implements guaranteed tail calls (§5) for the eligible shape
// this backend supports: at most four arguments, all in registers, so the
// caller's stack argument area is never rewritten. Stack-argument tailcalls
// are the frame-rewriting TODO.
func (fl *fnLower) selTailCall(x vir.TailCall) error {
	args := x.Args
	indirect := x.Callee == ""
	if indirect {
		args = args[1:]
	}
	if len(args) > 4 {
		return fmt.Errorf("tailcall with %d args exceeds the r0-r3 register set (stack-arg tailcalls TODO)", len(args))
	}
	// Stage on the stack first: arguments may read values that r0-r3 will
	// hold, so evaluate everything before loading the argument registers.
	stage := int64((4*len(args) + 7) &^ 7)
	if stage > 0 {
		fl.emit(minst{op: "sub_sp", imm: stage})
	}
	for i, a := range args {
		if err := fl.load(a, nil, r0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "str", d: Mem(rSP, int32(4*i)), s: R(r0), sz: 4})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, rIP, false); err != nil {
			return err
		}
	}
	for i := range args {
		fl.emit(minst{op: "ldr", d: R(reg(i)), s: Mem(rSP, int32(4*i)), sz: 4})
	}
	if stage > 0 {
		fl.emit(minst{op: "add_sp", imm: stage})
	}
	if indirect {
		fl.emit(minst{op: "epi_jmp_r", s: R(rIP)}) // IP survives the epilogue
	} else {
		fl.emit(minst{op: "epi_jmp_sym", sym: x.Callee})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Globals (static data + relocations). Scalars are serialized in the
// requested arch's byte order; layout offsets are identical either way
// (§7.1 — endianness governs bytes within a scalar, never placement).
// ---------------------------------------------------------------------------

func (lw *lowerer) lowerGlobal(g *vir.Global) (Global, error) {
	sz, err := lw.lay.size(g.Type)
	if err != nil {
		return Global{}, err
	}
	al, err := lw.lay.alignOf(g.Type)
	if err != nil {
		return Global{}, err
	}
	if g.Align > al {
		al = g.Align
	}
	out := Global{Name: g.Name, Size: uint32(sz), Align: uint32(al), Export: g.Export, TLS: g.TLS}
	if _, zero := g.Init.(vir.InitZero); zero {
		return out, nil // BSS-style: Data nil, Size set
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
	lay *layout
	be  bool // big-endian scalar serialization (armeb)
	b   []byte
	fx  []Fixup
}

func (w *dataw) pad(to int) {
	for len(w.b) < to {
		w.b = append(w.b, 0)
	}
}

// scalar appends v's low n bytes in the selected byte order.
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
		sz, err := w.lay.size(t)
		if err != nil {
			return err
		}
		w.pad(len(w.b) + sz)
		return nil
	case vir.InitBytes:
		w.b = append(w.b, x.Data...) // byte arrays are byte-invariant: no swap
		return nil
	case vir.InitAddr:
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: x.Name, Kind: FixupAbs32})
		w.scalar(0, 4)
		return nil
	case vir.InitLit:
		return w.lit(x.Value, t)
	case vir.InitAgg:
		switch tt := t.(type) {
		case vir.StructType:
			base := len(w.b)
			sz, _, offs, err := w.lay.structLayout(tt.Name)
			if err != nil {
				return err
			}
			s := w.lay.structs[tt.Name]
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
			es, err := w.lay.size(tt.Elem)
			if err != nil {
				return err
			}
			for _, e := range x.Elems {
				if err := w.emit(e, tt.Elem); err != nil {
					return err
				}
			}
			w.pad(base + es*tt.Len) // fewer than N elems: remainder is zero (§8)
			return nil
		}
		return fmt.Errorf("aggregate initializer for %s", t)
	}
	return fmt.Errorf("unknown initializer form")
}

func (w *dataw) lit(o vir.Operand, t vir.Type) error {
	switch o.Kind {
	case vir.OInt:
		sz, err := w.lay.size(t)
		if err != nil {
			return err
		}
		w.scalar(uint64(o.Int), sz)
		return nil
	case vir.OBool:
		v := uint64(0)
		if o.Bool {
			v = 1
		}
		w.scalar(v, 1)
		return nil
	case vir.ONull:
		w.scalar(0, 4)
		return nil
	case vir.OFloat:
		switch t {
		case vir.F64:
			w.scalar(math.Float64bits(o.Float), 8)
			return nil
		case vir.F32:
			w.scalar(uint64(math.Float32bits(float32(o.Float))), 4)
			return nil
		}
		return fmt.Errorf("f16 initializers not yet emitted on arm (TODO)")
	case vir.OVecLit:
		vt, ok := t.(vir.VecType)
		if !ok {
			return fmt.Errorf("vector literal for %s", t)
		}
		es, err := w.lay.size(vt.Elem)
		if err != nil {
			return err
		}
		for _, v := range o.Vec {
			w.scalar(uint64(v), es)
		}
		return nil
	}
	return fmt.Errorf("literal kind %d in initializer", o.Kind)
}