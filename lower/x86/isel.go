package x86

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	isax86 "github.com/vertex-language/vvm/isa/x86"
)

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

func (fl *fnLower) emit(i Inst)             { fl.b = append(fl.b, i) }
func (fl *fnLower) mov(d, s Opr)            { fl.emit(Inst{Op: "mov", D: d, S: s, Sz: 4}) }
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
			if in.Op == vir.OpLoc || in.Result == "" {
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

var voidOps = map[vir.Opcode]bool{
	vir.OpStore:       true,
	vir.OpStoreVol:    true,
	vir.OpAtomicStore: true,
	vir.OpMemcopy:     true,
	vir.OpMemmove:     true,
	vir.OpMemset:      true,
	vir.OpFence:       true,
	vir.OpPrefetch:    true,
	vir.OpMaskedStore: true,
	vir.OpScatter:     true,
}

var cmpOps = map[vir.Opcode]bool{
	vir.OpEq:     true,
	vir.OpNe:     true,
	vir.OpSlt:    true,
	vir.OpSgt:    true,
	vir.OpSle:    true,
	vir.OpSge:    true,
	vir.OpUlt:    true,
	vir.OpUgt:    true,
	vir.OpUle:    true,
	vir.OpUge:    true,
	vir.OpLt:     true,
	vir.OpGt:     true,
	vir.OpLe:     true,
	vir.OpGe:     true,
	vir.OpUAddO:  true,
	vir.OpSAddO:  true,
	vir.OpUSubO:  true,
	vir.OpSSubO:  true,
	vir.OpUMulO:  true,
	vir.OpSMulO:  true,
}

func (fl *fnLower) resultType(in *vir.Instruction) (vir.Type, error) {
	switch {
	case voidOps[in.Op]:
		return vir.Void, nil
	case cmpOps[in.Op]:
		return vir.I1, nil
	case in.Op == vir.OpCall:
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
	case in.Op == vir.OpSyscall:
		if in.Suffix == nil {
			return nil, fmt.Errorf("syscall: missing return type suffix")
		}
		return in.Suffix, nil
	case in.Suffix != nil:
		return in.Suffix, nil
	}
	return nil, fmt.Errorf("op %s has no result type", in.Op)
}

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

func (fl *fnLower) selSyscall(in *vir.Instruction) error {
	if fl.m.Target == nil {
		return fmt.Errorf("syscall: module has no target declaration")
	}
	osName := fl.m.Target.OS
	conv, ok := syscallConventionFor(osName)
	if !ok {
		if osName == "none" || osName == "uefi" {
			fl.emit(Inst{Op: "ud2"})
			return nil
		}
		return fmt.Errorf("syscall: no lowering convention for target os %q", osName)
	}

	args := in.Args
	var stackVals []vir.Operand
	regVals := map[int]vir.Operand{}
	for i, a := range args {
		if _, ok := conv.RegisterFor(i); ok {
			regVals[i] = a
		} else {
			stackVals = append(stackVals, a)
		}
	}

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

func (fl *fnLower) selInst(in *vir.Instruction) error {
	op, t, a := in.Op, in.Suffix, in.Args

	signedCmp := map[vir.Opcode]byte{
		vir.OpSlt: isax86.CondL,
		vir.OpSle: isax86.CondLE,
		vir.OpSgt: isax86.CondG,
		vir.OpSge: isax86.CondGE,
	}
	unsignedCmp := map[vir.Opcode]byte{
		vir.OpEq:  isax86.CondE,
		vir.OpNe:  isax86.CondNE,
		vir.OpUlt: isax86.CondB,
		vir.OpUle: isax86.CondBE,
		vir.OpUgt: isax86.CondA,
		vir.OpUge: isax86.CondAE,
	}

	switch {
	case op == vir.OpLoc:
		return nil

	case op == vir.OpAdd || op == vir.OpSub || op == vir.OpAnd || op == vir.OpOr || op == vir.OpXor:
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		x86op := map[vir.Opcode]string{
			vir.OpAdd: "add", vir.OpSub: "sub",
			vir.OpAnd: "and", vir.OpOr: "or", vir.OpXor: "xor",
		}[op]
		fl.alu(x86op, R(isax86.REAX), R(isax86.RECX))
		if op == vir.OpAdd || op == vir.OpSub {
			fl.norm(isax86.REAX, t)
		}
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpMul:
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "imul", D: R(isax86.REAX), S: R(isax86.RECX)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpNeg || op == vir.OpNot:
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		x86op := "neg"
		if op == vir.OpNot {
			x86op = "not"
		}
		fl.emit(Inst{Op: x86op, S: R(isax86.REAX)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpAbs:
		if err := fl.load(a[0], t, isax86.REAX, true); err != nil {
			return err
		}
		fl.mov(R(isax86.RECX), R(isax86.REAX))
		fl.emit(Inst{Op: "sar", D: R(isax86.RECX), S: Imm(31), Sz: 4})
		fl.alu("xor", R(isax86.REAX), R(isax86.RECX))
		fl.alu("sub", R(isax86.REAX), R(isax86.RECX))
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpUDiv || op == vir.OpURem:
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.alu("xor", R(isax86.REDX), R(isax86.REDX))
		fl.emit(Inst{Op: "div", S: R(isax86.RECX)})
		r := isax86.REAX
		if op == vir.OpURem {
			r = isax86.REDX
		}
		fl.st(in.Result, r)

	case op == vir.OpSDiv || op == vir.OpSRem:
		if err := fl.load(a[0], t, isax86.REAX, true); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, true); err != nil {
			return err
		}
		fl.emit(Inst{Op: "cdq"})
		fl.emit(Inst{Op: "idiv", S: R(isax86.RECX)})
		r := isax86.REAX
		if op == vir.OpSRem {
			r = isax86.REDX
		}
		fl.norm(r, t)
		fl.st(in.Result, r)

	case op == vir.OpShl || op == vir.OpLShr || op == vir.OpAShr:
		signedV := op == vir.OpAShr
		if err := fl.load(a[0], t, isax86.REAX, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		if bitsOf(t) < 32 {
			fl.alu("and", R(isax86.RECX), Imm(int64(bitsOf(t)-1)))
		}
		x86op := map[vir.Opcode]string{vir.OpShl: "shl", vir.OpLShr: "shr", vir.OpAShr: "sar"}[op]
		fl.emit(Inst{Op: x86op, D: R(isax86.REAX), Sz: 4})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpRotl || op == vir.OpRotr:
		if err := fl.load(a[0], t, isax86.REAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, false); err != nil {
			return err
		}
		x86op := "rol"
		if op == vir.OpRotr {
			x86op = "ror"
		}
		fl.emit(Inst{Op: x86op, D: R(isax86.REAX), Sz: szOf(t)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpSMin || op == vir.OpSMax || op == vir.OpUMin || op == vir.OpUMax:
		signed := op == vir.OpSMin || op == vir.OpSMax
		if err := fl.load(a[0], t, isax86.REAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.RECX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(isax86.REAX), R(isax86.RECX))
		cc := map[vir.Opcode]byte{
			vir.OpSMin: isax86.CondG, vir.OpSMax: isax86.CondL,
			vir.OpUMin: isax86.CondA, vir.OpUMax: isax86.CondB,
		}[op]
		fl.emit(Inst{Op: "cmovcc", CC: cc, D: R(isax86.REAX), S: R(isax86.RECX)})
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0:
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

	case op == vir.OpLt || op == vir.OpGt || op == vir.OpLe || op == vir.OpGe:
		return fmt.Errorf("float compares not lowered on x86 (TODO)")

	case op == vir.OpUAddO || op == vir.OpSAddO || op == vir.OpUSubO || op == vir.OpSSubO || op == vir.OpUMulO || op == vir.OpSMulO:
		return fl.selOverflow(in)

	case op == vir.OpUMulH || op == vir.OpSMulH:
		signed := op == vir.OpSMulH
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

	case op == vir.OpUAddSat || op == vir.OpSAddSat || op == vir.OpUSubSat || op == vir.OpSSubSat:
		return fmt.Errorf("saturating arithmetic not yet lowered on x86 (TODO)")

	case op == vir.OpCtlz:
		if err := fl.load(a[0], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "bsr", D: R(isax86.REDX), S: R(isax86.RECX)})
		fl.mov(R(isax86.REAX), Imm(-1))
		fl.emit(Inst{Op: "cmovcc", CC: isax86.CondNE, D: R(isax86.REAX), S: R(isax86.REDX)})
		fl.mov(R(isax86.RECX), Imm(int64(bitsOf(t)-1)))
		fl.alu("sub", R(isax86.RECX), R(isax86.REAX))
		fl.st(in.Result, isax86.RECX)

	case op == vir.OpCttz:
		if err := fl.load(a[0], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "bsf", D: R(isax86.REDX), S: R(isax86.RECX)})
		fl.mov(R(isax86.REAX), Imm(int64(bitsOf(t))))
		fl.emit(Inst{Op: "cmovcc", CC: isax86.CondNE, D: R(isax86.REAX), S: R(isax86.REDX)})
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpPopcnt:
		if err := fl.load(a[0], t, isax86.RECX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "popcnt", D: R(isax86.REAX), S: R(isax86.RECX)})
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpBSwap:
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

	case op == vir.OpBitrev:
		return fmt.Errorf("bitrev not yet lowered on x86 (SWAR sequence TODO)")

	case op == vir.OpSelect:
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

	case op == vir.OpLoad || op == vir.OpLoadVol || op == vir.OpAtomicLoad:
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

	case op == vir.OpStore || op == vir.OpStoreVol || op == vir.OpAtomicStore:
		if err := fl.load(a[0], vir.Ptr, isax86.RECX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86.REAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mov", D: Mem(isax86.RECX, 0), S: R(isax86.REAX), Sz: szOf(t)})
		if op == vir.OpAtomicStore && lastOrd(a) == "seqcst" {
			fl.emit(Inst{Op: "mfence"})
		}

	case op == vir.OpAlloca:
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

	case op == vir.OpField:
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

	case op == vir.OpIndex:
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

	case op == vir.OpMemcopy || op == vir.OpMemset:
		if err := fl.load(a[0], vir.Ptr, isax86.REDI, false); err != nil {
			return err
		}
		if op == vir.OpMemcopy {
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
		if op == vir.OpMemcopy {
			fl.emit(Inst{Op: "rep_movsb"})
		} else {
			fl.emit(Inst{Op: "rep_stosb"})
		}

	case op == vir.OpMemmove:
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

	case op == vir.OpPrefetch:
		return nil

	case op == vir.OpFence:
		if lastOrd(a) == "seqcst" {
			fl.emit(Inst{Op: "mfence"})
		}
		return nil

	case op == vir.OpAtomicAdd || op == vir.OpAtomicSub || op == vir.OpAtomicXchg:
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
		case vir.OpAtomicSub:
			fl.emit(Inst{Op: "neg", S: R(isax86.REAX)})
			fallthrough
		case vir.OpAtomicAdd:
			fl.emit(Inst{Op: "lock_xadd", D: Mem(isax86.RECX, 0), S: R(isax86.REAX)})
		case vir.OpAtomicXchg:
			fl.emit(Inst{Op: "xchg", D: Mem(isax86.RECX, 0), S: R(isax86.REAX)})
		}
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpAtomicAnd || op == vir.OpAtomicOr || op == vir.OpAtomicXor:
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
		aluOp := map[vir.Opcode]string{vir.OpAtomicAnd: "and", vir.OpAtomicOr: "or", vir.OpAtomicXor: "xor"}[op]
		fl.alu(aluOp, R(isax86.RECX), R(isax86.REDX))
		fl.emit(Inst{Op: "lock_cmpxchg", D: Mem(isax86.RESI, 0), S: R(isax86.RECX)})
		fl.emit(Inst{Op: "jcc", CC: isax86.CondNE, Lbl: loop})
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpCmpxchg:
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

	case op == vir.OpTrunc:
		if err := fl.load(a[0], nil, isax86.REAX, false); err != nil {
			return err
		}
		fl.norm(isax86.REAX, t)
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpZext:
		if err := fl.load(a[0], nil, isax86.REAX, false); err != nil {
			return err
		}
		fl.st(in.Result, isax86.REAX)

	case op == vir.OpSext:
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

	case op == vir.OpBitcast:
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

	case op == vir.OpCall:
		return fl.selCall(in)

	case op == vir.OpSyscall:
		return fl.selSyscall(in)

	case op == vir.OpFdemote || op == vir.OpFpromote || op == vir.OpSfromint || op == vir.OpUfromint ||
		op == vir.OpStoint || op == vir.OpUtoint || op == vir.OpStointSat || op == vir.OpUtointSat ||
		op == vir.OpSqrt || op == vir.OpFma || op == vir.OpCopysign || op == vir.OpFloor || op == vir.OpCeil ||
		op == vir.OpTruncF || op == vir.OpNearest || op == vir.OpMin || op == vir.OpMax:
		return fmt.Errorf("floating-point op %s not lowered on x86 (x87/SSE tier TODO)", op)

	case op == vir.OpSplat || op == vir.OpExtract || op == vir.OpInsert || op == vir.OpShuffle ||
		op == vir.OpMaskedLoad || op == vir.OpMaskedStore || op == vir.OpGather || op == vir.OpScatter ||
		op == vir.OpReduceAdd || op == vir.OpReduceMin || op == vir.OpReduceMax ||
		op == vir.OpReduceAnd || op == vir.OpReduceOr || op == vir.OpReduceXor:
		return fmt.Errorf("vector op %s not lowered on x86 (tier TODO, §10.4)", op)

	default:
		return fmt.Errorf("op %s not lowered on x86", op)
	}
	return nil
}

func (fl *fnLower) selOverflow(in *vir.Instruction) error {
	t, a := in.Suffix, in.Args
	signed := in.Op == vir.OpSAddO || in.Op == vir.OpSSubO || in.Op == vir.OpSMulO
	if err := fl.load(a[0], t, isax86.REAX, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, isax86.RECX, signed); err != nil {
		return err
	}
	if szOf(t) == 4 {
		var cc byte
		switch in.Op {
		case vir.OpUAddO, vir.OpUSubO:
			cc = isax86.CondB
		case vir.OpSAddO, vir.OpSSubO, vir.OpSMulO, vir.OpUMulO:
			cc = isax86.CondO
		}
		switch in.Op {
		case vir.OpUAddO, vir.OpSAddO:
			fl.alu("add", R(isax86.REAX), R(isax86.RECX))
		case vir.OpUSubO, vir.OpSSubO:
			fl.alu("sub", R(isax86.REAX), R(isax86.RECX))
		case vir.OpUMulO:
			fl.emit(Inst{Op: "mul32", S: R(isax86.RECX)})
		case vir.OpSMulO:
			fl.emit(Inst{Op: "imul32", S: R(isax86.RECX)})
		}
		fl.emit(Inst{Op: "setcc", CC: cc, D: R(isax86.REAX)})
	} else {
		switch in.Op {
		case vir.OpUAddO, vir.OpSAddO:
			fl.alu("add", R(isax86.REAX), R(isax86.RECX))
		case vir.OpUSubO, vir.OpSSubO:
			fl.alu("sub", R(isax86.REAX), R(isax86.RECX))
		case vir.OpUMulO, vir.OpSMulO:
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