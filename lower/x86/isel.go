package x86

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/lower/x86/abi"
	"github.com/vertex-language/vvm/lower/x86/inlineasm"
	"github.com/vertex-language/vvm/lower/x86/mcode"
	"github.com/vertex-language/vvm/lower/x86/regalloc"
	"github.com/vertex-language/vvm/lower/x86/syscallabi"
	"github.com/vertex-language/vvm/ir/vir"
)

// Local aliases keep the instruction-selection body close to a plain x86
// assembler's mental model without re-qualifying every operand
// constructor as mcode.X.
type (
	Reg  = mcode.Reg
	opr  = mcode.Opr
	minst = mcode.Inst
)

var (
	R       = mcode.R
	Imm     = mcode.Imm
	SymAddr = mcode.SymAddr
	Mem     = mcode.Mem
	Slot    = mcode.Slot
)

const (
	oNone = mcode.ONone
	oReg  = mcode.OReg
	oImm  = mcode.OImm
	oMem  = mcode.OMem
	oSlot = mcode.OSlot
)

const (
	rEAX = mcode.REAX
	rECX = mcode.RECX
	rEDX = mcode.REDX
	rEBX = mcode.REBX
	rESP = mcode.RESP
	rEBP = mcode.REBP
	rESI = mcode.RESI
	rEDI = mcode.REDI
)

const (
	ccO  = mcode.CondO
	ccNO = mcode.CondNO
	ccB  = mcode.CondB
	ccAE = mcode.CondAE
	ccE  = mcode.CondE
	ccNE = mcode.CondNE
	ccBE = mcode.CondBE
	ccA  = mcode.CondA
	ccS  = mcode.CondS
	ccNS = mcode.CondNS
	ccL  = mcode.CondL
	ccGE = mcode.CondGE
	ccLE = mcode.CondLE
	ccG  = mcode.CondG
)

// Lower converts a verified module into an x86 Program. The module must
// have passed vir.Verify; Lower assumes the §9 obligations already hold.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target != nil && m.Target.Arch != "x86" {
		return nil, fmt.Errorf("lower/x86: module targets arch %q, not x86", m.Target.Arch)
	}
	lw := &lowerer{
		m: m, lay: abi.NewLayout(m),
		kinds:  map[string]string{},
		consts: map[string]*vir.Constant{},
	}
	for _, s := range m.Structs {
		lw.kinds[s.Name] = "struct"
	}
	for _, s := range m.FunctionSignatures {
		lw.kinds[s.Name] = "fnsig"
	}
	for _, c := range m.Constants {
		lw.kinds[c.Name] = "const"
		lw.consts[c.Name] = c
	}
	for _, g := range m.Globals {
		lw.kinds[g.Name] = "global"
	}
	for _, g := range m.Externs {
		for _, f := range g.Functions {
			lw.kinds[f.Name] = "extern"
		}
	}
	for _, f := range m.Functions {
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
	for _, f := range m.Functions {
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
	lay    *abi.Layout
	kinds  map[string]string
	consts map[string]*vir.Constant
}

func (lw *lowerer) lookupCallable(name string) (ret vir.Type, params []vir.Param, ok bool) {
	for _, g := range lw.m.Externs {
		for _, e := range g.Functions {
			if e.Name == name {
				return e.Ret, e.Params, true
			}
		}
	}
	for _, f := range lw.m.Functions {
		if f.Name == name {
			return f.Ret, f.Params, true
		}
	}
	return nil, nil, false
}

// ---------------------------------------------------------------------------
// Function lowering
// ---------------------------------------------------------------------------

func (lw *lowerer) lowerFunc(f *vir.Function) (Func, error) {
	fl := &fnLower{lowerer: lw, f: f}
	var err error
	if fl.types, err = fl.typeFunc(); err != nil {
		return Func{}, err
	}
	for bi, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(minst{Op: "label", Lbl: b.Label})
		}
		for li := range b.Lines {
			ln := &b.Lines[li]
			if ln.Asm != nil {
				if err := fl.selAsm(bi, li, ln.Asm); err != nil {
					return Func{}, fmt.Errorf("block %s: asm: %w", labelName(b), err)
				}
				continue
			}
			if err := fl.selInst(ln.Instruction); err != nil {
				return Func{}, fmt.Errorf("block %s: %s: %w", labelName(b), ln.Instruction.Op, err)
			}
		}
		if err := fl.selTerm(b.Term); err != nil {
			return Func{}, fmt.Errorf("block %s: terminator: %w", labelName(b), err)
		}
	}
	fr := abi.BuildFrame(f, fl.b)
	if err := regalloc.ResolveSlots(fl.b, fr); err != nil {
		return Func{}, err
	}
	code, fixups, err := mcode.Encode(fl.b, fr.Local)
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
	f     *vir.Function
	types map[string]vir.Type
	b     []minst
	nlbl  int
}

func (fl *fnLower) emit(i minst)             { fl.b = append(fl.b, i) }
func (fl *fnLower) mov(d, s opr)             { fl.emit(minst{Op: "mov", D: d, S: s, Sz: 4}) }
func (fl *fnLower) alu(op string, d, s opr)  { fl.emit(minst{Op: op, D: d, S: s}) }
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}

// typeFunc mirrors the verifier's result-type/type-fixation computation for
// the subset this backend supports (input is verified, so lookups cannot
// fail semantically), including asm `out` bindings, which follow the same
// Join Convention as ordinary instructions (§4, §5 rule 2).
func (fl *fnLower) typeFunc() (map[string]vir.Type, error) {
	types := map[string]vir.Type{}
	for _, p := range fl.f.Params {
		if err := fl.checkValueType(p.Type); err != nil {
			return nil, err
		}
		types[p.Name] = p.Type
	}
	regs := vir.RegisterTableForArchitecture("x86")
	for _, b := range fl.f.AllBlocks() {
		for i := range b.Lines {
			ln := &b.Lines[i]
			if ln.Asm != nil {
				for _, bind := range ln.Asm.Bindings {
					if bind.Kind != vir.BindingOut {
						continue
					}
					if _, done := types[bind.Ident]; done {
						continue
					}
					info, ok := regs[bind.Register]
					if !ok {
						return nil, fmt.Errorf("asm: register %q not in the x86 register table", bind.Register)
					}
					types[bind.Ident] = vir.IntType{Bits: info.WidthBits}
				}
				continue
			}
			in := ln.Instruction
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
			return fmt.Errorf("i%d values not yet lowered on x86 (register pairs TODO)", x.Bits)
		}
		return nil
	case vir.PtrType:
		return nil
	case vir.FloatType:
		return fmt.Errorf("floating-point lowering not implemented on x86 (x87/SSE tier TODO)")
	case vir.VecType:
		return fmt.Errorf("vector lowering not implemented on x86 (tier TODO, §10.4)")
	}
	return fmt.Errorf("type %s cannot be a named value on x86", t)
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
		if in.Suffix == nil {
			return nil, fmt.Errorf("syscall: missing return type suffix")
		}
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

// load materializes operand o (of type t) into r as a 32-bit value. Values
// narrower than 32 bits live zero-extended; signed=true requests a
// sign-extended materialization instead.
func (fl *fnLower) load(o vir.Operand, t vir.Type, r Reg, signed bool) error {
	switch o.Kind {
	case vir.OperandInt:
		fl.mov(R(r), Imm(litBits(o.Int, t, signed)))
	case vir.OperandBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		fl.mov(R(r), Imm(v))
	case vir.OperandNull:
		fl.mov(R(r), Imm(0))
	case vir.OperandFloat:
		return fmt.Errorf("float operands not lowered on x86 (TODO)")
	case vir.OperandIdent:
		switch fl.kinds[o.Ident] {
		case "const":
			c := fl.consts[o.Ident]
			return fl.load(c.Value, c.Type, r, signed)
		case "global", "fn", "extern":
			fl.mov(R(r), SymAddr(o.Ident))
		case "struct", "fnsig":
			return fmt.Errorf("entity %q used as runtime operand", o.Ident)
		default:
			vt, ok := fl.types[o.Ident]
			if !ok {
				return fmt.Errorf("value %q has no type (isel bug)", o.Ident)
			}
			sz := szOf(vt)
			switch {
			case sz == 4:
				fl.emit(minst{Op: "mov", D: R(r), S: Slot(o.Ident), Sz: 4})
			case signed:
				fl.emit(minst{Op: "movsx", D: R(r), S: Slot(o.Ident), Sz: sz})
			default:
				fl.emit(minst{Op: "movzx", D: R(r), S: Slot(o.Ident), Sz: sz})
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on x86", o.Kind)
	}
	return nil
}

func (fl *fnLower) st(name string, r Reg) {
	fl.emit(minst{Op: "mov", D: Slot(name), S: R(r), Sz: 4})
}

func (fl *fnLower) norm(r Reg, t vir.Type) {
	if it, ok := t.(vir.IntType); ok && it.Bits == 1 {
		fl.alu("and", R(r), Imm(1))
		return
	}
	switch szOf(t) {
	case 1, 2:
		fl.emit(minst{Op: "movzx", D: R(r), S: R(r), Sz: szOf(t)})
	}
}

// Resolve implements inlineasm.SymbolResolver: an ident used as a raw
// asm-line operand resolves exactly like an ordinary instruction operand
// would (§4 Addresses).
func (fl *fnLower) Resolve(name string) (opr, error) {
	switch fl.kinds[name] {
	case "const":
		c := fl.consts[name]
		switch c.Value.Kind {
		case vir.OperandInt:
			return Imm(litBits(c.Value.Int, c.Type, false)), nil
		case vir.OperandBool:
			if c.Value.Bool {
				return Imm(1), nil
			}
			return Imm(0), nil
		case vir.OperandNull:
			return Imm(0), nil
		}
		return opr{}, fmt.Errorf("asm: const %q's value kind is not usable as a raw operand", name)
	case "global", "fn", "extern":
		return SymAddr(name), nil
	case "struct", "fnsig":
		return opr{}, fmt.Errorf("asm: %q names a compile-time entity, not a value", name)
	default:
		return Slot(name), nil
	}
}

// ---------------------------------------------------------------------------
// Inline assembly
// ---------------------------------------------------------------------------

func (fl *fnLower) selAsm(blockIdx, lineIdx int, a *vir.AsmBlock) error {
	if fl.m.AsmDialect == nil || fl.m.Target == nil {
		return fmt.Errorf("asm block present but module has no asmdialect/target (should have been rejected by Verify, §1.2 rule 11)")
	}
	uniq := fmt.Sprintf(".asm%d_%d.%s", blockIdx, lineIdx, fl.f.Name)
	label := func(name string) string { return uniq + "." + name }
	insts, err := inlineasm.LowerBlock(*fl.m.AsmDialect, fl.m.Target.Arch, a, fl, label)
	if err != nil {
		return err
	}
	fl.b = append(fl.b, insts...)
	return nil
}

// ---------------------------------------------------------------------------
// Syscalls
// ---------------------------------------------------------------------------

func (fl *fnLower) selSyscall(in *vir.Instruction) error {
	if fl.m.Target == nil {
		return fmt.Errorf("syscall: module has no target declaration")
	}
	osName := fl.m.Target.OS
	conv, ok := syscallabi.Lookup(osName)
	if !ok {
		if osName == "none" || osName == "uefi" {
			// §4: "executes a runtime trap if unsupported" absent an
			// explicitly enabled feature-tier flag providing a convention.
			fl.emit(minst{Op: "ud2"})
			return nil
		}
		return fmt.Errorf("syscall: no lowering convention for target os %q", osName)
	}

	args := in.Args // args[0] = sysno, args[1:] = up to six arguments
	var stackVals []vir.Operand
	regVals := map[int]vir.Operand{}
	for i, a := range args {
		if _, ok := conv.RegisterFor(i); ok {
			regVals[i] = a
		} else {
			stackVals = append(stackVals, a)
		}
	}

	// Push stack-passed arguments first, before any register (including
	// sysno) is loaded, so no scratch register we use here can clobber a
	// value about to be materialized into a syscall register.
	if len(stackVals) > 0 {
		if conv.StackArgsPushRetAddrPlaceholder {
			fl.emit(minst{Op: "push", S: Imm(0)})
		}
		for i := len(stackVals) - 1; i >= 0; i-- {
			if err := fl.load(stackVals[i], nil, rECX, false); err != nil {
				return err
			}
			fl.emit(minst{Op: "push", S: R(rECX)})
		}
	}
	for i := 0; i < len(args); i++ {
		if r, ok := conv.RegisterFor(i); ok {
			if err := fl.load(regVals[i], nil, r, false); err != nil {
				return err
			}
		}
	}
	fl.emit(conv.Trap)
	if len(stackVals) > 0 {
		cleanup := int64(4 * len(stackVals))
		if conv.StackArgsPushRetAddrPlaceholder {
			cleanup += 4
		}
		fl.alu("add", R(rESP), Imm(cleanup))
	}
	if in.Result != "" {
		fl.norm(conv.Result, in.Suffix)
		fl.st(in.Result, conv.Result)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Instruction selection
// ---------------------------------------------------------------------------

func (fl *fnLower) selInst(in *vir.Instruction) error {
	op, t, a := in.Op, in.Suffix, in.Args
	signedCmp := map[string]byte{"slt": ccL, "sle": ccLE, "sgt": ccG, "sge": ccGE}
	unsignedCmp := map[string]byte{"eq": ccE, "ne": ccNE, "ult": ccB, "ule": ccBE, "ugt": ccA, "uge": ccAE}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, rEAX, false); err != nil {
			return err
		}
		fl.st(in.Result, rEAX)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, rEAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, false); err != nil {
			return err
		}
		fl.alu(op, R(rEAX), R(rECX))
		if op == "add" || op == "sub" {
			fl.norm(rEAX, t)
		}
		fl.st(in.Result, rEAX)

	case op == "mul":
		if err := fl.load(a[0], t, rEAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "imul", D: R(rEAX), S: R(rECX)})
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case op == "neg" || op == "not":
		if err := fl.load(a[0], t, rEAX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: op, S: R(rEAX)})
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case op == "abs":
		if err := fl.load(a[0], t, rEAX, true); err != nil {
			return err
		}
		fl.mov(R(rECX), R(rEAX))
		fl.emit(minst{Op: "sar", D: R(rECX), S: Imm(31), Sz: 4})
		fl.alu("xor", R(rEAX), R(rECX))
		fl.alu("sub", R(rEAX), R(rECX))
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case op == "udiv" || op == "urem":
		if err := fl.load(a[0], t, rEAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, false); err != nil {
			return err
		}
		fl.alu("xor", R(rEDX), R(rEDX))
		fl.emit(minst{Op: "div", S: R(rECX)})
		r := rEAX
		if op == "urem" {
			r = rEDX
		}
		fl.st(in.Result, r)

	case op == "sdiv" || op == "srem":
		// TODO(§6.1): narrow INT_MIN/-1 (e.g. i8 -128/-1) must trap but the
		// widened 32-bit idiv wraps instead; needs an explicit check for sz<4.
		if err := fl.load(a[0], t, rEAX, true); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, true); err != nil {
			return err
		}
		fl.emit(minst{Op: "cdq"})
		fl.emit(minst{Op: "idiv", S: R(rECX)})
		r := rEAX
		if op == "srem" {
			r = rEDX
		}
		fl.norm(r, t)
		fl.st(in.Result, r)

	case op == "shl" || op == "lshr" || op == "ashr":
		signedV := op == "ashr"
		if err := fl.load(a[0], t, rEAX, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, false); err != nil {
			return err
		}
		if bitsOf(t) < 32 {
			fl.alu("and", R(rECX), Imm(int64(bitsOf(t)-1)))
		}
		x86op := map[string]string{"shl": "shl", "lshr": "shr", "ashr": "sar"}[op]
		fl.emit(minst{Op: x86op, D: R(rEAX), Sz: 4})
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, rEAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, false); err != nil {
			return err
		}
		x86op := "rol"
		if op == "rotr" {
			x86op = "ror"
		}
		fl.emit(minst{Op: x86op, D: R(rEAX), Sz: szOf(t)})
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, rEAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(rEAX), R(rECX))
		cc := map[string]byte{"smin": ccG, "smax": ccL, "umin": ccA, "umax": ccB}[op]
		fl.emit(minst{Op: "cmovcc", CC: cc, D: R(rEAX), S: R(rECX)})
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, rEAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(rEAX), R(rECX))
		fl.emit(minst{Op: "setcc", CC: cc, D: R(rEAX)})
		fl.emit(minst{Op: "movzx", D: R(rEAX), S: R(rEAX), Sz: 1})
		fl.st(in.Result, rEAX)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on x86 (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, rEAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, signed); err != nil {
			return err
		}
		if szOf(t) == 4 {
			m := "mul32"
			if signed {
				m = "imul32"
			}
			fl.emit(minst{Op: m, S: R(rECX)})
			fl.st(in.Result, rEDX)
		} else {
			fl.emit(minst{Op: "imul", D: R(rEAX), S: R(rECX)})
			sh := "shr"
			if signed {
				sh = "sar"
			}
			fl.emit(minst{Op: sh, D: R(rEAX), S: Imm(int64(bitsOf(t))), Sz: 4})
			fl.norm(rEAX, t)
			fl.st(in.Result, rEAX)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on x86 (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, rECX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "bsr", D: R(rEDX), S: R(rECX)})
		fl.mov(R(rEAX), Imm(-1))
		fl.emit(minst{Op: "cmovcc", CC: ccNE, D: R(rEAX), S: R(rEDX)})
		fl.mov(R(rECX), Imm(int64(bitsOf(t)-1)))
		fl.alu("sub", R(rECX), R(rEAX))
		fl.st(in.Result, rECX)

	case op == "cttz":
		if err := fl.load(a[0], t, rECX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "bsf", D: R(rEDX), S: R(rECX)})
		fl.mov(R(rEAX), Imm(int64(bitsOf(t))))
		fl.emit(minst{Op: "cmovcc", CC: ccNE, D: R(rEAX), S: R(rEDX)})
		fl.st(in.Result, rEAX)

	case op == "popcnt":
		if err := fl.load(a[0], t, rECX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "popcnt", D: R(rEAX), S: R(rECX)})
		fl.st(in.Result, rEAX)

	case op == "bswap":
		if err := fl.load(a[0], t, rEAX, false); err != nil {
			return err
		}
		if szOf(t) == 4 {
			fl.emit(minst{Op: "bswap", D: R(rEAX)})
		} else {
			fl.emit(minst{Op: "ror", D: R(rEAX), S: Imm(8), Sz: 2})
			fl.norm(rEAX, t)
		}
		fl.st(in.Result, rEAX)

	case op == "bitrev":
		return fmt.Errorf("bitrev not yet lowered on x86 (SWAR sequence TODO)")

	case op == "select":
		if err := fl.load(a[0], vir.I1, rEAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rECX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, rEDX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "test", D: R(rEAX), S: R(rEAX)})
		fl.emit(minst{Op: "cmovcc", CC: ccE, D: R(rECX), S: R(rEDX)})
		fl.st(in.Result, rECX)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, rECX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 4:
			fl.emit(minst{Op: "mov", D: R(rEAX), S: Mem(rECX, 0), Sz: 4})
		default:
			fl.emit(minst{Op: "movzx", D: R(rEAX), S: Mem(rECX, 0), Sz: szOf(t)})
		}
		fl.st(in.Result, rEAX)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, rECX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rEAX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "mov", D: Mem(rECX, 0), S: R(rEAX), Sz: szOf(t)})
		if op == "atomic_store" && lastOrd(a) == "seqcst" {
			fl.emit(minst{Op: "mfence"})
		}

	case op == "alloca":
		if err := fl.load(a[0], vir.I32, rEAX, false); err != nil {
			return err
		}
		fl.alu("add", R(rEAX), Imm(3))
		fl.alu("and", R(rEAX), Imm(-4))
		fl.alu("sub", R(rESP), R(rEAX))
		if in.Align > 4 {
			fl.alu("and", R(rESP), Imm(int64(-in.Align)))
		}
		fl.st(in.Result, rESP)

	case op == "field":
		off, err := fl.lay.FieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, rEAX, false); err != nil {
			return err
		}
		if off != 0 {
			fl.alu("add", R(rEAX), Imm(int64(off)))
		}
		fl.st(in.Result, rEAX)

	case op == "index":
		esz, err := fl.lay.Size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, rEAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, rECX, true); err != nil {
			return err
		}
		fl.emit(minst{Op: "imul3", D: R(rECX), S: R(rECX), Imm: int64(esz)})
		fl.alu("add", R(rEAX), R(rECX))
		fl.st(in.Result, rEAX)

	case op == "memcopy" || op == "memset":
		if err := fl.load(a[0], vir.Ptr, rEDI, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, rESI, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, rEAX, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I32, rECX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "cld"})
		if op == "memcopy" {
			fl.emit(minst{Op: "rep_movsb"})
		} else {
			fl.emit(minst{Op: "rep_stosb"})
		}

	case op == "memmove":
		fwd, done := fl.label(), fl.label()
		if err := fl.load(a[0], vir.Ptr, rEDI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, rESI, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, rECX, false); err != nil {
			return err
		}
		fl.alu("cmp", R(rESI), R(rEDI))
		fl.emit(minst{Op: "jcc", CC: ccAE, Lbl: fwd})
		fl.alu("add", R(rESI), R(rECX))
		fl.alu("sub", R(rESI), Imm(1))
		fl.alu("add", R(rEDI), R(rECX))
		fl.alu("sub", R(rEDI), Imm(1))
		fl.emit(minst{Op: "std"})
		fl.emit(minst{Op: "rep_movsb"})
		fl.emit(minst{Op: "cld"})
		fl.emit(minst{Op: "jmp", Lbl: done})
		fl.emit(minst{Op: "label", Lbl: fwd})
		fl.emit(minst{Op: "cld"})
		fl.emit(minst{Op: "rep_movsb"})
		fl.emit(minst{Op: "label", Lbl: done})

	case op == "prefetch":
		return nil

	case op == "fence":
		if lastOrd(a) == "seqcst" {
			fl.emit(minst{Op: "mfence"})
		}
		return nil

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_xchg":
		if szOf(t) != 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, rECX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rEAX, false); err != nil {
			return err
		}
		switch op {
		case "atomic_sub":
			fl.emit(minst{Op: "neg", S: R(rEAX)})
			fallthrough
		case "atomic_add":
			fl.emit(minst{Op: "lock_xadd", D: Mem(rECX, 0), S: R(rEAX)})
		case "atomic_xchg":
			fl.emit(minst{Op: "xchg", D: Mem(rECX, 0), S: R(rEAX)})
		}
		fl.st(in.Result, rEAX)

	case op == "atomic_and" || op == "atomic_or" || op == "atomic_xor":
		if szOf(t) != 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, rESI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rEDX, false); err != nil {
			return err
		}
		loop := fl.label()
		fl.emit(minst{Op: "mov", D: R(rEAX), S: Mem(rESI, 0), Sz: 4})
		fl.emit(minst{Op: "label", Lbl: loop})
		fl.mov(R(rECX), R(rEAX))
		fl.alu(op[len("atomic_"):], R(rECX), R(rEDX))
		fl.emit(minst{Op: "lock_cmpxchg", D: Mem(rESI, 0), S: R(rECX)})
		fl.emit(minst{Op: "jcc", CC: ccNE, Lbl: loop})
		fl.st(in.Result, rEAX)

	case op == "cmpxchg":
		if szOf(t) != 4 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on x86 (TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, rECX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, rEAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, rEDX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "lock_cmpxchg", D: Mem(rECX, 0), S: R(rEDX)})
		fl.st(in.Result, rEAX)

	case op == "trunc":
		if err := fl.load(a[0], nil, rEAX, false); err != nil {
			return err
		}
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case op == "zext":
		if err := fl.load(a[0], nil, rEAX, false); err != nil {
			return err
		}
		fl.st(in.Result, rEAX)

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if it, ok := st.(vir.IntType); ok && it.Bits == 1 {
			if err := fl.load(a[0], st, rEAX, false); err != nil {
				return err
			}
			fl.emit(minst{Op: "neg", S: R(rEAX)})
		} else {
			if err := fl.load(a[0], st, rEAX, true); err != nil {
				return err
			}
		}
		fl.norm(rEAX, t)
		fl.st(in.Result, rEAX)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on x86 (TODO)")
		}
		if err := fl.load(a[0], st, rEAX, false); err != nil {
			return err
		}
		fl.st(in.Result, rEAX)

	case op == "call":
		return fl.selCall(in)

	case op == "syscall":
		return fl.selSyscall(in)

	case op == "fdemote" || op == "fpromote" || op == "sfromint" || op == "ufromint" ||
		op == "stoint" || op == "utoint" || op == "stoint_sat" || op == "utoint_sat" ||
		op == "sqrt" || op == "fma" || op == "copysign" || op == "floor" || op == "ceil" ||
		op == "trunc_f" || op == "nearest" || op == "min" || op == "max":
		return fmt.Errorf("floating-point op %q not lowered on x86 (x87/SSE tier TODO)", op)

	case op == "splat" || op == "extract" || op == "insert" || op == "shuffle" ||
		op == "masked_load" || op == "masked_store" || op == "gather" || op == "scatter" ||
		op == "reduce_add" || op == "reduce_min" || op == "reduce_max" ||
		op == "reduce_and" || op == "reduce_or" || op == "reduce_xor":
		return fmt.Errorf("vector op %q not lowered on x86 (tier TODO, §10.4)", op)

	default:
		return fmt.Errorf("op %q not lowered on x86", op)
	}
	return nil
}

func (fl *fnLower) selOverflow(in *vir.Instruction) error {
	t, a := in.Suffix, in.Args
	signed := in.Op[0] == 's'
	if err := fl.load(a[0], t, rEAX, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, rECX, signed); err != nil {
		return err
	}
	if szOf(t) == 4 {
		var cc byte
		switch in.Op {
		case "uaddo", "usubo":
			cc = ccB
		case "saddo", "ssubo", "smulo":
			cc = ccO
		case "umulo":
			cc = ccO
		}
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(rEAX), R(rECX))
		case "usubo", "ssubo":
			fl.alu("sub", R(rEAX), R(rECX))
		case "umulo":
			fl.emit(minst{Op: "mul32", S: R(rECX)})
		case "smulo":
			fl.emit(minst{Op: "imul32", S: R(rECX)})
		}
		fl.emit(minst{Op: "setcc", CC: cc, D: R(rEAX)})
	} else {
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(rEAX), R(rECX))
		case "usubo", "ssubo":
			fl.alu("sub", R(rEAX), R(rECX))
		case "umulo", "smulo":
			fl.emit(minst{Op: "imul", D: R(rEAX), S: R(rECX)})
		}
		ext := "movzx"
		if signed {
			ext = "movsx"
		}
		fl.emit(minst{Op: ext, D: R(rECX), S: R(rEAX), Sz: szOf(t)})
		fl.alu("cmp", R(rECX), R(rEAX))
		fl.emit(minst{Op: "setcc", CC: ccNE, D: R(rEAX)})
	}
	fl.emit(minst{Op: "movzx", D: R(rEAX), S: R(rEAX), Sz: 1})
	fl.st(in.Result, rEAX)
	return nil
}

func (fl *fnLower) typeOfOperand(o vir.Operand) (vir.Type, error) {
	if o.Kind != vir.OperandIdent {
		return nil, fmt.Errorf("conversion source must be a named value or const on x86")
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
// Calls (cdecl) and terminators
// ---------------------------------------------------------------------------

func (fl *fnLower) selCall(in *vir.Instruction) error {
	args := in.Args
	var params []vir.Param
	var ret vir.Type
	indirect := in.Sig != ""
	if indirect {
		for _, s := range fl.m.FunctionSignatures {
			if s.Name == in.Sig {
				ret = s.Ret
				for _, pt := range s.Params {
					params = append(params, vir.Param{Type: pt})
				}
			}
		}
		args = args[1:]
	} else {
		ret, params, _ = fl.lookupCallable(args[0].Ident)
		args = args[1:]
	}
	if !vir.IsVoid(ret) {
		if err := fl.checkValueType(ret); err != nil {
			return err
		}
	}

	slots, total, err := abi.PlanCall(params, len(args), func(name string) (int, error) {
		sz, _, _, err := fl.lay.StructLayout(name)
		return sz, err
	})
	if err != nil {
		return err
	}
	if total > 0 {
		fl.alu("sub", R(rESP), Imm(int64(total)))
	}
	for i, a := range args {
		if slots[i].ByVal != "" {
			sz, _, _, err := fl.lay.StructLayout(slots[i].ByVal)
			if err != nil {
				return err
			}
			if err := fl.load(a, vir.Ptr, rESI, false); err != nil {
				return err
			}
			fl.emit(minst{Op: "lea", D: R(rEDI), S: Mem(rESP, int32(slots[i].Offset))})
			fl.mov(R(rECX), Imm(int64(sz)))
			fl.emit(minst{Op: "cld"})
			fl.emit(minst{Op: "rep_movsb"})
			continue
		}
		if err := fl.load(a, nil, rEAX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "mov", D: Mem(rESP, int32(slots[i].Offset)), S: R(rEAX), Sz: 4})
	}
	if indirect {
		if err := fl.load(in.Args[0], vir.Ptr, rEAX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "call_r", S: R(rEAX)})
	} else {
		fl.emit(minst{Op: "call_sym", Sym: in.Args[0].Ident})
	}
	if total > 0 {
		fl.alu("add", R(rESP), Imm(int64(total)))
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.norm(rEAX, ret)
		fl.st(in.Result, rEAX)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		fl.emit(minst{Op: "jmp", Lbl: x.Label})
	case vir.BranchIf:
		if err := fl.load(x.Cond, vir.I1, rEAX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "test", D: R(rEAX), S: R(rEAX)})
		fl.emit(minst{Op: "jcc", CC: ccNE, Lbl: x.Then})
		fl.emit(minst{Op: "jmp", Lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I32
		}
		if err := fl.load(x.Value, vt, rEAX, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.alu("cmp", R(rEAX), Imm(litBits(c.Value, vt, false)))
			fl.emit(minst{Op: "jcc", CC: ccE, Lbl: c.Label})
		}
		fl.emit(minst{Op: "jmp", Lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, rEAX, false); err != nil {
				return err
			}
		}
		fl.emit(minst{Op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(minst{Op: "ud2"})
	case vir.Unreachable:
		fl.emit(minst{Op: "ud2"})
	default:
		return fmt.Errorf("terminator %T not lowered on x86", t)
	}
	return nil
}

// selTailCall implements guaranteed tail calls (§5) for the eligible shape
// this backend supports: the callee's argument bytes fit inside the
// caller's own incoming argument area (which cdecl lets the callee
// overwrite).
func (fl *fnLower) selTailCall(x vir.TailCall) error {
	args := x.Args
	indirect := x.Callee == ""
	if indirect {
		args = args[1:]
	}
	need := 4 * len(args)
	have := 4 * len(fl.f.Params)
	if need > have {
		return fmt.Errorf("tailcall with %d arg bytes exceeds caller's %d incoming bytes (frame-growing tailcalls TODO)", need, have)
	}
	for _, a := range args {
		if err := fl.load(a, nil, rEAX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "push", S: R(rEAX)})
	}
	for i := len(args) - 1; i >= 0; i-- {
		fl.emit(minst{Op: "pop", D: R(rEAX)})
		fl.emit(minst{Op: "mov", D: Mem(rEBP, int32(8+4*i)), S: R(rEAX), Sz: 4})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, rEAX, false); err != nil {
			return err
		}
		fl.emit(minst{Op: "epi_jmp_r", S: R(rEAX)})
	} else {
		fl.emit(minst{Op: "epi_jmp_sym", Sym: x.Callee})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Globals (static data + relocations)
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
	lay *abi.Layout
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
		w.le(0, 4)
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
			s, _ := w.lay.Struct(tt.Name)
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
		w.le(uint64(o.Int), sz)
		return nil
	case vir.OperandBool:
		v := uint64(0)
		if o.Bool {
			v = 1
		}
		w.le(v, 1)
		return nil
	case vir.OperandNull:
		w.le(0, 4)
		return nil
	case vir.OperandFloat:
		switch t {
		case vir.F64:
			w.le(math.Float64bits(o.Float), 8)
			return nil
		case vir.F32:
			w.le(uint64(math.Float32bits(float32(o.Float))), 4)
			return nil
		}
		return fmt.Errorf("f16 initializers not yet emitted on x86 (TODO)")
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
			w.le(uint64(v), es)
		}
		return nil
	}
	return fmt.Errorf("literal kind %d in initializer", o.Kind)
}