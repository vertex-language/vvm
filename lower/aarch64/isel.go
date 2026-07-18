package aarch64

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Lower converts a verified module into an aarch64 Program (arrow 3) for
// the given arch ("aarch64" or "aarch64_be" — the §10.1 canonical
// spellings, arch.go). The module must have passed vir.Verify; Lower
// assumes the §9 obligations. A module with no target-decl is lowered for
// whichever arch is requested (target selection is a build input, §10); a
// module that declares a target must agree with the requested arch.
func Lower(m *vir.Module, arch Arch) (*Program, error) {
	if !arch.valid() {
		return nil, fmt.Errorf("lower/aarch64: unknown arch %q (want %q or %q)", arch, ArchAArch64, ArchAArch64BE)
	}
	if m.Target != nil && string(arch) != m.Target.Arch {
		return nil, fmt.Errorf("lower/aarch64: module targets arch %q, build requested %q (§10.6: the two must agree)", m.Target.Arch, arch)
	}
	lw := &lowerer{
		m: m, lay: newLayout(m), arch: arch,
		kinds:  map[string]string{},
		consts: map[string]*vir.Const{},
		tls:    map[string]bool{},
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
		if g.TLS {
			lw.tls[g.Name] = true
		}
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
	tls    map[string]bool
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
			return Func{}, fmt.Errorf("byval/sret not yet lowered on aarch64 (AAPCS64 aggregate passing + x8 TODO)")
		}
	}
	// Spill every parameter into its home slot before anything can clobber
	// x0-x7. AAPCS64 leaves the high bits of narrow arguments unspecified,
	// so narrow parameters are normalized on the way in (frame.go).
	for i, p := range f.Params {
		if i < 8 {
			fl.normReg(reg(i), p.Type)
			fl.st(p.Name, reg(i))
		} else {
			fl.emit(minst{op: "ldr", d: R(x0), s: Mem(rFP, int32(16+8*(i-8))), sz: 8})
			fl.normReg(x0, p.Type)
			fl.st(p.Name, x0)
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

func (fl *fnLower) emit(i minst)                     { fl.b = append(fl.b, i) }
func (fl *fnLower) alu(op string, sz int, d, s opr)  { fl.emit(minst{op: op, sz: sz, d: d, s: s}) }
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}

// typeFunc mirrors the verifier's result-type computation for the subset
// this backend supports. Identical to lower/x86 and lower/arm.
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
		return 64
	case nil:
		return 64
	}
	return 64
}

// szMachine picks the W (4) or X (8) operation width for a type.
func szMachine(t vir.Type) int {
	if bitsOf(t) > 32 {
		return 8
	}
	return 4
}

// szOf is the memory access size of a type.
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

// litBits masks v to t's width; signed requests sign-extension to the
// machine width (W ops read the low 32 bits, so bits 63:32 stay zero for
// W-width types — the zero-extended-slot invariant is preserved).
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

// load materializes operand o (of type t) into r. Values narrower than 64
// bits live zero-extended in their 8-byte slots; signed requests a
// sign-extended materialization at the type's machine width (signed
// compares/div/shifts on W-width types).
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
		return fmt.Errorf("float operands not lowered on aarch64 (TODO)")
	case vir.OIdent:
		switch fl.kinds[o.Ident] {
		case "const":
			c := fl.consts[o.Ident]
			return fl.load(c.Value, c.Type, r, signed)
		case "global", "fn", "extern":
			// A name in operand position yields its address (§4, Addresses).
			if fl.tls[o.Ident] {
				return fmt.Errorf("address of tls global %q not lowered on aarch64 (TPIDR_EL0 + TLS relocs TODO)", o.Ident)
			}
			fl.emit(minst{op: "movsym", d: R(r), sym: o.Ident})
		case "struct", "fnsig":
			return fmt.Errorf("entity %q used as runtime operand", o.Ident)
		default: // local value slot (always 8-byte, zero-extended)
			vt, ok := fl.types[o.Ident]
			if !ok {
				return fmt.Errorf("value %q has no type (isel bug)", o.Ident)
			}
			fl.emit(minst{op: "ldr", d: R(r), s: Slot(o.Ident), sz: 8})
			if signed {
				switch bitsOf(vt) {
				case 1:
					fl.emit(minst{op: "sxt1", d: R(r), s: R(r), sz: 4})
				case 8:
					fl.emit(minst{op: "sxtb", d: R(r), s: R(r), sz: 4})
				case 16:
					fl.emit(minst{op: "sxth", d: R(r), s: R(r), sz: 4})
				}
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on aarch64", o.Kind)
	}
	return nil
}

// st writes r (already normalized) into name's 8-byte home slot.
func (fl *fnLower) st(name string, r reg) {
	fl.emit(minst{op: "str", d: Slot(name), s: R(r), sz: 8})
}

// norm re-establishes the zero-extended-slot invariant after wrapping ops.
// W-width operations clear bits 63:32 in hardware, so only sub-32-bit
// widths need explicit masking.
func (fl *fnLower) norm(r reg, t vir.Type) {
	switch bitsOf(t) {
	case 1:
		fl.emit(minst{op: "and1", d: R(r), s: R(r)})
	case 8:
		fl.emit(minst{op: "uxtb", d: R(r), s: R(r)})
	case 16:
		fl.emit(minst{op: "uxth", d: R(r), s: R(r)})
	}
}

// normReg normalizes an incoming register whose high bits are unspecified
// (AAPCS64 arguments, call results): also clears bits 63:32 for i32.
func (fl *fnLower) normReg(r reg, t vir.Type) {
	switch bitsOf(t) {
	case 32:
		fl.emit(minst{op: "mov_r", d: R(r), s: R(r), sz: 4}) // W write clears 63:32
	default:
		fl.norm(r, t)
	}
}

// setcc materializes an i1 from the current flags into r (cset: 0/1, W).
func (fl *fnLower) setcc(cc byte, r reg) {
	fl.emit(minst{op: "cset", cc: cc, d: R(r)})
}

// trapIf emits a conditional deterministic halt: branch around a brk.
func (fl *fnLower) trapIf(cc byte) {
	ok := fl.label()
	fl.emit(minst{op: "bcc", cc: invert(cc), lbl: ok})
	fl.emit(minst{op: "brk"})
	fl.emit(minst{op: "label", lbl: ok})
}

// ---------------------------------------------------------------------------
// Instruction selection
// ---------------------------------------------------------------------------

func lastOrd(args []vir.Operand) string {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i].Kind == vir.OOrdering {
			return args[i].Ord
		}
	}
	return ""
}

func (fl *fnLower) selInst(in *vir.Inst) error {
	op, t, a := in.Op, in.Suffix, in.Args
	sz := szMachine(t)
	signedCmp := map[string]byte{"slt": ccLT, "sle": ccLE, "sgt": ccGT, "sge": ccGE}
	unsignedCmp := map[string]byte{"eq": ccEQ, "ne": ccNE, "ult": ccLO, "ule": ccLS, "ugt": ccHI, "uge": ccHS}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		fl.st(in.Result, x0)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, false); err != nil {
			return err
		}
		armOp := map[string]string{"add": "add", "sub": "sub", "and": "and", "or": "orr", "xor": "eor"}[op]
		fl.alu(armOp, sz, R(x0), R(x1))
		if op == "add" || op == "sub" {
			fl.norm(x0, t) // wrap mod 2^N (§4); 32/64 wrap in hardware
		}
		fl.st(in.Result, x0)

	case op == "mul":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, false); err != nil {
			return err
		}
		fl.emit(minst{op: "mul", sz: sz, d: R(x2), s: R(x0), t: R(x1)})
		fl.norm(x2, t)
		fl.st(in.Result, x2)

	case op == "neg":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "neg", sz: sz, d: R(x0), s: R(x0)})
		fl.norm(x0, t)
		fl.st(in.Result, x0)

	case op == "not":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "mvn", sz: sz, d: R(x0), s: R(x0)})
		fl.norm(x0, t)
		fl.st(in.Result, x0)

	case op == "abs": // signed; abs(INT_MIN) wraps (§4)
		if err := fl.load(a[0], t, x0, true); err != nil {
			return err
		}
		m := int64(8*sz - 1)
		fl.emit(minst{op: "asr_i", sz: sz, d: R(x1), s: R(x0), imm: m})
		fl.alu("eor", sz, R(x0), R(x1))
		fl.alu("sub", sz, R(x0), R(x1))
		fl.norm(x0, t)
		fl.st(in.Result, x0)

	case op == "udiv" || op == "urem" || op == "sdiv" || op == "srem":
		// A64 UDIV/SDIV never trap: zero divisor yields 0 and INT_MIN/-1
		// wraps, so §6.1's traps must be explicit here (same as lower/arm).
		signed := op[0] == 's'
		if err := fl.load(a[0], t, x0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, signed); err != nil {
			return err
		}
		fl.emit(minst{op: "cmp", sz: sz, d: R(x1), s: Imm(0)})
		fl.trapIf(ccEQ) // zero divisor traps (§6.1)
		div := "udiv"
		if signed {
			div = "sdiv"
			if bitsOf(t) == 32 || bitsOf(t) == 64 {
				// INT_MIN / -1 traps (§6.1).
				fl.emit(minst{op: "cmn", sz: sz, d: R(x1), s: Imm(1)})
				skip := fl.label()
				fl.emit(minst{op: "bcc", cc: ccNE, lbl: skip})
				min := int64(math.MinInt64)
				if bitsOf(t) == 32 {
					min = int64(uint32(math.MinInt32)) // low-32 pattern, upper zero
				}
				fl.emit(minst{op: "movimm", d: R(x2), imm: min})
				fl.emit(minst{op: "cmp", sz: sz, d: R(x0), s: R(x2)})
				fl.trapIf(ccEQ)
				fl.emit(minst{op: "label", lbl: skip})
			}
			// TODO(§6.1): narrow INT_MIN/-1 (e.g. i8 -128/-1) wraps via the
			// widened W-width sdiv instead of trapping (same gap as arm/x86).
		}
		fl.emit(minst{op: div, sz: sz, d: R(x2), s: R(x0), t: R(x1)})
		res := x2
		if op == "urem" || op == "srem" {
			// rem := a - (a/b)*b
			fl.emit(minst{op: "msub", sz: sz, d: R(x3), s: R(x2), t: R(x1), x: R(x0)})
			res = x3
		}
		fl.norm(res, t)
		fl.st(in.Result, res)

	case op == "shl" || op == "lshr" || op == "ashr":
		// A64 variable shifts mask the count mod the register width in
		// hardware, so §4's count-mod-N holds for free at 32/64 bits;
		// narrow widths mask explicitly.
		signedV := op == "ashr"
		if err := fl.load(a[0], t, x0, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, false); err != nil {
			return err
		}
		if b := bitsOf(t); b < 32 {
			fl.emit(minst{op: "movimm", d: R(x2), imm: int64(b - 1)})
			fl.alu("and", 4, R(x1), R(x2)) // count mod N (§4)
		}
		armOp := map[string]string{"shl": "lslv", "lshr": "lsrv", "ashr": "asrv"}[op]
		fl.emit(minst{op: armOp, sz: sz, d: R(x0), s: R(x0), t: R(x1)})
		fl.norm(x0, t)
		fl.st(in.Result, x0)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, false); err != nil {
			return err
		}
		b := bitsOf(t)
		if b == 32 || b == 64 {
			// Native rotate; rotl(x, c) = rotr(x, -c mod N), and rorv
			// masks the count mod N in hardware.
			if op == "rotl" {
				fl.emit(minst{op: "neg", sz: sz, d: R(x1), s: R(x1)})
			}
			fl.emit(minst{op: "rorv", sz: sz, d: R(x0), s: R(x0), t: R(x1)})
			fl.st(in.Result, x0)
			return nil
		}
		// Narrow: generic two-shift form at W width on the masked count;
		// the complementary shift degenerates correctly at c = 0 because a
		// W-register shift of a zero-extended narrow value by N (< 32)
		// yields 0 in the kept lanes after norm.
		fl.emit(minst{op: "movimm", d: R(x2), imm: int64(b - 1)})
		fl.alu("and", 4, R(x1), R(x2)) // count mod N (§4)
		fl.emit(minst{op: "movimm", d: R(x2), imm: int64(b)})
		fl.alu("sub", 4, R(x2), R(x1)) // N - c
		lo, hi := "lsrv", "lslv"       // rotr: (x >> c) | (x << (N-c))
		if op == "rotl" {
			lo, hi = "lslv", "lsrv"
		}
		fl.emit(minst{op: lo, sz: 4, d: R(x3), s: R(x0), t: R(x1)})
		fl.emit(minst{op: hi, sz: 4, d: R(x2), s: R(x0), t: R(x2)})
		fl.alu("orr", 4, R(x3), R(x2))
		fl.norm(x3, t)
		fl.st(in.Result, x3)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, x0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, signed); err != nil {
			return err
		}
		fl.emit(minst{op: "cmp", sz: sz, d: R(x0), s: R(x1)})
		cc := map[string]byte{"smin": ccGT, "smax": ccLT, "umin": ccHI, "umax": ccLO}[op]
		fl.emit(minst{op: "csel", cc: cc, sz: sz, d: R(x0), s: R(x1)})
		fl.norm(x0, t)
		fl.st(in.Result, x0)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, x0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, signed); err != nil {
			return err
		}
		fl.emit(minst{op: "cmp", sz: sz, d: R(x0), s: R(x1)})
		fl.setcc(cc, x2)
		fl.st(in.Result, x2)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on aarch64 (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, x0, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, signed); err != nil {
			return err
		}
		switch bitsOf(t) {
		case 64:
			m := "umulh"
			if signed {
				m = "smulh"
			}
			fl.emit(minst{op: m, d: R(x2), s: R(x0), t: R(x1)})
			fl.st(in.Result, x2)
		case 32: // full 64-bit product, take the high half
			m := "umull"
			if signed {
				m = "smull"
			}
			fl.emit(minst{op: m, d: R(x2), s: R(x0), t: R(x1)})
			fl.emit(minst{op: "lsr_i", sz: 8, d: R(x2), s: R(x2), imm: 32})
			fl.st(in.Result, x2)
		default: // narrow: product fits in 32 bits; shift the high half down
			fl.emit(minst{op: "mul", sz: 4, d: R(x2), s: R(x0), t: R(x1)})
			sh := "lsr_i"
			if signed {
				sh = "asr_i"
			}
			fl.emit(minst{op: sh, sz: 4, d: R(x2), s: R(x2), imm: int64(bitsOf(t))})
			fl.norm(x2, t)
			fl.st(in.Result, x2)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on aarch64 (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "clz", sz: sz, d: R(x0), s: R(x0)})
		if b := bitsOf(t); b < 32 { // leading zeros at width N = clz32 - (32-N)
			fl.alu("sub", 4, R(x0), Imm(int64(32-b)))
		}
		fl.st(in.Result, x0)

	case op == "cttz":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "rbit", sz: sz, d: R(x0), s: R(x0)})
		fl.emit(minst{op: "clz", sz: sz, d: R(x0), s: R(x0)})
		if b := bitsOf(t); b < 32 { // zero input gives 32; clamp to N
			fl.emit(minst{op: "movimm", d: R(x1), imm: int64(b)})
			fl.emit(minst{op: "cmp", sz: 4, d: R(x0), s: R(x1)})
			fl.emit(minst{op: "csel", cc: ccHI, sz: 4, d: R(x0), s: R(x1)})
		}
		fl.st(in.Result, x0)

	case op == "popcnt":
		return fmt.Errorf("popcnt has no baseline scalar A64 instruction (FEAT_CSSC CNT / NEON tier TODO, §10.4)")

	case op == "bitrev":
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "rbit", sz: sz, d: R(x0), s: R(x0)})
		if b := bitsOf(t); b < 32 {
			fl.emit(minst{op: "lsr_i", sz: 4, d: R(x0), s: R(x0), imm: int64(32 - b)})
		}
		fl.st(in.Result, x0)
	case op == "bswap": // i8 rejected by the verifier (§9.20)
		if err := fl.load(a[0], t, x0, false); err != nil {
			return err
		}
		switch bitsOf(t) {
		case 64:
			fl.emit(minst{op: "rev", sz: 8, d: R(x0), s: R(x0)})
		case 32:
			fl.emit(minst{op: "rev", sz: 4, d: R(x0), s: R(x0)})
		default: // i16: rev16 swaps bytes within each halfword; the high
			// halfword of the W register is zero and stays zero.
			fl.emit(minst{op: "rev16", sz: 4, d: R(x0), s: R(x0)})
		}
		fl.st(in.Result, x0)

	case op == "select":
		if err := fl.load(a[0], vir.I1, x0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, x2, false); err != nil {
			return err
		}
		fl.emit(minst{op: "cmp", sz: 4, d: R(x0), s: Imm(0)})
		fl.emit(minst{op: "csel", cc: ccEQ, sz: 8, d: R(x1), s: R(x2)}) // x1 := eq ? x2 : x1
		fl.st(in.Result, x1)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, x1, false); err != nil {
			return err
		}
		if op == "atomic_load" {
			// ldar covers acquire and (with A64's RCsc semantics) seqcst;
			// relaxed takes the plain load. No trailing dmb needed —
			// unlike lower/arm's dmb bracketing.
			switch lastOrd(a) {
			case "acquire", "seqcst":
				fl.emit(minst{op: "ldar", d: R(x0), s: Mem(x1, 0), sz: szOf(t)})
			default:
				fl.emit(minst{op: "ldr", d: R(x0), s: Mem(x1, 0), sz: szOf(t)})
			}
			fl.st(in.Result, x0)
			return nil
		}
		fl.emit(minst{op: "ldr", d: R(x0), s: Mem(x1, 0), sz: szOf(t)})
		fl.st(in.Result, x0)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, x1, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x0, false); err != nil {
			return err
		}
		if op == "atomic_store" {
			// stlr covers release and seqcst (RCsc); relaxed is plain.
			switch lastOrd(a) {
			case "release", "seqcst":
				fl.emit(minst{op: "stlr", d: Mem(x1, 0), s: R(x0), sz: szOf(t)})
			default:
				fl.emit(minst{op: "str", d: Mem(x1, 0), s: R(x0), sz: szOf(t)})
			}
			return nil
		}
		fl.emit(minst{op: "str", d: Mem(x1, 0), s: R(x0), sz: szOf(t)})

	case op == "alloca":
		if err := fl.load(a[0], vir.I64, x0, false); err != nil {
			return err
		}
		fl.alu("add", 8, R(x0), Imm(15)) // round size up, keep SP 16-aligned
		fl.alu("bic", 8, R(x0), Imm(15))
		fl.emit(minst{op: "sub_sp_r", s: R(x0)})
		if in.Align > 16 {
			fl.emit(minst{op: "and_sp", imm: int64(-in.Align)})
		}
		fl.emit(minst{op: "mov_r_sp", d: R(x0)})
		fl.st(in.Result, x0)

	case op == "field":
		off, err := fl.lay.fieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, x0, false); err != nil {
			return err
		}
		if off != 0 {
			fl.alu("add", 8, R(x0), Imm(int64(off)))
		}
		fl.st(in.Result, x0)

	case op == "index":
		esz, err := fl.lay.size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, x0, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, x1, true); err != nil { // index is signed (§4)
			return err
		}
		fl.emit(minst{op: "movimm", d: R(x2), imm: int64(esz)})
		fl.emit(minst{op: "mul", sz: 8, d: R(x3), s: R(x1), t: R(x2)})
		fl.alu("add", 8, R(x0), R(x3)) // address arithmetic wraps (§6.2)
		fl.st(in.Result, x0)

	case op == "memcopy" || op == "memset":
		// Byte-index loop over x15 (rIDX): dst x0, src/byte x1, len x2,
		// scratch x3. IP0/IP1 stay dead (encoder/callee contract).
		if err := fl.load(a[0], vir.Ptr, x0, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, x1, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, x1, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I64, x2, false); err != nil {
			return err
		}
		loop, done := fl.label(), fl.label()
		fl.emit(minst{op: "movimm", d: R(rIDX), imm: 0})
		fl.emit(minst{op: "label", lbl: loop})
		fl.emit(minst{op: "cmp", sz: 8, d: R(rIDX), s: R(x2)})
		fl.emit(minst{op: "bcc", cc: ccEQ, lbl: done})
		if op == "memcopy" {
			fl.emit(minst{op: "ldrb_r", d: R(x3), s: R(x1), t: R(rIDX)})
			fl.emit(minst{op: "strb_r", d: R(x0), s: R(x3), t: R(rIDX)})
		} else {
			fl.emit(minst{op: "strb_r", d: R(x0), s: R(x1), t: R(rIDX)})
		}
		fl.alu("add", 8, R(rIDX), Imm(1))
		fl.emit(minst{op: "b", lbl: loop})
		fl.emit(minst{op: "label", lbl: done})

	case op == "memmove":
		if err := fl.load(a[0], vir.Ptr, x0, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, x1, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, x2, false); err != nil {
			return err
		}
		back, bloop, floop, done := fl.label(), fl.label(), fl.label(), fl.label()
		fl.emit(minst{op: "cmp", sz: 8, d: R(x1), s: R(x0)})
		fl.emit(minst{op: "bcc", cc: ccLO, lbl: back}) // src < dst: copy backward
		fl.emit(minst{op: "movimm", d: R(rIDX), imm: 0})
		fl.emit(minst{op: "label", lbl: floop})
		fl.emit(minst{op: "cmp", sz: 8, d: R(rIDX), s: R(x2)})
		fl.emit(minst{op: "bcc", cc: ccEQ, lbl: done})
		fl.emit(minst{op: "ldrb_r", d: R(x3), s: R(x1), t: R(rIDX)})
		fl.emit(minst{op: "strb_r", d: R(x0), s: R(x3), t: R(rIDX)})
		fl.alu("add", 8, R(rIDX), Imm(1))
		fl.emit(minst{op: "b", lbl: floop})
		fl.emit(minst{op: "label", lbl: back}) // descending index
		fl.emit(minst{op: "mov_r", sz: 8, d: R(rIDX), s: R(x2)})
		fl.emit(minst{op: "label", lbl: bloop})
		fl.emit(minst{op: "cmp", sz: 8, d: R(rIDX), s: Imm(0)})
		fl.emit(minst{op: "bcc", cc: ccEQ, lbl: done})
		fl.alu("sub", 8, R(rIDX), Imm(1))
		fl.emit(minst{op: "ldrb_r", d: R(x3), s: R(x1), t: R(rIDX)})
		fl.emit(minst{op: "strb_r", d: R(x0), s: R(x3), t: R(rIDX)})
		fl.emit(minst{op: "b", lbl: bloop})
		fl.emit(minst{op: "label", lbl: done})

	case op == "prefetch":
		return nil // advisory (§4); dropped in this bring-up (PRFM TODO)

	case op == "fence":
		// Every §4 fence ordering needs a real barrier (A64 is weakly
		// ordered, like A32; unlike x86-TSO).
		fl.emit(minst{op: "dmb"})
		return nil

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_and" ||
		op == "atomic_or" || op == "atomic_xor" || op == "atomic_xchg":
		if szOf(t) < 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on aarch64 (TODO)", op)
		}
		// Baseline ldaxr/stlxr loop; the acquire/release pair over-delivers
		// for weaker orderings, which is correct if conservative. LSE
		// (ldadd/swp etc., ARMv8.1) is the §10.4 tier upgrade.
		if err := fl.load(a[0], vir.Ptr, x2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, false); err != nil {
			return err
		}
		retry := fl.label()
		fl.emit(minst{op: "label", lbl: retry})
		fl.emit(minst{op: "ldaxr", d: R(x0), s: Mem(x2, 0), sz: szOf(t)}) // old value
		switch op {
		case "atomic_xchg":
			fl.emit(minst{op: "mov_r", sz: 8, d: R(x3), s: R(x1)})
		case "atomic_add", "atomic_sub":
			fl.emit(minst{op: "mov_r", sz: 8, d: R(x3), s: R(x0)})
			armOp := "add"
			if op == "atomic_sub" {
				armOp = "sub"
			}
			fl.alu(armOp, sz, R(x3), R(x1))
		default:
			fl.emit(minst{op: "mov_r", sz: 8, d: R(x3), s: R(x0)})
			fl.alu(map[string]string{"atomic_and": "and", "atomic_or": "orr", "atomic_xor": "eor"}[op], sz, R(x3), R(x1))
		}
		fl.emit(minst{op: "stlxr", x: R(x4), s: R(x3), d: Mem(x2, 0), sz: szOf(t)})
		fl.emit(minst{op: "cbnz", sz: 4, s: R(x4), lbl: retry})
		fl.st(in.Result, x0) // old value (§4)

	case op == "cmpxchg":
		if szOf(t) < 4 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on aarch64 (TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, x2, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, x1, false); err != nil { // expected
			return err
		}
		if err := fl.load(a[2], t, x3, false); err != nil { // desired
			return err
		}
		retry, fail, done := fl.label(), fl.label(), fl.label()
		fl.emit(minst{op: "label", lbl: retry})
		fl.emit(minst{op: "ldaxr", d: R(x0), s: Mem(x2, 0), sz: szOf(t)})
		fl.emit(minst{op: "cmp", sz: sz, d: R(x0), s: R(x1)})
		fl.emit(minst{op: "bcc", cc: ccNE, lbl: fail})
		fl.emit(minst{op: "stlxr", x: R(x4), s: R(x3), d: Mem(x2, 0), sz: szOf(t)})
		fl.emit(minst{op: "cbnz", sz: 4, s: R(x4), lbl: retry})
		fl.emit(minst{op: "b", lbl: done})
		fl.emit(minst{op: "label", lbl: fail})
		fl.emit(minst{op: "clrex"})
		fl.emit(minst{op: "label", lbl: done})
		fl.st(in.Result, x0) // old value; caller compares with eq (§4)

	case op == "trunc":
		if err := fl.load(a[0], nil, x0, false); err != nil {
			return err
		}
		if bitsOf(t) == 32 {
			fl.emit(minst{op: "mov_r", sz: 4, d: R(x0), s: R(x0)}) // W write clears 63:32
		} else {
			fl.norm(x0, t)
		}
		fl.st(in.Result, x0)

	case op == "zext":
		if err := fl.load(a[0], nil, x0, false); err != nil {
			return err
		}
		fl.st(in.Result, x0) // slots are already zero-extended

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		dsz := szMachine(t)
		switch bitsOf(st) {
		case 1:
			fl.emit(minst{op: "ldr", d: R(x0), s: Slot(a[0].Ident), sz: 8})
			fl.emit(minst{op: "sxt1", sz: dsz, d: R(x0), s: R(x0)}) // 1 -> -1
		case 8:
			if err := fl.load(a[0], st, x0, false); err != nil {
				return err
			}
			fl.emit(minst{op: "sxtb", sz: dsz, d: R(x0), s: R(x0)})
		case 16:
			if err := fl.load(a[0], st, x0, false); err != nil {
				return err
			}
			fl.emit(minst{op: "sxth", sz: dsz, d: R(x0), s: R(x0)})
		case 32:
			if err := fl.load(a[0], st, x0, false); err != nil {
				return err
			}
			if dsz == 8 {
				fl.emit(minst{op: "sxtw", d: R(x0), s: R(x0)})
			}
		default: // i64 -> i64: identity
			if err := fl.load(a[0], st, x0, false); err != nil {
				return err
			}
		}
		fl.norm(x0, t)
		fl.st(in.Result, x0)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on aarch64 (TODO)")
		}
		if err := fl.load(a[0], st, x0, false); err != nil {
			return err
		}
		fl.st(in.Result, x0) // ptr <-> i64 register bits (§4, §9.19)

	case op == "call":
		return fl.selCall(in)

	case op == "asm":
		return fmt.Errorf("inline asm not lowered on aarch64 (reserved, §4)")

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

func (fl *fnLower) selOverflow(in *vir.Inst) error {
	t, a := in.Suffix, in.Args
	sz := szMachine(t)
	signed := in.Op[0] == 's'
	if err := fl.load(a[0], t, x0, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, x1, signed); err != nil {
		return err
	}
	b := bitsOf(t)
	if b == 32 || b == 64 { // hardware flags are exact at machine widths
		switch in.Op {
		case "uaddo":
			fl.emit(minst{op: "adds", sz: sz, d: R(x0), s: R(x1)})
			fl.setcc(ccHS, x2) // carry set = unsigned overflow
		case "usubo":
			fl.emit(minst{op: "subs", sz: sz, d: R(x0), s: R(x1)})
			fl.setcc(ccLO, x2) // A64 borrow = carry clear (same as A32)
		case "saddo":
			fl.emit(minst{op: "adds", sz: sz, d: R(x0), s: R(x1)})
			fl.setcc(ccVS, x2)
		case "ssubo":
			fl.emit(minst{op: "subs", sz: sz, d: R(x0), s: R(x1)})
			fl.setcc(ccVS, x2)
		case "umulo":
			if b == 64 { // high half nonzero <=> overflow
				fl.emit(minst{op: "umulh", d: R(x3), s: R(x0), t: R(x1)})
				fl.emit(minst{op: "cmp", sz: 8, d: R(x3), s: Imm(0)})
				fl.setcc(ccNE, x2)
			} else { // umull: full product, check bits 63:32
				fl.emit(minst{op: "umull", d: R(x3), s: R(x0), t: R(x1)})
				fl.emit(minst{op: "lsr_i", sz: 8, d: R(x3), s: R(x3), imm: 32})
				fl.emit(minst{op: "cmp", sz: 8, d: R(x3), s: Imm(0)})
				fl.setcc(ccNE, x2)
			}
		case "smulo":
			if b == 64 { // smulh ?= mul >> 63 (arithmetic)
				fl.emit(minst{op: "smulh", d: R(x3), s: R(x0), t: R(x1)})
				fl.emit(minst{op: "mul", sz: 8, d: R(x4), s: R(x0), t: R(x1)})
				fl.emit(minst{op: "asr_i", sz: 8, d: R(x4), s: R(x4), imm: 63})
				fl.emit(minst{op: "cmp", sz: 8, d: R(x3), s: R(x4)})
				fl.setcc(ccNE, x2)
			} else { // smull: overflow iff sext32(lo) != full product
				fl.emit(minst{op: "smull", d: R(x3), s: R(x0), t: R(x1)})
				fl.emit(minst{op: "sxtw", d: R(x4), s: R(x3)})
				fl.emit(minst{op: "cmp", sz: 8, d: R(x4), s: R(x3)})
				fl.setcc(ccNE, x2)
			}
		}
	} else {
		// Narrow widths: compute exactly at W width on extended operands,
		// then overflow iff re-extending the truncated result changes it.
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", 4, R(x0), R(x1))
		case "usubo", "ssubo":
			fl.alu("sub", 4, R(x0), R(x1))
		case "umulo", "smulo":
			fl.emit(minst{op: "mul", sz: 4, d: R(x2), s: R(x0), t: R(x1)})
			fl.emit(minst{op: "mov_r", sz: 8, d: R(x0), s: R(x2)})
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
		// Extend a masked copy and compare against the full W result.
		fl.emit(minst{op: "mov_r", sz: 8, d: R(x1), s: R(x0)})
		fl.norm(x1, t)
		fl.emit(minst{op: ext, sz: 4, d: R(x1), s: R(x1)})
		fl.emit(minst{op: "cmp", sz: 4, d: R(x1), s: R(x0)})
		fl.setcc(ccNE, x2)
	}
	fl.st(in.Result, x2)
	return nil
}

func (fl *fnLower) typeOfOperand(o vir.Operand) (vir.Type, error) {
	if o.Kind != vir.OIdent {
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

// selCall stages every argument in a stack area, then lifts the first
// eight into x0-x7 and releases the staging bytes that duplicated them,
// leaving any remaining arguments contiguous at SP for the call (AAPCS64:
// first stacked argument at SP, 8 bytes per slot). Caller cleans up.
// Variadic promotion is the frontend's job (§4); core-register rules are
// identical for variadics on linux/gnu (the aarch64-macos-aapcs64 variadic
// divergence is exactly why §10.3 carries the aapcs64 abi token — TODO).
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

	stage := int64((8*len(args) + 15) &^ 15) // staging area, SP kept 16-aligned
	if stage > 0 {
		fl.emit(minst{op: "sub_sp", imm: stage})
	}
	for i, a := range args {
		if err := fl.load(a, nil, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "str", d: Mem(rSP, int32(8*i)), s: R(x0), sz: 8})
	}
	if indirect { // callee ptr survives in IP1 across the register loads
		if err := fl.load(in.Args[0], vir.Ptr, rIP1, false); err != nil {
			return err
		}
	}
	nreg := len(args)
	if nreg > 8 {
		nreg = 8
	}
	for i := 0; i < nreg; i++ {
		fl.emit(minst{op: "ldr", d: R(reg(i)), s: Mem(rSP, int32(8*i)), sz: 8})
	}
	cleanup := stage
	if len(args) > 8 {
		fl.emit(minst{op: "add_sp", imm: 64}) // stack args now start at SP
		cleanup = stage - 64
	} else if stage > 0 {
		fl.emit(minst{op: "add_sp", imm: stage})
		cleanup = 0
	}
	if indirect {
		fl.emit(minst{op: "blr_r", s: R(rIP1)})
	} else {
		fl.emit(minst{op: "bl_sym", sym: in.Args[0].Ident})
	}
	if cleanup > 0 {
		fl.emit(minst{op: "add_sp", imm: cleanup}) // caller cleans up
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.normReg(x0, ret) // AAPCS64: high bits of narrow results unspecified
		fl.st(in.Result, x0)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Br:
		fl.emit(minst{op: "b", lbl: x.Label})
	case vir.BrIf:
		if err := fl.load(x.Cond, vir.I1, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "cbnz", sz: 4, s: R(x0), lbl: x.Then})
		fl.emit(minst{op: "b", lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I64
		}
		sz := szMachine(vt)
		if err := fl.load(x.Value, vt, x0, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.emit(minst{op: "movimm", d: R(x1), imm: litBits(c.Value, vt, false)})
			fl.emit(minst{op: "cmp", sz: sz, d: R(x0), s: R(x1)})
			fl.emit(minst{op: "bcc", cc: ccEQ, lbl: c.Label})
		}
		fl.emit(minst{op: "b", lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, x0, false); err != nil {
				return err
			}
		}
		fl.emit(minst{op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(minst{op: "brk"}) // canonical deterministic halt (§6.1)
	case vir.Unreachable:
		fl.emit(minst{op: "brk"}) // defensive; executing it is UB anyway (§6.3)
	default:
		return fmt.Errorf("terminator %T not lowered on aarch64", t)
	}
	return nil
}

// selTailCall implements guaranteed tail calls (§5) for the eligible shape
// this backend supports: at most eight arguments, all in registers, so the
// caller's stack argument area is never rewritten. Stack-argument tailcalls
// are the frame-rewriting TODO.
func (fl *fnLower) selTailCall(x vir.TailCall) error {
	args := x.Args
	indirect := x.Callee == ""
	if indirect {
		args = args[1:]
	}
	if len(args) > 8 {
		return fmt.Errorf("tailcall with %d args exceeds the x0-x7 register set (stack-arg tailcalls TODO)", len(args))
	}
	// Stage on the stack first: arguments may read values that x0-x7 will
	// hold, so evaluate everything before loading the argument registers.
	stage := int64((8*len(args) + 15) &^ 15)
	if stage > 0 {
		fl.emit(minst{op: "sub_sp", imm: stage})
	}
	for i, a := range args {
		if err := fl.load(a, nil, x0, false); err != nil {
			return err
		}
		fl.emit(minst{op: "str", d: Mem(rSP, int32(8*i)), s: R(x0), sz: 8})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, rIP1, false); err != nil {
			return err
		}
	}
	for i := range args {
		fl.emit(minst{op: "ldr", d: R(reg(i)), s: Mem(rSP, int32(8*i)), sz: 8})
	}
	if stage > 0 {
		fl.emit(minst{op: "add_sp", imm: stage})
	}
	if indirect {
		fl.emit(minst{op: "epi_jmp_r", s: R(rIP1)}) // IP1 survives the epilogue
	} else {
		fl.emit(minst{op: "epi_jmp_sym", sym: x.Callee})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Globals (static data + relocations). Scalars are serialized in the
// requested arch's byte order — the ONE place Arch.Big() matters in this
// package (arch.go); layout offsets are identical either way (§7.1).
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
	be  bool // big-endian scalar serialization (aarch64_be)
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
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: x.Name, Kind: FixupAbs64})
		w.scalar(0, 8) // 64-bit pointer field (usize is i64)
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
		w.scalar(0, 8)
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
		return fmt.Errorf("f16 initializers not yet emitted on aarch64 (TODO)")
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