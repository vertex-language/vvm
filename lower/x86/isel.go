package x86

import (
	"fmt"

	isax86 "github.com/vertex-language/vvm/isa/x86"
	"github.com/vertex-language/vvm/ir/vir"
)

// Lower converts a verified module into an x86 Program — see x86.go.
// This file is instruction selection: turning one vir.Function's body
// into an Inst stream over the EAX/ECX/EDX scratch set, with every named
// value materialized through its own stack slot (Slot). Register operands
// are isax86.Reg values used directly (isax86.REAX, ...) — no local
// rEAX-style aliases.

func (lw *lowerer) lowerFunc(f *vir.Function) (Func, error) {
	fl := &fnLower{lowerer: lw, f: f}
	var err error
	if fl.types, err = fl.typeFunc(); err != nil {
		return Func{}, err
	}
	for bi, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(Inst{Op: "label", Lbl: b.Label})
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
	fr := BuildFrame(f, fl.b)
	code, fixups, err := assemble(fl.b, fr)
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
	b     []Inst
	nlbl  int
}

func (fl *fnLower) emit(i Inst)            { fl.b = append(fl.b, i) }
func (fl *fnLower) mov(d, s Opr)           { fl.emit(Inst{Op: "mov", D: d, S: s, Sz: 4}) }
func (fl *fnLower) alu(op string, d, s Opr) { fl.emit(Inst{Op: op, D: d, S: s}) }
func (fl *fnLower) label() string {
	fl.nlbl++
	return fmt.Sprintf(".L%s.%d", fl.f.Name, fl.nlbl)
}

// typeFunc mirrors the verifier's result-type/type-fixation computation
// for the subset this backend supports (input is verified, so lookups
// cannot fail semantically), including asm `out` bindings, which follow
// the same Join Convention as ordinary instructions (§4, §5 rule 2).
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

// load materializes operand o (of type t) into r as a 32-bit value.
// Values narrower than 32 bits live zero-extended; signed=true requests a
// sign-extended materialization instead.
func (fl *fnLower) load(o vir.Operand, t vir.Type, r isax86.Reg, signed bool) error {
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
				fl.emit(Inst{Op: "mov", D: R(r), S: Slot(o.Ident), Sz: 4})
			case signed:
				fl.emit(Inst{Op: "movsx", D: R(r), S: Slot(o.Ident), Sz: sz})
			default:
				fl.emit(Inst{Op: "movzx", D: R(r), S: Slot(o.Ident), Sz: sz})
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on x86", o.Kind)
	}
	return nil
}

func (fl *fnLower) st(name string, r isax86.Reg) {
	fl.emit(Inst{Op: "mov", D: Slot(name), S: R(r), Sz: 4})
}

func (fl *fnLower) norm(r isax86.Reg, t vir.Type) {
	if it, ok := t.(vir.IntType); ok && it.Bits == 1 {
		fl.alu("and", R(r), Imm(1))
		return
	}
	switch szOf(t) {
	case 1, 2:
		fl.emit(Inst{Op: "movzx", D: R(r), S: R(r), Sz: szOf(t)})
	}
}

// Resolve implements SymbolResolver: an ident used as a raw asm-line
// operand resolves exactly like an ordinary instruction operand would
// (§4 Addresses).
func (fl *fnLower) Resolve(name string) (Opr, error) {
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
		return Opr{}, fmt.Errorf("asm: const %q's value kind is not usable as a raw operand", name)
	case "global", "fn", "extern":
		return SymAddr(name), nil
	case "struct", "fnsig":
		return Opr{}, fmt.Errorf("asm: %q names a compile-time entity, not a value", name)
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
	insts, err := LowerBlock(*fl.m.AsmDialect, fl.m.Target.Arch, a, fl, label)
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
	conv, ok := syscallConventionFor(osName)
	if !ok {
		if osName == "none" || osName == "uefi" {
			// §4: "executes a runtime trap if unsupported" absent an
			// explicitly enabled feature-tier flag providing a convention.
			fl.emit(Inst{Op: "ud2"})
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
			fl.emit(Inst{Op: "push", S: Imm(0)})
		}
		for i := len(stackVals) - 1; i >= 0; i-- {
			if err := fl.load(stackVals[i], nil, isax86.RECX, false); err != nil {
				return err
			}
			fl.emit(Inst{Op: "push", S: R(isax86.RECX)})
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
		fl.alu("add", R(isax86.RESP), Imm(cleanup))
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
	signedCmp := map[string]byte{"slt": isax86.CondL, "sle": isax86.CondLE, "sgt": isax86.CondG, "sge": isax86.CondGE}
	unsignedCmp := map[string]byte{"eq": isax86.CondE, "ne": isax86.CondNE, "ult": isax86.CondB, "ule": isax86.CondBE, "ugt": isax86.CondA, "uge": isax86.CondAE}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		fl.st(in.Result, isax86.REAX)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.alu(op, R(isax86.REAX), R(isax86.RECX))
		if op == "add" || op == "sub" {
			fl.norm(isax86.REAX, t)
		}
		fl.st(in.Result, isax86.REAX)

	case op == "mul":
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "imul", D: R(isax86.REAX), S: R(isax86.RECX)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == "neg" || op == "not":
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: op, S: R(isax86.REAX)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == "abs":
		if err := fl.load(a[0], t, isax86.REAX, true); err != nil {
			return err
		}
		fl.mov(R(isax86.RECX), R(isax86.REAX))
		fl.emit(Inst{Op: "sar", D: R(isax86.RECX), S: Imm(31), Sz: 4})
		fl.alu("xor", R(isax86.REAX), R(isax86.RECX))
		fl.alu("sub", R(isax86.REAX), R(isax86.RECX))
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == "udiv" || op == "urem":
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.alu("xor", R(isax86.REDX), R(isax86.REDX))
		fl.emit(Inst{Op: "div", S: R(isax86.RECX)})
		r := isax86.REAX
		if op == "urem" {
			r = isax86.REDX
		}
		fl.st(in.Result, r)

	case op == "sdiv" || op == "srem":
		// TODO(§6.1): narrow INT_MIN/-1 (e.g. i8 -128/-1) must trap but the
		// widened 32-bit idiv wraps instead; needs an explicit check for sz<4.
		if err := fl.load(a[0], t, isax86.REAX, true); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, true); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cdq"})
		fl.emit(Inst{Op: "idiv", S: R(isax86.RECX)})
		r := isax86.REAX
		if op == "srem" {
			r = isax86.REDX
		}
		fl.norm(r, t)
		fl.st(in.Result, r)

	case op == "shl" || op == "lshr" || op == "ashr":
		signedV := op == "ashr"
		if err := fl.load(a[0], t, isax86.REAX, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		if bitsOf(t) < 32 {
			fl.alu("and", R(isax86.RECX), Imm(int64(bitsOf(t)-1)))
		}
		x86op := map[string]string{"shl": "shl", "lshr": "shr", "ashr": "sar"}[op]
		fl.emit(Inst{Op: x86op, D: R(isax86.REAX), Sz: 4})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		x86op := "rol"
		if op == "rotr" {
			x86op = "ror"
		}
		fl.emit(Inst{Op: x86op, D: R(isax86.REAX), Sz: szOf(t)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, isax86.REAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(isax86.REAX), R(isax86.RECX))
		cc := map[string]byte{"smin": isax86.CondG, "smax": isax86.CondL, "umin": isax86.CondA, "umax": isax86.CondB}[op]
		fl.emit(Inst{Op: "cmovcc", CC: cc, D: R(isax86.REAX), S: R(isax86.RECX)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, isax86.REAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(isax86.REAX), R(isax86.RECX))
		fl.emit(Inst{Op: "setcc", CC: cc, D: R(isax86.REAX)})
		fl.emit(Inst{Op: "movzx", D: R(isax86.REAX), S: R(isax86.REAX), Sz: 1})
		fl.st(in.Result, isax86.REAX)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on x86 (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, isax86.REAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, signed); err != nil {
			return err
		}
		if szOf(t) == 4 {
			m := "mul32"
			if signed {
				m = "imul32"
			}
			fl.emit(Inst{Op: m, S: R(isax86.RECX)})
			fl.st(in.Result, isax86.REDX)
		} else {
			fl.emit(Inst{Op: "imul", D: R(isax86.REAX), S: R(isax86.RECX)})
			sh := "shr"
			if signed {
				sh = "sar"
			}
			fl.emit(Inst{Op: sh, D: R(isax86.REAX), S: Imm(int64(bitsOf(t))), Sz: 4})
			fl.norm(isax86.REAX, t)
			fl.st(in.Result, isax86.REAX)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on x86 (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "bsr", D: R(isax86.REDX), S: R(isax86.RECX)})
		fl.mov(R(isax86.REAX), Imm(-1))
		fl.emit(Inst{Op: "cmovcc", CC: isax86.CondNE, D: R(isax86.REAX), S: R(isax86.REDX)})
		fl.mov(R(isax86.RECX), Imm(int64(bitsOf(t)-1)))
		fl.alu("sub", R(isax86.RECX), R(isax86.REAX))
		fl.st(in.Result, isax86.RECX)

	case op == "cttz":
		if err := fl.load(a[0], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "bsf", D: R(isax86.REDX), S: R(isax86.RECX)})
		fl.mov(R(isax86.REAX), Imm(int64(bitsOf(t))))
		fl.emit(Inst{Op: "cmovcc", CC: isax86.CondNE, D: R(isax86.REAX), S: R(isax86.REDX)})
		fl.st(in.Result, isax86.REAX)

	case op == "popcnt":
		if err := fl.load(a[0], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "popcnt", D: R(isax86.REAX), S: R(isax86.RECX)})
		fl.st(in.Result, isax86.REAX)

	case op == "bswap":
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if szOf(t) == 4 {
			fl.emit(Inst{Op: "bswap", D: R(isax86.REAX)})
		} else {
			fl.emit(Inst{Op: "ror", D: R(isax86.REAX), S: Imm(8), Sz: 2})
			fl.norm(isax86.REAX, t)
		}
		fl.st(in.Result, isax86.REAX)

	case op == "bitrev":
		return fmt.Errorf("bitrev not yet lowered on x86 (SWAR sequence TODO)")

	case op == "select":
		if err := fl.load(a[0], vir.I1, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, isax86.REDX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "test", D: R(isax86.REAX), S: R(isax86.REAX)})
		fl.emit(Inst{Op: "cmovcc", CC: isax86.CondE, D: R(isax86.RECX), S: R(isax86.REDX)})
		fl.st(in.Result, isax86.RECX)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, isax86.RECX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 4:
			fl.emit(Inst{Op: "mov", D: R(isax86.REAX), S: Mem(isax86.RECX, 0), Sz: 4})
		default:
			fl.emit(Inst{Op: "movzx", D: R(isax86.REAX), S: Mem(isax86.RECX, 0), Sz: szOf(t)})
		}
		fl.st(in.Result, isax86.REAX)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, isax86.RECX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mov", D: Mem(isax86.RECX, 0), S: R(isax86.REAX), Sz: szOf(t)})
		if op == "atomic_store" && lastOrd(a) == "seqcst" {
			fl.emit(Inst{Op: "mfence"})
		}

	case op == "alloca":
		if err := fl.load(a[0], vir.I32, isax86.REAX, false); err != nil {
			return err
		}
		fl.alu("add", R(isax86.REAX), Imm(3))
		fl.alu("and", R(isax86.REAX), Imm(-4))
		fl.alu("sub", R(isax86.RESP), R(isax86.REAX))
		if in.Align > 4 {
			fl.alu("and", R(isax86.RESP), Imm(int64(-in.Align)))
		}
		fl.st(in.Result, isax86.RESP)

	case op == "field":
		off, err := fl.lay.FieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, isax86.REAX, false); err != nil {
			return err
		}
		if off != 0 {
			fl.alu("add", R(isax86.REAX), Imm(int64(off)))
		}
		fl.st(in.Result, isax86.REAX)

	case op == "index":
		esz, err := fl.lay.Size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, isax86.RECX, true); err != nil {
			return err
		}
		fl.emit(Inst{Op: "imul3", D: R(isax86.RECX), S: R(isax86.RECX), Imm: int64(esz)})
		fl.alu("add", R(isax86.REAX), R(isax86.RECX))
		fl.st(in.Result, isax86.REAX)

	case op == "memcopy" || op == "memset":
		if err := fl.load(a[0], vir.Ptr, isax86.REDI, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, isax86.RESI, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, isax86.REAX, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I32, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cld"})
		if op == "memcopy" {
			fl.emit(Inst{Op: "rep_movsb"})
		} else {
			fl.emit(Inst{Op: "rep_stosb"})
		}

	case op == "memmove":
		fwd, done := fl.label(), fl.label()
		if err := fl.load(a[0], vir.Ptr, isax86.REDI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, isax86.RESI, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I32, isax86.RECX, false); err != nil {
			return err
		}
		fl.alu("cmp", R(isax86.RESI), R(isax86.REDI))
		fl.emit(Inst{Op: "jcc", CC: isax86.CondAE, Lbl: fwd})
		fl.alu("add", R(isax86.RESI), R(isax86.RECX))
		fl.alu("sub", R(isax86.RESI), Imm(1))
		fl.alu("add", R(isax86.REDI), R(isax86.RECX))
		fl.alu("sub", R(isax86.REDI), Imm(1))
		fl.emit(Inst{Op: "std"})
		fl.emit(Inst{Op: "rep_movsb"})
		fl.emit(Inst{Op: "cld"})
		fl.emit(Inst{Op: "jmp", Lbl: done})
		fl.emit(Inst{Op: "label", Lbl: fwd})
		fl.emit(Inst{Op: "cld"})
		fl.emit(Inst{Op: "rep_movsb"})
		fl.emit(Inst{Op: "label", Lbl: done})

	case op == "prefetch":
		return nil

	case op == "fence":
		if lastOrd(a) == "seqcst" {
			fl.emit(Inst{Op: "mfence"})
		}
		return nil

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_xchg":
		if szOf(t) != 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, isax86.RECX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.REAX, false); err != nil {
			return err
		}
		switch op {
		case "atomic_sub":
			fl.emit(Inst{Op: "neg", S: R(isax86.REAX)})
			fallthrough
		case "atomic_add":
			fl.emit(Inst{Op: "lock_xadd", D: Mem(isax86.RECX, 0), S: R(isax86.REAX)})
		case "atomic_xchg":
			fl.emit(Inst{Op: "xchg", D: Mem(isax86.RECX, 0), S: R(isax86.REAX)})
		}
		fl.st(in.Result, isax86.REAX)

	case op == "atomic_and" || op == "atomic_or" || op == "atomic_xor":
		if szOf(t) != 4 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, isax86.RESI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.REDX, false); err != nil {
			return err
		}
		loop := fl.label()
		fl.emit(Inst{Op: "mov", D: R(isax86.REAX), S: Mem(isax86.RESI, 0), Sz: 4})
		fl.emit(Inst{Op: "label", Lbl: loop})
		fl.mov(R(isax86.RECX), R(isax86.REAX))
		fl.alu(op[len("atomic_"):], R(isax86.RECX), R(isax86.REDX))
		fl.emit(Inst{Op: "lock_cmpxchg", D: Mem(isax86.RESI, 0), S: R(isax86.RECX)})
		fl.emit(Inst{Op: "jcc", CC: isax86.CondNE, Lbl: loop})
		fl.st(in.Result, isax86.REAX)

	case op == "cmpxchg":
		if szOf(t) != 4 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on x86 (TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, isax86.RECX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, isax86.REDX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "lock_cmpxchg", D: Mem(isax86.RECX, 0), S: R(isax86.REDX)})
		fl.st(in.Result, isax86.REAX)

	case op == "trunc":
		if err := fl.load(a[0], nil, isax86.REAX, false); err != nil {
			return err
		}
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == "zext":
		if err := fl.load(a[0], nil, isax86.REAX, false); err != nil {
			return err
		}
		fl.st(in.Result, isax86.REAX)

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if it, ok := st.(vir.IntType); ok && it.Bits == 1 {
			if err := fl.load(a[0], st, isax86.REAX, false); err != nil {
				return err
			}
			fl.emit(Inst{Op: "neg", S: R(isax86.REAX)})
		} else {
			if err := fl.load(a[0], st, isax86.REAX, true); err != nil {
				return err
			}
		}
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on x86 (TODO)")
		}
		if err := fl.load(a[0], st, isax86.REAX, false); err != nil {
			return err
		}
		fl.st(in.Result, isax86.REAX)

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
	if err := fl.load(a[0], t, isax86.REAX, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, isax86.RECX, signed); err != nil {
		return err
	}
	if szOf(t) == 4 {
		var cc byte
		switch in.Op {
		case "uaddo", "usubo":
			cc = isax86.CondB
		case "saddo", "ssubo", "smulo":
			cc = isax86.CondO
		case "umulo":
			cc = isax86.CondO
		}
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(isax86.REAX), R(isax86.RECX))
		case "usubo", "ssubo":
			fl.alu("sub", R(isax86.REAX), R(isax86.RECX))
		case "umulo":
			fl.emit(Inst{Op: "mul32", S: R(isax86.RECX)})
		case "smulo":
			fl.emit(Inst{Op: "imul32", S: R(isax86.RECX)})
		}
		fl.emit(Inst{Op: "setcc", CC: cc, D: R(isax86.REAX)})
	} else {
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(isax86.REAX), R(isax86.RECX))
		case "usubo", "ssubo":
			fl.alu("sub", R(isax86.REAX), R(isax86.RECX))
		case "umulo", "smulo":
			fl.emit(Inst{Op: "imul", D: R(isax86.REAX), S: R(isax86.RECX)})
		}
		ext := "movzx"
		if signed {
			ext = "movsx"
		}
		fl.emit(Inst{Op: ext, D: R(isax86.RECX), S: R(isax86.REAX), Sz: szOf(t)})
		fl.alu("cmp", R(isax86.RECX), R(isax86.REAX))
		fl.emit(Inst{Op: "setcc", CC: isax86.CondNE, D: R(isax86.REAX)})
	}
	fl.emit(Inst{Op: "movzx", D: R(isax86.REAX), S: R(isax86.REAX), Sz: 1})
	fl.st(in.Result, isax86.REAX)
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

	slots, total, err := PlanCall(params, len(args), func(name string) (int, error) {
		sz, _, _, err := fl.lay.StructLayout(name)
		return sz, err
	})
	if err != nil {
		return err
	}
	if total > 0 {
		fl.alu("sub", R(isax86.RESP), Imm(int64(total)))
	}
	for i, a := range args {
		if slots[i].ByVal != "" {
			sz, _, _, err := fl.lay.StructLayout(slots[i].ByVal)
			if err != nil {
				return err
			}
			if err := fl.load(a, vir.Ptr, isax86.RESI, false); err != nil {
				return err
			}
			fl.emit(Inst{Op: "lea", D: R(isax86.REDI), S: Mem(isax86.RESP, int32(slots[i].Offset))})
			fl.mov(R(isax86.RECX), Imm(int64(sz)))
			fl.emit(Inst{Op: "cld"})
			fl.emit(Inst{Op: "rep_movsb"})
			continue
		}
		if err := fl.load(a, nil, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mov", D: Mem(isax86.RESP, int32(slots[i].Offset)), S: R(isax86.REAX), Sz: 4})
	}
	if indirect {
		if err := fl.load(in.Args[0], vir.Ptr, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "call_r", S: R(isax86.REAX)})
	} else {
		fl.emit(Inst{Op: "call_sym", Sym: in.Args[0].Ident})
	}
	if total > 0 {
		fl.alu("add", R(isax86.RESP), Imm(int64(total)))
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.norm(isax86.REAX, ret)
		fl.st(in.Result, isax86.REAX)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		fl.emit(Inst{Op: "jmp", Lbl: x.Label})
	case vir.BranchIf:
		if err := fl.load(x.Cond, vir.I1, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "test", D: R(isax86.REAX), S: R(isax86.REAX)})
		fl.emit(Inst{Op: "jcc", CC: isax86.CondNE, Lbl: x.Then})
		fl.emit(Inst{Op: "jmp", Lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I32
		}
		if err := fl.load(x.Value, vt, isax86.REAX, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.alu("cmp", R(isax86.REAX), Imm(litBits(c.Value, vt, false)))
			fl.emit(Inst{Op: "jcc", CC: isax86.CondE, Lbl: c.Label})
		}
		fl.emit(Inst{Op: "jmp", Lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, isax86.REAX, false); err != nil {
				return err
			}
		}
		fl.emit(Inst{Op: "epi_ret"})
	case vir.TailCall:
		return fl.selTailCall(x)
	case vir.Trap:
		fl.emit(Inst{Op: "ud2"})
	case vir.Unreachable:
		fl.emit(Inst{Op: "ud2"})
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
		if err := fl.load(a, nil, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "push", S: R(isax86.REAX)})
	}
	for i := len(args) - 1; i >= 0; i-- {
		fl.emit(Inst{Op: "pop", D: R(isax86.REAX)})
		fl.emit(Inst{Op: "mov", D: Mem(isax86.REBP, int32(8+4*i)), S: R(isax86.REAX), Sz: 4})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "epi_jmp_r", S: R(isax86.REAX)})
	} else {
		fl.emit(Inst{Op: "epi_jmp_sym", Sym: x.Callee})
	}
	return nil
}