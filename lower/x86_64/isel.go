package x86_64

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Lower converts a verified module into an x86_64 Program (arrow 3).
// The module must have passed vir.Verify; Lower assumes the §9 obligations.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target != nil && m.Target.Arch != "x86_64" {
		return nil, fmt.Errorf("lower/x86_64: module targets arch %q, not x86_64", m.Target.Arch)
	}
	lw := &lowerer{
		m: m, lay: newLayout(m),
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

	p := &Program{}
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
	kinds  map[string]string
	consts map[string]*vir.Const
}

func (lw *lowerer) lookupCallable(name string) (ret vir.Type, params []vir.Param, variadic, ok bool) {
	for _, g := range lw.m.Externs {
		for _, e := range g.Fns {
			if e.Name == name {
				return e.Ret, e.Params, e.Variadic, true
			}
		}
	}
	for _, f := range lw.m.Funcs {
		if f.Name == name {
			return f.Ret, f.Params, false, true
		}
	}
	return nil, nil, false, false
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
	// Spill register-passed parameters into their home slots first — the
	// scratch set (which includes the argument registers) is live-in here
	// and dead everywhere else.
	for i, p := range f.Params {
		if i >= len(argRegs) {
			break // stack params already have homes at [rbp+16+8k]
		}
		fl.emit(minst{op: "mov", d: Slot(p.Name), s: R(argRegs[i]), sz: 8})
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
	code, fixups, err := encodeFunc(fl.b, fr.local)
	if err != nil {
		return Func{}, err
	}
	return Func{Name: f.Name, Code: code, Align: 16, Export: f.Export, Fixups: fixups}, nil
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

func (fl *fnLower) emit(i minst) { fl.b = append(fl.b, i) }
func (fl *fnLower) mov(d, s opr) { fl.emit(minst{op: "mov", d: d, s: s, sz: 8}) }
func (fl *fnLower) alu(op string, d, s opr, sz int) {
	fl.emit(minst{op: op, d: d, s: s, sz: sz})
}
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}

// typeFunc mirrors the verifier's result-type computation for the subset this
// backend supports (input is verified, so lookups cannot fail semantically).
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
		if x.Bits > 64 {
			return fmt.Errorf("i%d values not yet lowered on x86_64 (register pairs TODO)", x.Bits)
		}
		return nil
	case vir.PtrType:
		return nil
	case vir.FloatType:
		return fmt.Errorf("floating-point lowering not implemented on x86_64 (SSE tier TODO)")
	case vir.VecType:
		return fmt.Errorf("vector lowering not implemented on x86_64 (tier TODO, §10.4)")
	}
	return fmt.Errorf("type %s cannot be a named value on x86_64", t)
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
		ret, _, _, ok := fl.lookupCallable(in.Args[0].Ident)
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
		return 64
	}
	return 64
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

// opSz is the ALU width for a type: 8 (REX.W) for 64-bit/ptr, else 4 —
// 32-bit operations zero the upper half, which the slot invariant relies on.
func opSz(t vir.Type) int {
	if szOf(t) == 8 {
		return 8
	}
	return 4
}

// litBits masks v to t's width, sign- or zero-extending back to 64 bits.
func litBits(v int64, t vir.Type, signed bool) int64 {
	b := uint(bitsOf(t))
	if b >= 64 {
		return v
	}
	mask := uint64(1)<<b - 1
	u := uint64(v) & mask
	if signed && u&(1<<(b-1)) != 0 {
		u |= ^mask
	}
	return int64(u)
}

// load materializes operand o (of type t) into r as a 64-bit value.
// Values narrower than 64 bits live zero-extended in their slots; signed=true
// requests a sign-extended materialization instead (for signed
// compares/div/shift). t == nil means "use the slot's 8 bytes as-is".
func (fl *fnLower) load(o vir.Operand, t vir.Type, r reg, signed bool) error {
	switch o.Kind {
	case vir.OInt:
		fl.mov(R(r), Imm(litBits(o.Int, t, signed)))
	case vir.OBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		fl.mov(R(r), Imm(v))
	case vir.ONull:
		fl.mov(R(r), Imm(0))
	case vir.OFloat:
		return fmt.Errorf("float operands not lowered on x86_64 (TODO)")
	case vir.OIdent:
		switch fl.kinds[o.Ident] {
		case "const":
			c := fl.consts[o.Ident]
			return fl.load(c.Value, c.Type, r, signed)
		case "global", "fn", "extern":
			// A name in operand position yields its address (§4, Addresses):
			// lea r, [rip+sym] — PIC-clean by construction.
			fl.mov(R(r), SymAddr(o.Ident))
		case "struct", "fnsig":
			return fmt.Errorf("entity %q used as runtime operand", o.Ident)
		default: // local value slot
			var sz int
			if t == nil {
				sz = 8
			} else {
				vt, ok := fl.types[o.Ident]
				if !ok {
					return fmt.Errorf("value %q has no type (isel bug)", o.Ident)
				}
				sz = szOf(vt)
			}
			switch {
			case sz == 8:
				fl.emit(minst{op: "mov", d: R(r), s: Slot(o.Ident), sz: 8})
			case signed:
				fl.emit(minst{op: "movsx", d: R(r), s: Slot(o.Ident), sz: sz})
			default:
				fl.emit(minst{op: "movzx", d: R(r), s: Slot(o.Ident), sz: sz})
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on x86_64", o.Kind)
	}
	return nil
}

// st writes r (already normalized) into name's 8-byte home slot.
func (fl *fnLower) st(name string, r reg) {
	fl.emit(minst{op: "mov", d: Slot(name), s: R(r), sz: 8})
}

// norm re-establishes the zero-extended-slot invariant after wrapping or
// sign-extending operations.
func (fl *fnLower) norm(r reg, t vir.Type) {
	if it, ok := t.(vir.IntType); ok && it.Bits == 1 {
		fl.alu("and", R(r), Imm(1), 4)
		return
	}
	switch szOf(t) {
	case 1, 2:
		fl.emit(minst{op: "movzx", d: R(r), s: R(r), sz: szOf(t)})
	case 4:
		fl.emit(minst{op: "movzx", d: R(r), s: R(r), sz: 4}) // mov r32, r32
	}
}

// ---------------------------------------------------------------------------
// Instruction selection
// ---------------------------------------------------------------------------

func (fl *fnLower) selInst(in *vir.Inst) error {
	op, t, a := in.Op, in.Suffix, in.Args
	signedCmp := map[string]byte{"slt": ccL, "sle": ccLE, "sgt": ccG, "sge": ccGE}
	unsignedCmp := map[string]byte{"eq": ccE, "ne": ccNE, "ult": ccB, "ule": ccBE, "ugt": ccA, "uge": ccAE}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, rRAX, false); err != nil {
			return err
		}
		fl.st(in.Result, rRAX)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, rRAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, false); err != nil {
			return err
		}
		fl.alu(op, R(rRAX), R(rRCX), opSz(t))
		if op == "add" || op == "sub" {
			fl.norm(rRAX, t) // wrap mod 2^N (§4)
		}
		fl.st(in.Result, rRAX)

	case op == "mul":
		if err := fl.load(a[0], t, rRAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "imul", d: R(rRAX), s: R(rRCX), sz: opSz(t)})
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case op == "neg" || op == "not":
		if err := fl.load(a[0], t, rRAX, false); err != nil {
			return err
		}
		fl.emit(minst{op: op, s: R(rRAX), sz: opSz(t)})
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case op == "abs": // signed; abs(INT_MIN) wraps (§4)
		if err := fl.load(a[0], t, rRAX, true); err != nil {
			return err
		}
		w := opSz(t)
		fl.mov(R(rRCX), R(rRAX))
		fl.emit(minst{op: "sar", d: R(rRCX), s: Imm(int64(w*8 - 1)), sz: w})
		fl.alu("xor", R(rRAX), R(rRCX), w)
		fl.alu("sub", R(rRAX), R(rRCX), w)
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case op == "udiv" || op == "urem":
		if err := fl.load(a[0], t, rRAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.alu("xor", R(rRDX), R(rRDX), 4)
		fl.emit(minst{op: "div", s: R(rRCX), sz: w}) // zero divisor -> #DE trap (§6.1)
		r := rRAX
		if op == "urem" {
			r = rRDX
		}
		fl.st(in.Result, r)

	case op == "sdiv" || op == "srem":
		// TODO(§6.1): narrow INT_MIN/-1 (e.g. i8 -128/-1) must trap but the
		// widened 32-bit idiv wraps instead; needs an explicit check for sz<4.
		// i32 and i64 are exact: hardware idiv traps on INT_MIN/-1 itself.
		if err := fl.load(a[0], t, rRAX, true); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, true); err != nil {
			return err
		}
		w := opSz(t)
		if w == 8 {
			fl.emit(minst{op: "cqo"})
		} else {
			fl.emit(minst{op: "cdq"})
		}
		fl.emit(minst{op: "idiv", s: R(rRCX), sz: w})
		r := rRAX
		if op == "srem" {
			r = rRDX
		}
		fl.norm(r, t)
		fl.st(in.Result, r)

	case op == "shl" || op == "lshr" || op == "ashr":
		signedV := op == "ashr"
		if err := fl.load(a[0], t, rRAX, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, false); err != nil {
			return err
		}
		w := opSz(t)
		if bitsOf(t) < 32 { // count mod N (§4); hardware masks mod 32/64 only
			fl.alu("and", R(rRCX), Imm(int64(bitsOf(t)-1)), 4)
		}
		x86op := map[string]string{"shl": "shl", "lshr": "shr", "ashr": "sar"}[op]
		fl.emit(minst{op: x86op, d: R(rRAX), sz: w}) // by CL
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, rRAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, false); err != nil {
			return err
		}
		x86op := "rol"
		if op == "rotr" {
			x86op = "ror"
		}
		// Rotate at the exact width: rotation by the width is the identity,
		// so hardware's mod-32/64 count matches §4's mod-N for N | 64.
		fl.emit(minst{op: x86op, d: R(rRAX), sz: szOf(t)})
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, rRAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(rRAX), R(rRCX), 8) // extended to 64: compare wide
		cc := map[string]byte{"smin": ccG, "smax": ccL, "umin": ccA, "umax": ccB}[op]
		fl.emit(minst{op: "cmovcc", cc: cc, d: R(rRAX), s: R(rRCX), sz: 8})
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, rRAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(rRAX), R(rRCX), 8) // operands are 64-bit extended
		fl.emit(minst{op: "setcc", cc: cc, d: R(rRAX)})
		fl.emit(minst{op: "movzx", d: R(rRAX), s: R(rRAX), sz: 1})
		fl.st(in.Result, rRAX)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on x86_64 (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, rRAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, signed); err != nil {
			return err
		}
		if szOf(t) == 8 { // full 128-bit product; take RDX
			m := "mul1"
			if signed {
				m = "imul1"
			}
			fl.emit(minst{op: m, s: R(rRCX), sz: 8})
			fl.st(in.Result, rRDX)
		} else { // narrow: full product fits in 64 bits; shift the high half down
			fl.emit(minst{op: "imul", d: R(rRAX), s: R(rRCX), sz: 8})
			sh := "shr"
			if signed {
				sh = "sar"
			}
			fl.emit(minst{op: sh, d: R(rRAX), s: Imm(int64(bitsOf(t))), sz: 8})
			fl.norm(rRAX, t)
			fl.st(in.Result, rRAX)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on x86_64 (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, rRCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.emit(minst{op: "bsr", d: R(rRDX), s: R(rRCX), sz: w})
		fl.mov(R(rRAX), Imm(-1)) // sentinel: (N-1) - (-1) = N for zero input
		fl.emit(minst{op: "cmovcc", cc: ccNE, d: R(rRAX), s: R(rRDX), sz: 8})
		fl.mov(R(rRCX), Imm(int64(bitsOf(t)-1)))
		fl.alu("sub", R(rRCX), R(rRAX), 8)
		fl.st(in.Result, rRCX)

	case op == "cttz":
		if err := fl.load(a[0], t, rRCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.emit(minst{op: "bsf", d: R(rRDX), s: R(rRCX), sz: w})
		fl.mov(R(rRAX), Imm(int64(bitsOf(t)))) // zero input -> N
		fl.emit(minst{op: "cmovcc", cc: ccNE, d: R(rRAX), s: R(rRDX), sz: 8})
		fl.st(in.Result, rRAX)

	case op == "popcnt":
		// TODO(§10.4): gate on a POPCNT-capable feature tier.
		if err := fl.load(a[0], t, rRCX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "popcnt", d: R(rRAX), s: R(rRCX), sz: opSz(t)})
		fl.st(in.Result, rRAX)

	case op == "bswap":
		if err := fl.load(a[0], t, rRAX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 8, 4:
			fl.emit(minst{op: "bswap", d: R(rRAX), sz: szOf(t)})
		default: // i16: ror ax, 8 (i8 is rejected by the verifier, §9.20)
			fl.emit(minst{op: "ror", d: R(rRAX), s: Imm(8), sz: 2})
			fl.norm(rRAX, t)
		}
		fl.st(in.Result, rRAX)

	case op == "bitrev":
		return fmt.Errorf("bitrev not yet lowered on x86_64 (SWAR sequence TODO)")

	case op == "select":
		if err := fl.load(a[0], vir.I1, rRAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRCX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, rRDX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "test", d: R(rRAX), s: R(rRAX), sz: 4})
		fl.emit(minst{op: "cmovcc", cc: ccE, d: R(rRCX), s: R(rRDX), sz: 8})
		fl.st(in.Result, rRCX)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		// Aligned scalar loads are single accesses on x86-64; acquire/seqcst
		// atomic loads need no fence under TSO.
		if err := fl.load(a[0], vir.Ptr, rRCX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 8:
			fl.emit(minst{op: "mov", d: R(rRAX), s: Mem(rRCX, 0), sz: 8})
		case 4:
			fl.emit(minst{op: "movzx", d: R(rRAX), s: Mem(rRCX, 0), sz: 4}) // mov r32, m32
		default:
			fl.emit(minst{op: "movzx", d: R(rRAX), s: Mem(rRCX, 0), sz: szOf(t)})
		}
		fl.st(in.Result, rRAX)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, rRCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRAX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "mov", d: Mem(rRCX, 0), s: R(rRAX), sz: szOf(t)})
		if op == "atomic_store" && lastOrd(a) == "seqcst" {
			fl.emit(minst{op: "mfence"}) // seqcst store needs a full barrier on x86
		}

	case op == "alloca":
		if err := fl.load(a[0], vir.I64, rRAX, false); err != nil {
			return err
		}
		fl.alu("add", R(rRAX), Imm(15), 8) // round size up, keep RSP 16-aligned
		fl.alu("and", R(rRAX), Imm(-16), 8)
		fl.alu("sub", R(rRSP), R(rRAX), 8)
		if in.Align > 16 {
			fl.alu("and", R(rRSP), Imm(int64(-in.Align)), 8)
		}
		fl.st(in.Result, rRSP)

	case op == "field":
		off, err := fl.lay.fieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, rRAX, false); err != nil {
			return err
		}
		if off != 0 {
			fl.alu("add", R(rRAX), Imm(int64(off)), 8)
		}
		fl.st(in.Result, rRAX)

	case op == "index":
		esz, err := fl.lay.size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, rRAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, rRCX, true); err != nil { // index is signed (§4)
			return err
		}
		fl.emit(minst{op: "imul3", d: R(rRCX), s: R(rRCX), imm: int64(esz), sz: 8})
		fl.alu("add", R(rRAX), R(rRCX), 8) // address arithmetic wraps (§6.2)
		fl.st(in.Result, rRAX)

	case op == "memcopy" || op == "memset":
		if err := fl.load(a[0], vir.Ptr, rRDI, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, rRSI, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, rRAX, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I64, rRCX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "cld"})
		if op == "memcopy" {
			fl.emit(minst{op: "rep_movsb"}) // count in RCX (64-bit address size)
		} else {
			fl.emit(minst{op: "rep_stosb"})
		}

	case op == "memmove":
		fwd, done := fl.label(), fl.label()
		if err := fl.load(a[0], vir.Ptr, rRDI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, rRSI, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, rRCX, false); err != nil {
			return err
		}
		fl.alu("cmp", R(rRSI), R(rRDI), 8)
		fl.emit(minst{op: "jcc", cc: ccAE, lbl: fwd}) // src >= dst: forward is safe
		fl.alu("add", R(rRSI), R(rRCX), 8)            // else copy backward
		fl.alu("sub", R(rRSI), Imm(1), 8)
		fl.alu("add", R(rRDI), R(rRCX), 8)
		fl.alu("sub", R(rRDI), Imm(1), 8)
		fl.emit(minst{op: "std"})
		fl.emit(minst{op: "rep_movsb"})
		fl.emit(minst{op: "cld"})
		fl.emit(minst{op: "jmp", lbl: done})
		fl.emit(minst{op: "label", lbl: fwd})
		fl.emit(minst{op: "cld"})
		fl.emit(minst{op: "rep_movsb"})
		fl.emit(minst{op: "label", lbl: done})

	case op == "prefetch":
		return nil // advisory (§4); dropped in this bring-up

	case op == "fence":
		if lastOrd(a) == "seqcst" {
			fl.emit(minst{op: "mfence"})
		}
		return nil // acquire/release/acqrel fences are compiler-only on x86 TSO

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_xchg":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86_64 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, rRCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRAX, false); err != nil {
			return err
		}
		switch op {
		case "atomic_sub":
			fl.emit(minst{op: "neg", s: R(rRAX), sz: w})
			fallthrough
		case "atomic_add":
			fl.emit(minst{op: "lock_xadd", d: Mem(rRCX, 0), s: R(rRAX), sz: w})
		case "atomic_xchg":
			fl.emit(minst{op: "xchg", d: Mem(rRCX, 0), s: R(rRAX), sz: w}) // implicitly locked
		}
		fl.st(in.Result, rRAX) // old value (§4)

	case op == "atomic_and" || op == "atomic_or" || op == "atomic_xor":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86_64 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, rRSI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRDX, false); err != nil {
			return err
		}
		loop := fl.label()
		fl.emit(minst{op: "mov", d: R(rRAX), s: Mem(rRSI, 0), sz: w})
		fl.emit(minst{op: "label", lbl: loop})
		fl.mov(R(rRCX), R(rRAX))
		fl.alu(op[len("atomic_"):], R(rRCX), R(rRDX), w)
		fl.emit(minst{op: "lock_cmpxchg", d: Mem(rRSI, 0), s: R(rRCX), sz: w})
		fl.emit(minst{op: "jcc", cc: ccNE, lbl: loop})
		fl.st(in.Result, rRAX)

	case op == "cmpxchg":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on x86_64 (i128 needs cmpxchg16b tier, TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, rRCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rRAX, false); err != nil { // expected -> RAX
			return err
		}
		if err := fl.load(a[2], t, rRDX, false); err != nil { // desired
			return err
		}
		fl.emit(minst{op: "lock_cmpxchg", d: Mem(rRCX, 0), s: R(rRDX), sz: w})
		fl.st(in.Result, rRAX) // old value (§4)

	case op == "trunc":
		if err := fl.load(a[0], nil, rRAX, false); err != nil {
			return err
		}
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case op == "zext":
		if err := fl.load(a[0], nil, rRAX, false); err != nil {
			return err
		}
		fl.st(in.Result, rRAX) // slots are already zero-extended

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if it, ok := st.(vir.IntType); ok && it.Bits == 1 {
			if err := fl.load(a[0], st, rRAX, false); err != nil {
				return err
			}
			fl.emit(minst{op: "neg", s: R(rRAX), sz: 8}) // i1 sext: 1 -> -1
		} else {
			if err := fl.load(a[0], st, rRAX, true); err != nil {
				return err
			}
		}
		fl.norm(rRAX, t)
		fl.st(in.Result, rRAX)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on x86_64 (TODO)")
		}
		if err := fl.load(a[0], st, rRAX, false); err != nil {
			return err
		}
		fl.st(in.Result, rRAX) // ptr <-> i64 register bits (§4, §9.19)

	case op == "call":
		return fl.selCall(in)

	case op == "asm":
		return fmt.Errorf("inline asm not lowered on x86_64 (reserved, §4)")

	case op == "fdemote" || op == "fpromote" || op == "sfromint" || op == "ufromint" ||
		op == "stoint" || op == "utoint" || op == "stoint_sat" || op == "utoint_sat" ||
		op == "sqrt" || op == "fma" || op == "copysign" || op == "floor" || op == "ceil" ||
		op == "trunc_f" || op == "nearest" || op == "min" || op == "max":
		return fmt.Errorf("floating-point op %q not lowered on x86_64 (SSE tier TODO)", op)

	case op == "splat" || op == "extract" || op == "insert" || op == "shuffle" ||
		op == "masked_load" || op == "masked_store" || op == "gather" || op == "scatter" ||
		op == "reduce_add" || op == "reduce_min" || op == "reduce_max" ||
		op == "reduce_and" || op == "reduce_or" || op == "reduce_xor":
		return fmt.Errorf("vector op %q not lowered on x86_64 (tier TODO, §10.4)", op)

	default:
		return fmt.Errorf("op %q not lowered on x86_64", op)
	}
	return nil
}

func (fl *fnLower) selOverflow(in *vir.Inst) error {
	t, a := in.Suffix, in.Args
	signed := in.Op[0] == 's'
	if err := fl.load(a[0], t, rRAX, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, rRCX, signed); err != nil {
		return err
	}
	w := szOf(t)
	if w == 4 || w == 8 { // hardware flags are exact at the native widths
		var cc byte
		switch in.Op {
		case "uaddo", "usubo":
			cc = ccB // carry / borrow
		default:
			cc = ccO // saddo/ssubo/smulo; one-operand MUL sets CF=OF too
		}
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(rRAX), R(rRCX), w)
		case "usubo", "ssubo":
			fl.alu("sub", R(rRAX), R(rRCX), w)
		case "umulo":
			fl.emit(minst{op: "mul1", s: R(rRCX), sz: w})
		case "smulo":
			fl.emit(minst{op: "imul1", s: R(rRCX), sz: w})
		}
		fl.emit(minst{op: "setcc", cc: cc, d: R(rRAX)})
	} else {
		// Narrow widths: compute exactly at 64 bits on extended operands, then
		// overflow iff re-extending the truncated result changes it.
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(rRAX), R(rRCX), 8)
		case "usubo", "ssubo":
			fl.alu("sub", R(rRAX), R(rRCX), 8)
		case "umulo", "smulo":
			fl.emit(minst{op: "imul", d: R(rRAX), s: R(rRCX), sz: 8})
		}
		ext := "movzx"
		if signed {
			ext = "movsx"
		}
		fl.emit(minst{op: ext, d: R(rRCX), s: R(rRAX), sz: w})
		fl.alu("cmp", R(rRCX), R(rRAX), 8)
		fl.emit(minst{op: "setcc", cc: ccNE, d: R(rRAX)})
	}
	fl.emit(minst{op: "movzx", d: R(rRAX), s: R(rRAX), sz: 1})
	fl.st(in.Result, rRAX)
	return nil
}

func (fl *fnLower) typeOfOperand(o vir.Operand) (vir.Type, error) {
	if o.Kind != vir.OIdent {
		return nil, fmt.Errorf("conversion source must be a named value or const on x86_64")
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
// Calls (System V AMD64) and terminators
// ---------------------------------------------------------------------------

// selCall implements the SysV integer calling sequence: first six arguments
// in RDI/RSI/RDX/RCX/R8/R9, the rest in 8-byte stack slots, RSP 16-aligned
// at the call, AL = 0 for variadic callees (no vector args lowered yet).
//
// Because the argument registers double as isel scratch, arguments are first
// evaluated one at a time into a staging area ([rsp+0..] = stack args in
// place, above them the register-arg staging), then the register args are
// loaded last, when no further evaluation can clobber them.
func (fl *fnLower) selCall(in *vir.Inst) error {
	args := in.Args
	var params []vir.Param
	var ret vir.Type
	variadic := false
	indirect := in.Sig != ""
	if indirect {
		for _, s := range fl.m.FnSigs {
			if s.Name == in.Sig {
				ret = s.Ret
				variadic = s.Variadic
				for _, pt := range s.Params {
					params = append(params, vir.Param{Type: pt})
				}
			}
		}
		args = args[1:] // Args[0] is the callee ptr
	} else {
		ret, params, variadic, _ = fl.lookupCallable(args[0].Ident)
		args = args[1:]
	}
	if !vir.IsVoid(ret) {
		if err := fl.checkValueType(ret); err != nil {
			return err
		}
	}
	for i := range args {
		if i < len(params) && params[i].ByVal != "" {
			// TODO(§7.2): SysV AMD64 byval needs the INTEGER/MEMORY
			// classification algorithm; not wired yet.
			return fmt.Errorf("byval struct passing not yet lowered on x86_64 (SysV classification TODO)")
		}
	}
	// sret is just the first ptr argument at this level; it lands in RDI as
	// the ABI wants. TODO(§7.2): the callee must also return the sret pointer
	// in RAX per SysV — harmless to omit for our own internal calls, needed
	// for full C interop.

	nReg := len(args)
	if nReg > len(argRegs) {
		nReg = len(argRegs)
	}
	nStack := len(args) - nReg
	total := (8*nStack + 8*nReg + 15) &^ 15 // stays 16-aligned at the call
	if total > 0 {
		fl.alu("sub", R(rRSP), Imm(int64(total)), 8)
	}
	stage := func(i int) int32 { // staging offset for arg i
		if i >= nReg {
			return int32(8 * (i - nReg)) // stack args sit exactly where cdecl-
		} //                                style SysV wants them: [rsp+0..]
		return int32(8*nStack + 8*i) // reg args staged above the stack args
	}
	for i, a := range args {
		// Variadic promotion is the frontend's job (§4); pass bits as-is.
		if err := fl.load(a, nil, rRAX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "mov", d: Mem(rRSP, stage(i)), s: R(rRAX), sz: 8})
	}
	// Callee pointer next (may itself clobber scratch), staged in R10 —
	// caller-saved, not an argument register.
	if indirect {
		if err := fl.load(in.Args[0], vir.Ptr, rR10, false); err != nil {
			return err
		}
	}
	// Now fill the argument registers; nothing clobbers them past this point.
	for i := 0; i < nReg; i++ {
		fl.emit(minst{op: "mov", d: R(argRegs[i]), s: Mem(rRSP, stage(i)), sz: 8})
	}
	if variadic {
		fl.alu("xor", R(rRAX), R(rRAX), 4) // AL = 0: no vector registers used
	}
	if indirect {
		fl.emit(minst{op: "call_r", s: R(rR10)})
	} else {
		fl.emit(minst{op: "call_sym", sym: in.Args[0].Ident})
	}
	if total > 0 {
		fl.alu("add", R(rRSP), Imm(int64(total)), 8) // caller owns the area
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.norm(rRAX, ret)
		fl.st(in.Result, rRAX)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Br:
		fl.emit(minst{op: "jmp", lbl: x.Label})
	case vir.BrIf:
		if err := fl.load(x.Cond, vir.I1, rRAX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "test", d: R(rRAX), s: R(rRAX), sz: 4})
		fl.emit(minst{op: "jcc", cc: ccNE, lbl: x.Then})
		fl.emit(minst{op: "jmp", lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I64
		}
		if err := fl.load(x.Value, vt, rRAX, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.mov(R(rRCX), Imm(litBits(c.Value, vt, false))) // may need imm64
			fl.alu("cmp", R(rRAX), R(rRCX), 8)
			fl.emit(minst{op: "jcc", cc: ccE, lbl: c.Label})
		}
		fl.emit(minst{op: "jmp", lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, rRAX, false); err != nil {
				return err
			}
		}
		fl.emit(minst{op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(minst{op: "ud2"}) // canonical deterministic halt (§6.1)
	case vir.Unreachable:
		fl.emit(minst{op: "ud2"}) // defensive; executing it is UB anyway (§6.3)
	default:
		return fmt.Errorf("terminator %T not lowered on x86_64", t)
	}
	return nil
}

// selTailCall implements guaranteed tail calls (§5). With six register
// arguments, tailcalls of up to six args need no stack writes at all; args
// 7+ must fit inside the caller's own incoming stack-argument area (which
// SysV lets the callee overwrite).
func (fl *fnLower) selTailCall(x vir.TailCall) error {
	args := x.Args
	indirect := x.Callee == ""
	variadic := false
	if indirect {
		args = args[1:]
		for _, s := range fl.m.FnSigs {
			if s.Name == x.Sig {
				variadic = s.Variadic
			}
		}
	} else {
		_, _, v, _ := fl.lookupCallable(x.Callee)
		variadic = v
	}
	needStack := len(args) - len(argRegs)
	if needStack < 0 {
		needStack = 0
	}
	haveStack := len(fl.f.Params) - len(argRegs)
	if haveStack < 0 {
		haveStack = 0
	}
	if needStack > haveStack {
		return fmt.Errorf("tailcall with %d stack-arg slots exceeds caller's %d incoming slots (frame-growing tailcalls TODO)", needStack, haveStack)
	}
	// Evaluate all args first (they may read the params we're about to
	// overwrite), staging them with pushes, then pop them into their final
	// homes in reverse order. Pushes move RSP, but the epilogue restores RSP
	// from RBP, and every push is popped before the jump.
	for _, a := range args {
		if err := fl.load(a, nil, rRAX, false); err != nil {
			return err
		}
		fl.emit(minst{op: "push", s: R(rRAX)})
	}
	if indirect { // callee ptr staged in R10 across the pops
		if err := fl.load(x.Args[0], vir.Ptr, rR10, false); err != nil {
			return err
		}
	}
	for i := len(args) - 1; i >= 0; i-- {
		if i < len(argRegs) {
			fl.emit(minst{op: "pop", d: R(argRegs[i])})
		} else {
			fl.emit(minst{op: "pop", d: R(rRAX)})
			fl.emit(minst{op: "mov", d: Mem(rRBP, int32(16+8*(i-len(argRegs)))), s: R(rRAX), sz: 8})
		}
	}
	if variadic {
		fl.alu("xor", R(rRAX), R(rRAX), 4) // AL = 0
	}
	if indirect {
		fl.emit(minst{op: "epi_jmp_r", s: R(rR10)})
	} else {
		fl.emit(minst{op: "epi_jmp_sym", sym: x.Callee})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Globals (static data + relocations)
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
	w := &dataw{lay: lw.lay}
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
	b   []byte
	fx  []Fixup
}

func (w *dataw) pad(to int) {
	for len(w.b) < to {
		w.b = append(w.b, 0)
	}
}

func (w *dataw) le(v uint64, n int) {
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
		w.b = append(w.b, x.Data...)
		return nil
	case vir.InitAddr:
		// 8-byte absolute pointer: R_X86_64_64 shape.
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: x.Name, Kind: FixupAbs64})
		w.le(0, 8)
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
			var s *vir.Struct = w.lay.structs[tt.Name]
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
		w.le(uint64(o.Int), sz)
		return nil
	case vir.OBool:
		v := uint64(0)
		if o.Bool {
			v = 1
		}
		w.le(v, 1)
		return nil
	case vir.ONull:
		w.le(0, 8)
		return nil
	case vir.OFloat:
		switch t {
		case vir.F64:
			w.le(math.Float64bits(o.Float), 8)
			return nil
		case vir.F32:
			w.le(uint64(math.Float32bits(float32(o.Float))), 4)
			return nil
		}
		return fmt.Errorf("f16 initializers not yet emitted on x86_64 (TODO)")
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
			w.le(uint64(v), es)
		}
		return nil
	}
	return fmt.Errorf("literal kind %d in initializer", o.Kind)
}