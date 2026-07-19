package x86_64

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/lower/x86_64/abi"
	"github.com/vertex-language/vvm/lower/x86_64/inlineasm"
	"github.com/vertex-language/vvm/lower/x86_64/mcode"
	"github.com/vertex-language/vvm/lower/x86_64/regalloc"
	"github.com/vertex-language/vvm/lower/x86_64/syscallabi"
)

// Lower converts a verified module into an x86_64 Program. The module must
// have passed vir.Verify; Lower assumes the §9 obligations.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target != nil && m.Target.Arch != "x86_64" {
		return nil, fmt.Errorf("lower/x86_64: module targets arch %q, not x86_64", m.Target.Arch)
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

func (lw *lowerer) lookupCallable(name string) (ret vir.Type, params []vir.Param, variadic, ok bool) {
	for _, g := range lw.m.Externs {
		for _, e := range g.Functions {
			if e.Name == name {
				return e.Ret, e.Params, e.Variadic, true
			}
		}
	}
	for _, f := range lw.m.Functions {
		if f.Name == name {
			return f.Ret, f.Params, false, true // fn-def can't express variadic (vir.Function.IsVariadic)
		}
	}
	return nil, nil, false, false
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
	// Spill register-passed parameters into their home slots first — the
	// scratch set (which includes the argument registers) is live-in here
	// and dead everywhere else.
	for i, p := range f.Params {
		if i >= len(abi.ArgRegs) {
			break // stack params already have homes at [rbp+16+8k]
		}
		fl.emit(mcode.Inst{Op: "mov", D: mcode.SlotOpr(p.Name), S: mcode.R(abi.ArgRegs[i]), Sz: 8})
	}
	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(mcode.Inst{Op: "label", Lbl: b.Label})
		}
		for i := range b.Lines {
			line := &b.Lines[i]
			switch {
			case line.Instruction != nil:
				if err := fl.selInst(line.Instruction); err != nil {
					return Func{}, fmt.Errorf("block %s: %s: %w", labelName(b), line.Instruction.Op, err)
				}
			case line.Asm != nil:
				if err := fl.selAsm(line.Asm); err != nil {
					return Func{}, fmt.Errorf("block %s: asm: %w", labelName(b), err)
				}
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
	f       *vir.Function
	types   map[string]vir.Type
	b       []mcode.Inst
	nlbl    int
	nasmlbl int
}

func (fl *fnLower) emit(i mcode.Inst) { fl.b = append(fl.b, i) }
func (fl *fnLower) mov(d, s mcode.Opr) { fl.emit(mcode.Inst{Op: "mov", D: d, S: s, Sz: 8}) }
func (fl *fnLower) alu(op string, d, s mcode.Opr, sz int) {
	fl.emit(mcode.Inst{Op: op, D: d, S: s, Sz: sz})
}
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}
func (fl *fnLower) asmLabel() string {
	fl.nasmlbl++
	return fmt.Sprintf(".A%s.%d.", fl.f.Name, fl.nasmlbl)
}

// typeFunc mirrors the verifier's result-type computation for the subset
// this backend supports (input is verified, so lookups cannot fail
// semantically). Asm `out` bindings participate in the same fixation pass
// as ordinary instructions (§9.37): a first-seen out ident's type is fixed
// from its bound register's width.
func (fl *fnLower) typeFunc() (map[string]vir.Type, error) {
	types := map[string]vir.Type{}
	for _, p := range fl.f.Params {
		if err := fl.checkValueType(p.Type); err != nil {
			return nil, err
		}
		types[p.Name] = p.Type
	}
	arch := "x86_64"
	if fl.m.Target != nil {
		arch = fl.m.Target.Arch
	}
	for _, b := range fl.f.AllBlocks() {
		for i := range b.Lines {
			line := &b.Lines[i]
			switch {
			case line.Instruction != nil:
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
			case line.Asm != nil:
				for _, bind := range line.Asm.Bindings {
					if bind.Kind != vir.BindingOut {
						continue
					}
					if _, done := types[bind.Ident]; done {
						continue
					}
					_, width, ok := inlineasm.Register(arch, bind.Register)
					if !ok {
						return nil, fmt.Errorf("asm out binding: unknown register %q", bind.Register)
					}
					types[bind.Ident] = vir.IntType{Bits: width}
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

// load materializes operand o (of type t) into r as a 64-bit value. Values
// narrower than 64 bits live zero-extended in their slots; signed=true
// requests a sign-extended materialization instead. t == nil means "use
// the slot's 8 bytes as-is".
func (fl *fnLower) load(o vir.Operand, t vir.Type, r mcode.Reg, signed bool) error {
	switch o.Kind {
	case vir.OperandInt:
		fl.mov(mcode.R(r), mcode.Imm(litBits(o.Int, t, signed)))
	case vir.OperandBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		fl.mov(mcode.R(r), mcode.Imm(v))
	case vir.OperandNull:
		fl.mov(mcode.R(r), mcode.Imm(0))
	case vir.OperandFloat:
		return fmt.Errorf("float operands not lowered on x86_64 (TODO)")
	case vir.OperandIdent:
		switch fl.kinds[o.Ident] {
		case "const":
			c := fl.consts[o.Ident]
			return fl.load(c.Value, c.Type, r, signed)
		case "global", "fn", "extern":
			// A name in operand position yields its address (§4, Addresses):
			// lea r, [rip+sym] — PIC-clean by construction.
			fl.mov(mcode.R(r), mcode.SymAddr(o.Ident))
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
				fl.emit(mcode.Inst{Op: "mov", D: mcode.R(r), S: mcode.SlotOpr(o.Ident), Sz: 8})
			case signed:
				fl.emit(mcode.Inst{Op: "movsx", D: mcode.R(r), S: mcode.SlotOpr(o.Ident), Sz: sz})
			default:
				fl.emit(mcode.Inst{Op: "movzx", D: mcode.R(r), S: mcode.SlotOpr(o.Ident), Sz: sz})
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on x86_64", o.Kind)
	}
	return nil
}

// st writes r (already normalized) into name's 8-byte home slot.
func (fl *fnLower) st(name string, r mcode.Reg) {
	fl.emit(mcode.Inst{Op: "mov", D: mcode.SlotOpr(name), S: mcode.R(r), Sz: 8})
}

// norm re-establishes the zero-extended-slot invariant after wrapping or
// sign-extending operations.
func (fl *fnLower) norm(r mcode.Reg, t vir.Type) {
	if it, ok := t.(vir.IntType); ok && it.Bits == 1 {
		fl.alu("and", mcode.R(r), mcode.Imm(1), 4)
		return
	}
	switch szOf(t) {
	case 1, 2:
		fl.emit(mcode.Inst{Op: "movzx", D: mcode.R(r), S: mcode.R(r), Sz: szOf(t)})
	case 4:
		fl.emit(mcode.Inst{Op: "movzx", D: mcode.R(r), S: mcode.R(r), Sz: 4}) // mov r32, r32
	}
}

// ---------------------------------------------------------------------------
// Inline assembly
// ---------------------------------------------------------------------------

// selAsm lowers one asm block: loads `in` bindings, appends the
// dialect-lowered code, then stores `out` bindings back to their idents'
// home slots. Clobbered registers need no explicit action — the
// spill-everything allocator never keeps a live value in a register across
// this block (§4: strict optimization barrier).
func (fl *fnLower) selAsm(ab *vir.AsmBlock) error {
	arch, dialect := "x86_64", vir.DialectIntel
	if fl.m.Target != nil {
		arch = fl.m.Target.Arch
	}
	if fl.m.AsmDialect != nil {
		dialect = *fl.m.AsmDialect
	}

	for _, bind := range ab.Bindings {
		if bind.Kind != vir.BindingIn {
			continue
		}
		r, _, ok := inlineasm.Register(arch, bind.Register)
		if !ok {
			return fmt.Errorf("asm: unknown register %q", bind.Register)
		}
		if err := fl.load(vir.Ident(bind.Ident), fl.types[bind.Ident], r, false); err != nil {
			return err
		}
	}

	prefix := fl.asmLabel()
	insts, err := inlineasm.LowerBlock(dialect, arch, ab.Code, prefix)
	if err != nil {
		return err
	}
	fl.b = append(fl.b, insts...)

	for _, bind := range ab.Bindings {
		if bind.Kind != vir.BindingOut {
			continue
		}
		r, _, ok := inlineasm.Register(arch, bind.Register)
		if !ok {
			return fmt.Errorf("asm: unknown register %q", bind.Register)
		}
		fl.norm(r, fl.types[bind.Ident])
		fl.st(bind.Ident, r)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Instruction selection
// ---------------------------------------------------------------------------

func (fl *fnLower) selInst(in *vir.Instruction) error {
	op, t, a := in.Op, in.Suffix, in.Args
	signedCmp := map[string]byte{"slt": mcode.CondL, "sle": mcode.CondLE, "sgt": mcode.CondG, "sge": mcode.CondGE}
	unsignedCmp := map[string]byte{"eq": mcode.CondE, "ne": mcode.CondNE, "ult": mcode.CondB, "ule": mcode.CondBE, "ugt": mcode.CondA, "uge": mcode.CondAE}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, mcode.RAX, false); err != nil {
			return err
		}
		fl.st(in.Result, mcode.RAX)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, mcode.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, false); err != nil {
			return err
		}
		fl.alu(op, mcode.R(mcode.RAX), mcode.R(mcode.RCX), opSz(t))
		if op == "add" || op == "sub" {
			fl.norm(mcode.RAX, t) // wrap mod 2^N (§4)
		}
		fl.st(in.Result, mcode.RAX)

	case op == "mul":
		if err := fl.load(a[0], t, mcode.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "imul", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RCX), Sz: opSz(t)})
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case op == "neg" || op == "not":
		if err := fl.load(a[0], t, mcode.RAX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: op, S: mcode.R(mcode.RAX), Sz: opSz(t)})
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case op == "abs": // signed; abs(INT_MIN) wraps (§4)
		if err := fl.load(a[0], t, mcode.RAX, true); err != nil {
			return err
		}
		w := opSz(t)
		fl.mov(mcode.R(mcode.RCX), mcode.R(mcode.RAX))
		fl.emit(mcode.Inst{Op: "sar", D: mcode.R(mcode.RCX), S: mcode.Imm(int64(w*8 - 1)), Sz: w})
		fl.alu("xor", mcode.R(mcode.RAX), mcode.R(mcode.RCX), w)
		fl.alu("sub", mcode.R(mcode.RAX), mcode.R(mcode.RCX), w)
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case op == "udiv" || op == "urem":
		if err := fl.load(a[0], t, mcode.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.alu("xor", mcode.R(mcode.RDX), mcode.R(mcode.RDX), 4)
		fl.emit(mcode.Inst{Op: "div", S: mcode.R(mcode.RCX), Sz: w}) // zero divisor -> #DE trap (§6.1)
		r := mcode.RAX
		if op == "urem" {
			r = mcode.RDX
		}
		fl.st(in.Result, r)

	case op == "sdiv" || op == "srem":
		// TODO(§6.1): narrow INT_MIN/-1 (e.g. i8 -128/-1) must trap but the
		// widened 32-bit idiv wraps instead; needs an explicit check for sz<4.
		if err := fl.load(a[0], t, mcode.RAX, true); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, true); err != nil {
			return err
		}
		w := opSz(t)
		if w == 8 {
			fl.emit(mcode.Inst{Op: "cqo"})
		} else {
			fl.emit(mcode.Inst{Op: "cdq"})
		}
		fl.emit(mcode.Inst{Op: "idiv", S: mcode.R(mcode.RCX), Sz: w})
		r := mcode.RAX
		if op == "srem" {
			r = mcode.RDX
		}
		fl.norm(r, t)
		fl.st(in.Result, r)

	case op == "shl" || op == "lshr" || op == "ashr":
		signedV := op == "ashr"
		if err := fl.load(a[0], t, mcode.RAX, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		if bitsOf(t) < 32 { // count mod N (§4); hardware masks mod 32/64 only
			fl.alu("and", mcode.R(mcode.RCX), mcode.Imm(int64(bitsOf(t)-1)), 4)
		}
		x86op := map[string]string{"shl": "shl", "lshr": "shr", "ashr": "sar"}[op]
		fl.emit(mcode.Inst{Op: x86op, D: mcode.R(mcode.RAX), Sz: w}) // by CL
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, mcode.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, false); err != nil {
			return err
		}
		x86op := "rol"
		if op == "rotr" {
			x86op = "ror"
		}
		fl.emit(mcode.Inst{Op: x86op, D: mcode.R(mcode.RAX), Sz: szOf(t)})
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, mcode.RAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, signed); err != nil {
			return err
		}
		fl.alu("cmp", mcode.R(mcode.RAX), mcode.R(mcode.RCX), 8)
		cc := map[string]byte{"smin": mcode.CondG, "smax": mcode.CondL, "umin": mcode.CondA, "umax": mcode.CondB}[op]
		fl.emit(mcode.Inst{Op: "cmovcc", CC: cc, D: mcode.R(mcode.RAX), S: mcode.R(mcode.RCX), Sz: 8})
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, mcode.RAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, signed); err != nil {
			return err
		}
		fl.alu("cmp", mcode.R(mcode.RAX), mcode.R(mcode.RCX), 8)
		fl.emit(mcode.Inst{Op: "setcc", CC: cc, D: mcode.R(mcode.RAX)})
		fl.emit(mcode.Inst{Op: "movzx", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RAX), Sz: 1})
		fl.st(in.Result, mcode.RAX)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on x86_64 (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, mcode.RAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, signed); err != nil {
			return err
		}
		if szOf(t) == 8 {
			m := "mul1"
			if signed {
				m = "imul1"
			}
			fl.emit(mcode.Inst{Op: m, S: mcode.R(mcode.RCX), Sz: 8})
			fl.st(in.Result, mcode.RDX)
		} else {
			fl.emit(mcode.Inst{Op: "imul", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RCX), Sz: 8})
			sh := "shr"
			if signed {
				sh = "sar"
			}
			fl.emit(mcode.Inst{Op: sh, D: mcode.R(mcode.RAX), S: mcode.Imm(int64(bitsOf(t))), Sz: 8})
			fl.norm(mcode.RAX, t)
			fl.st(in.Result, mcode.RAX)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on x86_64 (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, mcode.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.emit(mcode.Inst{Op: "bsr", D: mcode.R(mcode.RDX), S: mcode.R(mcode.RCX), Sz: w})
		fl.mov(mcode.R(mcode.RAX), mcode.Imm(-1))
		fl.emit(mcode.Inst{Op: "cmovcc", CC: mcode.CondNE, D: mcode.R(mcode.RAX), S: mcode.R(mcode.RDX), Sz: 8})
		fl.mov(mcode.R(mcode.RCX), mcode.Imm(int64(bitsOf(t)-1)))
		fl.alu("sub", mcode.R(mcode.RCX), mcode.R(mcode.RAX), 8)
		fl.st(in.Result, mcode.RCX)

	case op == "cttz":
		if err := fl.load(a[0], t, mcode.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.emit(mcode.Inst{Op: "bsf", D: mcode.R(mcode.RDX), S: mcode.R(mcode.RCX), Sz: w})
		fl.mov(mcode.R(mcode.RAX), mcode.Imm(int64(bitsOf(t))))
		fl.emit(mcode.Inst{Op: "cmovcc", CC: mcode.CondNE, D: mcode.R(mcode.RAX), S: mcode.R(mcode.RDX), Sz: 8})
		fl.st(in.Result, mcode.RAX)

	case op == "popcnt":
		// TODO(§10.4): gate on a POPCNT-capable feature tier.
		if err := fl.load(a[0], t, mcode.RCX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "popcnt", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RCX), Sz: opSz(t)})
		fl.st(in.Result, mcode.RAX)

	case op == "bswap":
		if err := fl.load(a[0], t, mcode.RAX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 8, 4:
			fl.emit(mcode.Inst{Op: "bswap", D: mcode.R(mcode.RAX), Sz: szOf(t)})
		default: // i16: ror ax, 8 (i8 is rejected by the verifier, §9.20)
			fl.emit(mcode.Inst{Op: "ror", D: mcode.R(mcode.RAX), S: mcode.Imm(8), Sz: 2})
			fl.norm(mcode.RAX, t)
		}
		fl.st(in.Result, mcode.RAX)

	case op == "bitrev":
		return fmt.Errorf("bitrev not yet lowered on x86_64 (SWAR sequence TODO)")

	case op == "select":
		if err := fl.load(a[0], vir.I1, mcode.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, mcode.RDX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "test", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RAX), Sz: 4})
		fl.emit(mcode.Inst{Op: "cmovcc", CC: mcode.CondE, D: mcode.R(mcode.RCX), S: mcode.R(mcode.RDX), Sz: 8})
		fl.st(in.Result, mcode.RCX)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, mcode.RCX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 8:
			fl.emit(mcode.Inst{Op: "mov", D: mcode.R(mcode.RAX), S: mcode.Mem(mcode.RCX, 0), Sz: 8})
		case 4:
			fl.emit(mcode.Inst{Op: "movzx", D: mcode.R(mcode.RAX), S: mcode.Mem(mcode.RCX, 0), Sz: 4})
		default:
			fl.emit(mcode.Inst{Op: "movzx", D: mcode.R(mcode.RAX), S: mcode.Mem(mcode.RCX, 0), Sz: szOf(t)})
		}
		fl.st(in.Result, mcode.RAX)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, mcode.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RAX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "mov", D: mcode.Mem(mcode.RCX, 0), S: mcode.R(mcode.RAX), Sz: szOf(t)})
		if op == "atomic_store" && lastOrd(a) == "seqcst" {
			fl.emit(mcode.Inst{Op: "mfence"})
		}

	case op == "alloca":
		if err := fl.load(a[0], vir.I64, mcode.RAX, false); err != nil {
			return err
		}
		fl.alu("add", mcode.R(mcode.RAX), mcode.Imm(15), 8)
		fl.alu("and", mcode.R(mcode.RAX), mcode.Imm(-16), 8)
		fl.alu("sub", mcode.R(mcode.RSP), mcode.R(mcode.RAX), 8)
		if in.Align > 16 {
			fl.alu("and", mcode.R(mcode.RSP), mcode.Imm(int64(-in.Align)), 8)
		}
		fl.st(in.Result, mcode.RSP)

	case op == "field":
		off, err := fl.lay.FieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, mcode.RAX, false); err != nil {
			return err
		}
		if off != 0 {
			fl.alu("add", mcode.R(mcode.RAX), mcode.Imm(int64(off)), 8)
		}
		fl.st(in.Result, mcode.RAX)

	case op == "index":
		esz, err := fl.lay.Size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, mcode.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, mcode.RCX, true); err != nil { // index is signed (§4)
			return err
		}
		fl.emit(mcode.Inst{Op: "imul3", D: mcode.R(mcode.RCX), S: mcode.R(mcode.RCX), Imm: int64(esz), Sz: 8})
		fl.alu("add", mcode.R(mcode.RAX), mcode.R(mcode.RCX), 8)
		fl.st(in.Result, mcode.RAX)

	case op == "memcopy" || op == "memset":
		if err := fl.load(a[0], vir.Ptr, mcode.RDI, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, mcode.RSI, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, mcode.RAX, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I64, mcode.RCX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "cld"})
		if op == "memcopy" {
			fl.emit(mcode.Inst{Op: "rep_movsb"})
		} else {
			fl.emit(mcode.Inst{Op: "rep_stosb"})
		}

	case op == "memmove":
		fwd, done := fl.label(), fl.label()
		if err := fl.load(a[0], vir.Ptr, mcode.RDI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, mcode.RSI, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, mcode.RCX, false); err != nil {
			return err
		}
		fl.alu("cmp", mcode.R(mcode.RSI), mcode.R(mcode.RDI), 8)
		fl.emit(mcode.Inst{Op: "jcc", CC: mcode.CondAE, Lbl: fwd})
		fl.alu("add", mcode.R(mcode.RSI), mcode.R(mcode.RCX), 8)
		fl.alu("sub", mcode.R(mcode.RSI), mcode.Imm(1), 8)
		fl.alu("add", mcode.R(mcode.RDI), mcode.R(mcode.RCX), 8)
		fl.alu("sub", mcode.R(mcode.RDI), mcode.Imm(1), 8)
		fl.emit(mcode.Inst{Op: "std"})
		fl.emit(mcode.Inst{Op: "rep_movsb"})
		fl.emit(mcode.Inst{Op: "cld"})
		fl.emit(mcode.Inst{Op: "jmp", Lbl: done})
		fl.emit(mcode.Inst{Op: "label", Lbl: fwd})
		fl.emit(mcode.Inst{Op: "cld"})
		fl.emit(mcode.Inst{Op: "rep_movsb"})
		fl.emit(mcode.Inst{Op: "label", Lbl: done})

	case op == "prefetch":
		return nil // advisory (§4); dropped in this bring-up

	case op == "fence":
		if lastOrd(a) == "seqcst" {
			fl.emit(mcode.Inst{Op: "mfence"})
		}
		return nil // acquire/release/acqrel fences are compiler-only on x86 TSO

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_xchg":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86_64 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, mcode.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RAX, false); err != nil {
			return err
		}
		switch op {
		case "atomic_sub":
			fl.emit(mcode.Inst{Op: "neg", S: mcode.R(mcode.RAX), Sz: w})
			fallthrough
		case "atomic_add":
			fl.emit(mcode.Inst{Op: "lock_xadd", D: mcode.Mem(mcode.RCX, 0), S: mcode.R(mcode.RAX), Sz: w})
		case "atomic_xchg":
			fl.emit(mcode.Inst{Op: "xchg", D: mcode.Mem(mcode.RCX, 0), S: mcode.R(mcode.RAX), Sz: w})
		}
		fl.st(in.Result, mcode.RAX)

	case op == "atomic_and" || op == "atomic_or" || op == "atomic_xor":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86_64 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, mcode.RSI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RDX, false); err != nil {
			return err
		}
		loop := fl.label()
		fl.emit(mcode.Inst{Op: "mov", D: mcode.R(mcode.RAX), S: mcode.Mem(mcode.RSI, 0), Sz: w})
		fl.emit(mcode.Inst{Op: "label", Lbl: loop})
		fl.mov(mcode.R(mcode.RCX), mcode.R(mcode.RAX))
		fl.alu(op[len("atomic_"):], mcode.R(mcode.RCX), mcode.R(mcode.RDX), w)
		fl.emit(mcode.Inst{Op: "lock_cmpxchg", D: mcode.Mem(mcode.RSI, 0), S: mcode.R(mcode.RCX), Sz: w})
		fl.emit(mcode.Inst{Op: "jcc", CC: mcode.CondNE, Lbl: loop})
		fl.st(in.Result, mcode.RAX)

	case op == "cmpxchg":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on x86_64 (i128 needs cmpxchg16b tier, TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, mcode.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, mcode.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, mcode.RDX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "lock_cmpxchg", D: mcode.Mem(mcode.RCX, 0), S: mcode.R(mcode.RDX), Sz: w})
		fl.st(in.Result, mcode.RAX)

	case op == "syscall":
		os := ""
		if fl.m.Target != nil {
			os = fl.m.Target.OS
		}
		conv, ok := syscallabi.Lookup(os)
		if !ok {
			return fmt.Errorf("syscalls not supported on target OS %q on x86_64", os)
		}
		if len(a)-1 > len(conv.Args) {
			return fmt.Errorf("syscall: too many arguments for %s (max %d)", os, len(conv.Args))
		}
		if err := fl.load(a[0], vir.I64, conv.NR, false); err != nil { // sysno is usize-width
			return err
		}
		for i, argOp := range a[1:] {
			if err := fl.load(argOp, nil, conv.Args[i], false); err != nil {
				return err
			}
		}
		fl.emit(mcode.Inst{Op: conv.Trap})
		if !vir.IsVoid(t) && in.Result != "" {
			fl.norm(conv.Result, t)
			fl.st(in.Result, conv.Result)
		}

	case op == "trunc":
		if err := fl.load(a[0], nil, mcode.RAX, false); err != nil {
			return err
		}
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case op == "zext":
		if err := fl.load(a[0], nil, mcode.RAX, false); err != nil {
			return err
		}
		fl.st(in.Result, mcode.RAX) // slots are already zero-extended

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if it, ok := st.(vir.IntType); ok && it.Bits == 1 {
			if err := fl.load(a[0], st, mcode.RAX, false); err != nil {
				return err
			}
			fl.emit(mcode.Inst{Op: "neg", S: mcode.R(mcode.RAX), Sz: 8}) // i1 sext: 1 -> -1
		} else {
			if err := fl.load(a[0], st, mcode.RAX, true); err != nil {
				return err
			}
		}
		fl.norm(mcode.RAX, t)
		fl.st(in.Result, mcode.RAX)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on x86_64 (TODO)")
		}
		if err := fl.load(a[0], st, mcode.RAX, false); err != nil {
			return err
		}
		fl.st(in.Result, mcode.RAX)

	case op == "call":
		return fl.selCall(in)

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

func (fl *fnLower) selOverflow(in *vir.Instruction) error {
	t, a := in.Suffix, in.Args
	signed := in.Op[0] == 's'
	if err := fl.load(a[0], t, mcode.RAX, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, mcode.RCX, signed); err != nil {
		return err
	}
	w := szOf(t)
	if w == 4 || w == 8 {
		var cc byte
		switch in.Op {
		case "uaddo", "usubo":
			cc = mcode.CondB
		default:
			cc = mcode.CondO
		}
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", mcode.R(mcode.RAX), mcode.R(mcode.RCX), w)
		case "usubo", "ssubo":
			fl.alu("sub", mcode.R(mcode.RAX), mcode.R(mcode.RCX), w)
		case "umulo":
			fl.emit(mcode.Inst{Op: "mul1", S: mcode.R(mcode.RCX), Sz: w})
		case "smulo":
			fl.emit(mcode.Inst{Op: "imul1", S: mcode.R(mcode.RCX), Sz: w})
		}
		fl.emit(mcode.Inst{Op: "setcc", CC: cc, D: mcode.R(mcode.RAX)})
	} else {
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", mcode.R(mcode.RAX), mcode.R(mcode.RCX), 8)
		case "usubo", "ssubo":
			fl.alu("sub", mcode.R(mcode.RAX), mcode.R(mcode.RCX), 8)
		case "umulo", "smulo":
			fl.emit(mcode.Inst{Op: "imul", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RCX), Sz: 8})
		}
		ext := "movzx"
		if signed {
			ext = "movsx"
		}
		fl.emit(mcode.Inst{Op: ext, D: mcode.R(mcode.RCX), S: mcode.R(mcode.RAX), Sz: w})
		fl.alu("cmp", mcode.R(mcode.RCX), mcode.R(mcode.RAX), 8)
		fl.emit(mcode.Inst{Op: "setcc", CC: mcode.CondNE, D: mcode.R(mcode.RAX)})
	}
	fl.emit(mcode.Inst{Op: "movzx", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RAX), Sz: 1})
	fl.st(in.Result, mcode.RAX)
	return nil
}

func (fl *fnLower) typeOfOperand(o vir.Operand) (vir.Type, error) {
	if o.Kind != vir.OperandIdent {
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
		if args[i].Kind == vir.OperandOrdering {
			return args[i].Ordering
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Calls (System V AMD64) and terminators
// ---------------------------------------------------------------------------

func (fl *fnLower) selCall(in *vir.Instruction) error {
	args := in.Args
	var params []vir.Param
	var ret vir.Type
	variadic := false
	indirect := in.Sig != ""
	if indirect {
		for _, s := range fl.m.FunctionSignatures {
			if s.Name == in.Sig {
				ret = s.Ret
				variadic = s.Variadic
				for _, pt := range s.Params {
					params = append(params, vir.Param{Type: pt})
				}
			}
		}
		args = args[1:]
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
			return fmt.Errorf("byval struct passing not yet lowered on x86_64 (SysV classification TODO)")
		}
	}

	plan := abi.PlanCall(len(args))
	if plan.StackBytes > 0 {
		fl.alu("sub", mcode.R(mcode.RSP), mcode.Imm(int64(plan.StackBytes)), 8)
	}
	for i, a := range args {
		if err := fl.load(a, nil, mcode.RAX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "mov", D: mcode.Mem(mcode.RSP, plan.StageOffset(i)), S: mcode.R(mcode.RAX), Sz: 8})
	}
	if indirect {
		if err := fl.load(in.Args[0], vir.Ptr, mcode.R10, false); err != nil {
			return err
		}
	}
	for i := 0; i < plan.NumRegArgs; i++ {
		fl.emit(mcode.Inst{Op: "mov", D: mcode.R(abi.ArgRegs[i]), S: mcode.Mem(mcode.RSP, plan.StageOffset(i)), Sz: 8})
	}
	if variadic {
		fl.alu("xor", mcode.R(mcode.RAX), mcode.R(mcode.RAX), 4)
	}
	if indirect {
		fl.emit(mcode.Inst{Op: "call_r", S: mcode.R(mcode.R10)})
	} else {
		fl.emit(mcode.Inst{Op: "call_sym", Sym: in.Args[0].Ident})
	}
	if plan.StackBytes > 0 {
		fl.alu("add", mcode.R(mcode.RSP), mcode.Imm(int64(plan.StackBytes)), 8)
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.norm(mcode.RAX, ret)
		fl.st(in.Result, mcode.RAX)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		fl.emit(mcode.Inst{Op: "jmp", Lbl: x.Label})
	case vir.BranchIf:
		if err := fl.load(x.Cond, vir.I1, mcode.RAX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "test", D: mcode.R(mcode.RAX), S: mcode.R(mcode.RAX), Sz: 4})
		fl.emit(mcode.Inst{Op: "jcc", CC: mcode.CondNE, Lbl: x.Then})
		fl.emit(mcode.Inst{Op: "jmp", Lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I64
		}
		if err := fl.load(x.Value, vt, mcode.RAX, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.mov(mcode.R(mcode.RCX), mcode.Imm(litBits(c.Value, vt, false)))
			fl.alu("cmp", mcode.R(mcode.RAX), mcode.R(mcode.RCX), 8)
			fl.emit(mcode.Inst{Op: "jcc", CC: mcode.CondE, Lbl: c.Label})
		}
		fl.emit(mcode.Inst{Op: "jmp", Lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, mcode.RAX, false); err != nil {
				return err
			}
		}
		fl.emit(mcode.Inst{Op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(mcode.Inst{Op: "ud2"})
	case vir.Unreachable:
		fl.emit(mcode.Inst{Op: "ud2"})
	default:
		return fmt.Errorf("terminator %T not lowered on x86_64", t)
	}
	return nil
}

func (fl *fnLower) selTailCall(x vir.TailCall) error {
	args := x.Args
	indirect := x.Callee == ""
	variadic := false
	if indirect {
		args = args[1:]
		for _, s := range fl.m.FunctionSignatures {
			if s.Name == x.Sig {
				variadic = s.Variadic
			}
		}
	} else {
		_, _, v, _ := fl.lookupCallable(x.Callee)
		variadic = v
	}
	needStack := len(args) - len(abi.ArgRegs)
	if needStack < 0 {
		needStack = 0
	}
	haveStack := len(fl.f.Params) - len(abi.ArgRegs)
	if haveStack < 0 {
		haveStack = 0
	}
	if needStack > haveStack {
		return fmt.Errorf("tailcall with %d stack-arg slots exceeds caller's %d incoming slots (frame-growing tailcalls TODO)", needStack, haveStack)
	}
	for _, a := range args {
		if err := fl.load(a, nil, mcode.RAX, false); err != nil {
			return err
		}
		fl.emit(mcode.Inst{Op: "push", S: mcode.R(mcode.RAX)})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, mcode.R10, false); err != nil {
			return err
		}
	}
	for i := len(args) - 1; i >= 0; i-- {
		if i < len(abi.ArgRegs) {
			fl.emit(mcode.Inst{Op: "pop", D: mcode.R(abi.ArgRegs[i])})
		} else {
			fl.emit(mcode.Inst{Op: "pop", D: mcode.R(mcode.RAX)})
			fl.emit(mcode.Inst{Op: "mov", D: mcode.Mem(mcode.RBP, int32(16+8*(i-len(abi.ArgRegs)))), S: mcode.R(mcode.RAX), Sz: 8})
		}
	}
	if variadic {
		fl.alu("xor", mcode.R(mcode.RAX), mcode.R(mcode.RAX), 4)
	}
	if indirect {
		fl.emit(mcode.Inst{Op: "epi_jmp_r", S: mcode.R(mcode.R10)})
	} else {
		fl.emit(mcode.Inst{Op: "epi_jmp_sym", Sym: x.Callee})
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
		w.fx = append(w.fx, Fixup{Offset: uint32(len(w.b)), Symbol: x.Name, Kind: FixupAbs64})
		w.le(0, 8)
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
			s, err := w.lay.structOf(tt.Name)
			if err != nil {
				return err
			}
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
		w.le(0, 8)
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
		return fmt.Errorf("f16 initializers not yet emitted on x86_64 (TODO)")
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