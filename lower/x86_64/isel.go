package x86_64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	isax86_64 "github.com/vertex-language/vvm/isa/x86_64"
)

// lowerFunc lowers one vir.Function to a Func: instruction selection,
// frame building, slot resolution, and final encoding, in that order.
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
		if i >= len(ArgRegs) {
			break // stack params already have homes at [rbp+16+8k]
		}
		fl.emit(Inst{Op: "mov", D: SlotOpr(p.Name), S: R(ArgRegs[i]), Sz: 8})
	}
	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			fl.emit(Inst{Op: "label", Lbl: b.Label})
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
	f       *vir.Function
	types   map[string]vir.Type
	b       []Inst
	nlbl    int
	nasmlbl int
}

func (fl *fnLower) emit(i Inst) { fl.b = append(fl.b, i) }
func (fl *fnLower) mov(d, s Opr) { fl.emit(Inst{Op: "mov", D: d, S: s, Sz: 8}) }
func (fl *fnLower) alu(op string, d, s Opr, sz int) {
	fl.emit(Inst{Op: op, D: d, S: s, Sz: sz})
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
					_, width, ok := Register(arch, bind.Register)
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
func (fl *fnLower) load(o vir.Operand, t vir.Type, r isax86_64.Reg, signed bool) error {
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
		return fmt.Errorf("float operands not lowered on x86_64 (TODO)")
	case vir.OperandIdent:
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
				fl.emit(Inst{Op: "mov", D: R(r), S: SlotOpr(o.Ident), Sz: 8})
			case signed:
				fl.emit(Inst{Op: "movsx", D: R(r), S: SlotOpr(o.Ident), Sz: sz})
			default:
				fl.emit(Inst{Op: "movzx", D: R(r), S: SlotOpr(o.Ident), Sz: sz})
			}
		}
	default:
		return fmt.Errorf("operand kind %d not lowered on x86_64", o.Kind)
	}
	return nil
}

// st writes r (already normalized) into name's 8-byte home slot.
func (fl *fnLower) st(name string, r isax86_64.Reg) {
	fl.emit(Inst{Op: "mov", D: SlotOpr(name), S: R(r), Sz: 8})
}

// norm re-establishes the zero-extended-slot invariant after wrapping or
// sign-extending operations.
func (fl *fnLower) norm(r isax86_64.Reg, t vir.Type) {
	if it, ok := t.(vir.IntType); ok && it.Bits == 1 {
		fl.alu("and", R(r), Imm(1), 4)
		return
	}
	switch szOf(t) {
	case 1, 2:
		fl.emit(Inst{Op: "movzx", D: R(r), S: R(r), Sz: szOf(t)})
	case 4:
		fl.emit(Inst{Op: "movzx", D: R(r), S: R(r), Sz: 4}) // mov r32, r32
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
		r, _, ok := Register(arch, bind.Register)
		if !ok {
			return fmt.Errorf("asm: unknown register %q", bind.Register)
		}
		if err := fl.load(vir.Ident(bind.Ident), fl.types[bind.Ident], r, false); err != nil {
			return err
		}
	}

	prefix := fl.asmLabel()
	insts, err := LowerBlock(dialect, arch, ab.Code, prefix)
	if err != nil {
		return err
	}
	fl.b = append(fl.b, insts...)

	for _, bind := range ab.Bindings {
		if bind.Kind != vir.BindingOut {
			continue
		}
		r, _, ok := Register(arch, bind.Register)
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
	signedCmp := map[string]byte{
		"slt": isax86_64.CondL, "sle": isax86_64.CondLE,
		"sgt": isax86_64.CondG, "sge": isax86_64.CondGE,
	}
	unsignedCmp := map[string]byte{
		"eq": isax86_64.CondE, "ne": isax86_64.CondNE,
		"ult": isax86_64.CondB, "ule": isax86_64.CondBE,
		"ugt": isax86_64.CondA, "uge": isax86_64.CondAE,
	}

	switch {
	case op == "loc":
		return nil

	case op == "mov":
		if err := fl.load(a[0], t, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.st(in.Result, isax86_64.RAX)

	case op == "add" || op == "sub" || op == "and" || op == "or" || op == "xor":
		if err := fl.load(a[0], t, isax86_64.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, false); err != nil {
			return err
		}
		fl.alu(op, R(isax86_64.RAX), R(isax86_64.RCX), opSz(t))
		if op == "add" || op == "sub" {
			fl.norm(isax86_64.RAX, t) // wrap mod 2^N (§4)
		}
		fl.st(in.Result, isax86_64.RAX)

	case op == "mul":
		if err := fl.load(a[0], t, isax86_64.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "imul", D: R(isax86_64.RAX), S: R(isax86_64.RCX), Sz: opSz(t)})
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case op == "neg" || op == "not":
		if err := fl.load(a[0], t, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: op, S: R(isax86_64.RAX), Sz: opSz(t)})
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case op == "abs": // signed; abs(INT_MIN) wraps (§4)
		if err := fl.load(a[0], t, isax86_64.RAX, true); err != nil {
			return err
		}
		w := opSz(t)
		fl.mov(R(isax86_64.RCX), R(isax86_64.RAX))
		fl.emit(Inst{Op: "sar", D: R(isax86_64.RCX), S: Imm(int64(w*8 - 1)), Sz: w})
		fl.alu("xor", R(isax86_64.RAX), R(isax86_64.RCX), w)
		fl.alu("sub", R(isax86_64.RAX), R(isax86_64.RCX), w)
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case op == "udiv" || op == "urem":
		if err := fl.load(a[0], t, isax86_64.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.alu("xor", R(isax86_64.RDX), R(isax86_64.RDX), 4)
		fl.emit(Inst{Op: "div", S: R(isax86_64.RCX), Sz: w}) // zero divisor -> #DE trap (§6.1)
		r := isax86_64.RAX
		if op == "urem" {
			r = isax86_64.RDX
		}
		fl.st(in.Result, r)

	case op == "sdiv" || op == "srem":
		// TODO(§6.1): narrow INT_MIN/-1 (e.g. i8 -128/-1) must trap but the
		// widened 32-bit idiv wraps instead; needs an explicit check for sz<4.
		if err := fl.load(a[0], t, isax86_64.RAX, true); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, true); err != nil {
			return err
		}
		w := opSz(t)
		if w == 8 {
			fl.emit(Inst{Op: "cqo"})
		} else {
			fl.emit(Inst{Op: "cdq"})
		}
		fl.emit(Inst{Op: "idiv", S: R(isax86_64.RCX), Sz: w})
		r := isax86_64.RAX
		if op == "srem" {
			r = isax86_64.RDX
		}
		fl.norm(r, t)
		fl.st(in.Result, r)

	case op == "shl" || op == "lshr" || op == "ashr":
		signedV := op == "ashr"
		if err := fl.load(a[0], t, isax86_64.RAX, signedV); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		if bitsOf(t) < 32 { // count mod N (§4); hardware masks mod 32/64 only
			fl.alu("and", R(isax86_64.RCX), Imm(int64(bitsOf(t)-1)), 4)
		}
		x86op := map[string]string{"shl": "shl", "lshr": "shr", "ashr": "sar"}[op]
		fl.emit(Inst{Op: x86op, D: R(isax86_64.RAX), Sz: w}) // by CL
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case op == "rotl" || op == "rotr":
		if err := fl.load(a[0], t, isax86_64.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, false); err != nil {
			return err
		}
		x86op := "rol"
		if op == "rotr" {
			x86op = "ror"
		}
		fl.emit(Inst{Op: x86op, D: R(isax86_64.RAX), Sz: szOf(t)})
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case op == "smin" || op == "smax" || op == "umin" || op == "umax":
		signed := op[0] == 's'
		if err := fl.load(a[0], t, isax86_64.RAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(isax86_64.RAX), R(isax86_64.RCX), 8)
		cc := map[string]byte{
			"smin": isax86_64.CondG, "smax": isax86_64.CondL,
			"umin": isax86_64.CondA, "umax": isax86_64.CondB,
		}[op]
		fl.emit(Inst{Op: "cmovcc", CC: cc, D: R(isax86_64.RAX), S: R(isax86_64.RCX), Sz: 8})
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case signedCmp[op] != 0 || unsignedCmp[op] != 0 || op == "eq" || op == "ne":
		cc, signed := unsignedCmp[op], false
		if c, ok := signedCmp[op]; ok {
			cc, signed = c, true
		}
		if err := fl.load(a[0], t, isax86_64.RAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, signed); err != nil {
			return err
		}
		fl.alu("cmp", R(isax86_64.RAX), R(isax86_64.RCX), 8)
		fl.emit(Inst{Op: "setcc", CC: cc, D: R(isax86_64.RAX)})
		fl.emit(Inst{Op: "movzx", D: R(isax86_64.RAX), S: R(isax86_64.RAX), Sz: 1})
		fl.st(in.Result, isax86_64.RAX)

	case op == "lt" || op == "gt" || op == "le" || op == "ge":
		return fmt.Errorf("float compares not lowered on x86_64 (TODO)")

	case op == "uaddo" || op == "saddo" || op == "usubo" || op == "ssubo" || op == "umulo" || op == "smulo":
		return fl.selOverflow(in)

	case op == "umulh" || op == "smulh":
		signed := op == "smulh"
		if err := fl.load(a[0], t, isax86_64.RAX, signed); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, signed); err != nil {
			return err
		}
		if szOf(t) == 8 {
			m := "mul1"
			if signed {
				m = "imul1"
			}
			fl.emit(Inst{Op: m, S: R(isax86_64.RCX), Sz: 8})
			fl.st(in.Result, isax86_64.RDX)
		} else {
			fl.emit(Inst{Op: "imul", D: R(isax86_64.RAX), S: R(isax86_64.RCX), Sz: 8})
			sh := "shr"
			if signed {
				sh = "sar"
			}
			fl.emit(Inst{Op: sh, D: R(isax86_64.RAX), S: Imm(int64(bitsOf(t))), Sz: 8})
			fl.norm(isax86_64.RAX, t)
			fl.st(in.Result, isax86_64.RAX)
		}

	case op == "uadd_sat" || op == "sadd_sat" || op == "usub_sat" || op == "ssub_sat":
		return fmt.Errorf("saturating arithmetic not yet lowered on x86_64 (TODO)")

	case op == "ctlz":
		if err := fl.load(a[0], t, isax86_64.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.emit(Inst{Op: "bsr", D: R(isax86_64.RDX), S: R(isax86_64.RCX), Sz: w})
		fl.mov(R(isax86_64.RAX), Imm(-1))
		fl.emit(Inst{Op: "cmovcc", CC: isax86_64.CondNE, D: R(isax86_64.RAX), S: R(isax86_64.RDX), Sz: 8})
		fl.mov(R(isax86_64.RCX), Imm(int64(bitsOf(t)-1)))
		fl.alu("sub", R(isax86_64.RCX), R(isax86_64.RAX), 8)
		fl.st(in.Result, isax86_64.RCX)

	case op == "cttz":
		if err := fl.load(a[0], t, isax86_64.RCX, false); err != nil {
			return err
		}
		w := opSz(t)
		fl.emit(Inst{Op: "bsf", D: R(isax86_64.RDX), S: R(isax86_64.RCX), Sz: w})
		fl.mov(R(isax86_64.RAX), Imm(int64(bitsOf(t))))
		fl.emit(Inst{Op: "cmovcc", CC: isax86_64.CondNE, D: R(isax86_64.RAX), S: R(isax86_64.RDX), Sz: 8})
		fl.st(in.Result, isax86_64.RAX)

	case op == "popcnt":
		// TODO(§10.4): gate on a POPCNT-capable feature tier.
		if err := fl.load(a[0], t, isax86_64.RCX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "popcnt", D: R(isax86_64.RAX), S: R(isax86_64.RCX), Sz: opSz(t)})
		fl.st(in.Result, isax86_64.RAX)

	case op == "bswap":
		if err := fl.load(a[0], t, isax86_64.RAX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 8, 4:
			fl.emit(Inst{Op: "bswap", D: R(isax86_64.RAX), Sz: szOf(t)})
		default: // i16: ror ax, 8 (i8 is rejected by the verifier, §9.20)
			fl.emit(Inst{Op: "ror", D: R(isax86_64.RAX), S: Imm(8), Sz: 2})
			fl.norm(isax86_64.RAX, t)
		}
		fl.st(in.Result, isax86_64.RAX)

	case op == "bitrev":
		return fmt.Errorf("bitrev not yet lowered on x86_64 (SWAR sequence TODO)")

	case op == "select":
		if err := fl.load(a[0], vir.I1, isax86_64.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, isax86_64.RDX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "test", D: R(isax86_64.RAX), S: R(isax86_64.RAX), Sz: 4})
		fl.emit(Inst{Op: "cmovcc", CC: isax86_64.CondE, D: R(isax86_64.RCX), S: R(isax86_64.RDX), Sz: 8})
		fl.st(in.Result, isax86_64.RCX)

	case op == "load" || op == "load_vol" || op == "atomic_load":
		if err := fl.load(a[0], vir.Ptr, isax86_64.RCX, false); err != nil {
			return err
		}
		switch szOf(t) {
		case 8:
			fl.emit(Inst{Op: "mov", D: R(isax86_64.RAX), S: Mem(isax86_64.RCX, 0), Sz: 8})
		case 4:
			fl.emit(Inst{Op: "movzx", D: R(isax86_64.RAX), S: Mem(isax86_64.RCX, 0), Sz: 4})
		default:
			fl.emit(Inst{Op: "movzx", D: R(isax86_64.RAX), S: Mem(isax86_64.RCX, 0), Sz: szOf(t)})
		}
		fl.st(in.Result, isax86_64.RAX)

	case op == "store" || op == "store_vol" || op == "atomic_store":
		if err := fl.load(a[0], vir.Ptr, isax86_64.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mov", D: Mem(isax86_64.RCX, 0), S: R(isax86_64.RAX), Sz: szOf(t)})
		if op == "atomic_store" && lastOrd(a) == "seqcst" {
			fl.emit(Inst{Op: "mfence"})
		}

	case op == "alloca":
		if err := fl.load(a[0], vir.I64, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.alu("add", R(isax86_64.RAX), Imm(15), 8)
		fl.alu("and", R(isax86_64.RAX), Imm(-16), 8)
		fl.alu("sub", R(isax86_64.RSP), R(isax86_64.RAX), 8)
		if in.Align > 16 {
			fl.alu("and", R(isax86_64.RSP), Imm(int64(-in.Align)), 8)
		}
		fl.st(in.Result, isax86_64.RSP)

	case op == "field":
		off, err := fl.lay.FieldOffset(a[1].Ident, a[2].Ident)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, isax86_64.RAX, false); err != nil {
			return err
		}
		if off != 0 {
			fl.alu("add", R(isax86_64.RAX), Imm(int64(off)), 8)
		}
		fl.st(in.Result, isax86_64.RAX)

	case op == "index":
		esz, err := fl.lay.Size(a[1].Type)
		if err != nil {
			return err
		}
		if err := fl.load(a[0], vir.Ptr, isax86_64.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, isax86_64.RCX, true); err != nil { // index is signed (§4)
			return err
		}
		fl.emit(Inst{Op: "imul3", D: R(isax86_64.RCX), S: R(isax86_64.RCX), Imm: int64(esz), Sz: 8})
		fl.alu("add", R(isax86_64.RAX), R(isax86_64.RCX), 8)
		fl.st(in.Result, isax86_64.RAX)

	case op == "memcopy" || op == "memset":
		if err := fl.load(a[0], vir.Ptr, isax86_64.RDI, false); err != nil {
			return err
		}
		if op == "memcopy" {
			if err := fl.load(a[1], vir.Ptr, isax86_64.RSI, false); err != nil {
				return err
			}
		} else {
			if err := fl.load(a[1], vir.I8, isax86_64.RAX, false); err != nil {
				return err
			}
		}
		if err := fl.load(a[2], vir.I64, isax86_64.RCX, false); err != nil {
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
		if err := fl.load(a[0], vir.Ptr, isax86_64.RDI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], vir.Ptr, isax86_64.RSI, false); err != nil {
			return err
		}
		if err := fl.load(a[2], vir.I64, isax86_64.RCX, false); err != nil {
			return err
		}
		fl.alu("cmp", R(isax86_64.RSI), R(isax86_64.RDI), 8)
		fl.emit(Inst{Op: "jcc", CC: isax86_64.CondAE, Lbl: fwd})
		fl.alu("add", R(isax86_64.RSI), R(isax86_64.RCX), 8)
		fl.alu("sub", R(isax86_64.RSI), Imm(1), 8)
		fl.alu("add", R(isax86_64.RDI), R(isax86_64.RCX), 8)
		fl.alu("sub", R(isax86_64.RDI), Imm(1), 8)
		fl.emit(Inst{Op: "std"})
		fl.emit(Inst{Op: "rep_movsb"})
		fl.emit(Inst{Op: "cld"})
		fl.emit(Inst{Op: "jmp", Lbl: done})
		fl.emit(Inst{Op: "label", Lbl: fwd})
		fl.emit(Inst{Op: "cld"})
		fl.emit(Inst{Op: "rep_movsb"})
		fl.emit(Inst{Op: "label", Lbl: done})

	case op == "prefetch":
		return nil // advisory (§4); dropped in this bring-up

	case op == "fence":
		if lastOrd(a) == "seqcst" {
			fl.emit(Inst{Op: "mfence"})
		}
		return nil // acquire/release/acqrel fences are compiler-only on x86 TSO

	case op == "atomic_add" || op == "atomic_sub" || op == "atomic_xchg":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86_64 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, isax86_64.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RAX, false); err != nil {
			return err
		}
		switch op {
		case "atomic_sub":
			fl.emit(Inst{Op: "neg", S: R(isax86_64.RAX), Sz: w})
			fallthrough
		case "atomic_add":
			fl.emit(Inst{Op: "lock_xadd", D: Mem(isax86_64.RCX, 0), S: R(isax86_64.RAX), Sz: w})
		case "atomic_xchg":
			fl.emit(Inst{Op: "xchg", D: Mem(isax86_64.RCX, 0), S: R(isax86_64.RAX), Sz: w})
		}
		fl.st(in.Result, isax86_64.RAX)

	case op == "atomic_and" || op == "atomic_or" || op == "atomic_xor":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("%s narrower than 32 bits not yet lowered on x86_64 (TODO)", op)
		}
		if err := fl.load(a[0], vir.Ptr, isax86_64.RSI, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RDX, false); err != nil {
			return err
		}
		loop := fl.label()
		fl.emit(Inst{Op: "mov", D: R(isax86_64.RAX), S: Mem(isax86_64.RSI, 0), Sz: w})
		fl.emit(Inst{Op: "label", Lbl: loop})
		fl.mov(R(isax86_64.RCX), R(isax86_64.RAX))
		fl.alu(op[len("atomic_"):], R(isax86_64.RCX), R(isax86_64.RDX), w)
		fl.emit(Inst{Op: "lock_cmpxchg", D: Mem(isax86_64.RSI, 0), S: R(isax86_64.RCX), Sz: w})
		fl.emit(Inst{Op: "jcc", CC: isax86_64.CondNE, Lbl: loop})
		fl.st(in.Result, isax86_64.RAX)

	case op == "cmpxchg":
		w := szOf(t)
		if w != 4 && w != 8 {
			return fmt.Errorf("cmpxchg narrower than 32 bits not yet lowered on x86_64 (i128 needs cmpxchg16b tier, TODO)")
		}
		if err := fl.load(a[0], vir.Ptr, isax86_64.RCX, false); err != nil {
			return err
		}
		if err := fl.load(a[1], t, isax86_64.RAX, false); err != nil {
			return err
		}
		if err := fl.load(a[2], t, isax86_64.RDX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "lock_cmpxchg", D: Mem(isax86_64.RCX, 0), S: R(isax86_64.RDX), Sz: w})
		fl.st(in.Result, isax86_64.RAX)

	case op == "syscall":
		os := ""
		if fl.m.Target != nil {
			os = fl.m.Target.OS
		}
		conv, ok := LookupSyscall(os)
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
		fl.emit(Inst{Op: conv.Trap})
		if !vir.IsVoid(t) && in.Result != "" {
			fl.norm(conv.Result, t)
			fl.st(in.Result, conv.Result)
		}

	case op == "trunc":
		if err := fl.load(a[0], nil, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case op == "zext":
		if err := fl.load(a[0], nil, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.st(in.Result, isax86_64.RAX) // slots are already zero-extended

	case op == "sext":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if it, ok := st.(vir.IntType); ok && it.Bits == 1 {
			if err := fl.load(a[0], st, isax86_64.RAX, false); err != nil {
				return err
			}
			fl.emit(Inst{Op: "neg", S: R(isax86_64.RAX), Sz: 8}) // i1 sext: 1 -> -1
		} else {
			if err := fl.load(a[0], st, isax86_64.RAX, true); err != nil {
				return err
			}
		}
		fl.norm(isax86_64.RAX, t)
		fl.st(in.Result, isax86_64.RAX)

	case op == "bitcast":
		st, err := fl.typeOfOperand(a[0])
		if err != nil {
			return err
		}
		if vir.IsFloat(st) || vir.IsFloat(t) {
			return fmt.Errorf("float bitcast not lowered on x86_64 (TODO)")
		}
		if err := fl.load(a[0], st, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.st(in.Result, isax86_64.RAX)

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
	if err := fl.load(a[0], t, isax86_64.RAX, signed); err != nil {
		return err
	}
	if err := fl.load(a[1], t, isax86_64.RCX, signed); err != nil {
		return err
	}
	w := szOf(t)
	if w == 4 || w == 8 {
		var cc byte
		switch in.Op {
		case "uaddo", "usubo":
			cc = isax86_64.CondB
		default:
			cc = isax86_64.CondO
		}
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(isax86_64.RAX), R(isax86_64.RCX), w)
		case "usubo", "ssubo":
			fl.alu("sub", R(isax86_64.RAX), R(isax86_64.RCX), w)
		case "umulo":
			fl.emit(Inst{Op: "mul1", S: R(isax86_64.RCX), Sz: w})
		case "smulo":
			fl.emit(Inst{Op: "imul1", S: R(isax86_64.RCX), Sz: w})
		}
		fl.emit(Inst{Op: "setcc", CC: cc, D: R(isax86_64.RAX)})
	} else {
		switch in.Op {
		case "uaddo", "saddo":
			fl.alu("add", R(isax86_64.RAX), R(isax86_64.RCX), 8)
		case "usubo", "ssubo":
			fl.alu("sub", R(isax86_64.RAX), R(isax86_64.RCX), 8)
		case "umulo", "smulo":
			fl.emit(Inst{Op: "imul", D: R(isax86_64.RAX), S: R(isax86_64.RCX), Sz: 8})
		}
		ext := "movzx"
		if signed {
			ext = "movsx"
		}
		fl.emit(Inst{Op: ext, D: R(isax86_64.RCX), S: R(isax86_64.RAX), Sz: w})
		fl.alu("cmp", R(isax86_64.RCX), R(isax86_64.RAX), 8)
		fl.emit(Inst{Op: "setcc", CC: isax86_64.CondNE, D: R(isax86_64.RAX)})
	}
	fl.emit(Inst{Op: "movzx", D: R(isax86_64.RAX), S: R(isax86_64.RAX), Sz: 1})
	fl.st(in.Result, isax86_64.RAX)
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

	plan := PlanCall(len(args))
	if plan.StackBytes > 0 {
		fl.alu("sub", R(isax86_64.RSP), Imm(int64(plan.StackBytes)), 8)
	}
	for i, a := range args {
		if err := fl.load(a, nil, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "mov", D: Mem(isax86_64.RSP, plan.StageOffset(i)), S: R(isax86_64.RAX), Sz: 8})
	}
	if indirect {
		if err := fl.load(in.Args[0], vir.Ptr, isax86_64.R10, false); err != nil {
			return err
		}
	}
	for i := 0; i < plan.NumRegArgs; i++ {
		fl.emit(Inst{Op: "mov", D: R(ArgRegs[i]), S: Mem(isax86_64.RSP, plan.StageOffset(i)), Sz: 8})
	}
	if variadic {
		fl.alu("xor", R(isax86_64.RAX), R(isax86_64.RAX), 4)
	}
	if indirect {
		fl.emit(Inst{Op: "call_r", S: R(isax86_64.R10)})
	} else {
		fl.emit(Inst{Op: "call_sym", Sym: in.Args[0].Ident})
	}
	if plan.StackBytes > 0 {
		fl.alu("add", R(isax86_64.RSP), Imm(int64(plan.StackBytes)), 8)
	}
	if !vir.IsVoid(ret) && in.Result != "" {
		fl.norm(isax86_64.RAX, ret)
		fl.st(in.Result, isax86_64.RAX)
	}
	return nil
}

func (fl *fnLower) selTerm(t vir.Terminator) error {
	switch x := t.(type) {
	case vir.Branch:
		fl.emit(Inst{Op: "jmp", Lbl: x.Label})
	case vir.BranchIf:
		if err := fl.load(x.Cond, vir.I1, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "test", D: R(isax86_64.RAX), S: R(isax86_64.RAX), Sz: 4})
		fl.emit(Inst{Op: "jcc", CC: isax86_64.CondNE, Lbl: x.Then})
		fl.emit(Inst{Op: "jmp", Lbl: x.Else})
	case vir.Switch:
		vt, err := fl.typeOfOperand(x.Value)
		if err != nil {
			vt = vir.I64
		}
		if err := fl.load(x.Value, vt, isax86_64.RAX, false); err != nil {
			return err
		}
		for _, c := range x.Cases {
			fl.mov(R(isax86_64.RCX), Imm(litBits(c.Value, vt, false)))
			fl.alu("cmp", R(isax86_64.RAX), R(isax86_64.RCX), 8)
			fl.emit(Inst{Op: "jcc", CC: isax86_64.CondE, Lbl: c.Label})
		}
		fl.emit(Inst{Op: "jmp", Lbl: x.Default})
	case vir.Return:
		if x.Value != nil {
			if err := fl.load(*x.Value, fl.f.Ret, isax86_64.RAX, false); err != nil {
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
	needStack := len(args) - len(ArgRegs)
	if needStack < 0 {
		needStack = 0
	}
	haveStack := len(fl.f.Params) - len(ArgRegs)
	if haveStack < 0 {
		haveStack = 0
	}
	if needStack > haveStack {
		return fmt.Errorf("tailcall with %d stack-arg slots exceeds caller's %d incoming slots (frame-growing tailcalls TODO)", needStack, haveStack)
	}
	for _, a := range args {
		if err := fl.load(a, nil, isax86_64.RAX, false); err != nil {
			return err
		}
		fl.emit(Inst{Op: "push", S: R(isax86_64.RAX)})
	}
	if indirect {
		if err := fl.load(x.Args[0], vir.Ptr, isax86_64.R10, false); err != nil {
			return err
		}
	}
	for i := len(args) - 1; i >= 0; i-- {
		if i < len(ArgRegs) {
			fl.emit(Inst{Op: "pop", D: R(ArgRegs[i])})
		} else {
			fl.emit(Inst{Op: "pop", D: R(isax86_64.RAX)})
			fl.emit(Inst{Op: "mov", D: Mem(isax86_64.RBP, int32(16+8*(i-len(ArgRegs)))), S: R(isax86_64.RAX), Sz: 8})
		}
	}
	if variadic {
		fl.alu("xor", R(isax86_64.RAX), R(isax86_64.RAX), 4)
	}
	if indirect {
		fl.emit(Inst{Op: "epi_jmp_r", S: R(isax86_64.R10)})
	} else {
		fl.emit(Inst{Op: "epi_jmp_sym", Sym: x.Callee})
	}
	return nil
}