package ptx

// Emit methods build one Instr, append it to the body, and return it. The
// returned pointer is the handle for predication (If, IfNot), for comments,
// and for any pass that wants to rewrite the instruction later.
//
// Type specifiers are positional because they are mandatory. Everything
// else is a variadic Qual and may be supplied in any order.

func (b *Body) inst(op Op, t []Type, dst, src []Operand, qs []Qual) *Instr {
	in := &Instr{Op: op, Q: buildQuals(qs), Dst: dst, Src: src}
	for i, x := range t {
		if i < len(in.Types) {
			in.Types[i] = x
		}
	}
	// The vector width is stated by operand arity, never by the caller.
	for _, o := range append(append([]Operand{}, dst...), src...) {
		if v, ok := o.(VecOp); ok {
			in.Q.Vec = len(v)
		}
	}
	return b.add(in)
}

// Emit appends an instruction whose base mnemonic this package does not
// model. Emitted instructions participate in predication, walking and
// printing exactly like modelled ones; qualifiers are appended in the
// generic canonical order. Anything needing a different order belongs in the
// mnemonic string.
func (b *Body) Emit(mnemonic string, dst, src []Operand, qs ...Qual) *Instr {
	in := &Instr{Op: OpCustom, custom: mnemonic, Q: buildQuals(qs), Dst: dst, Src: src}
	return b.add(in)
}

// ---- Integer and floating-point arithmetic --------------------------------

func (b *Body) Add(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpAdd, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Sub(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpSub, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Mul(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpMul, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Mad(t Type, d Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpMad, []Type{t}, []Operand{d}, []Operand{a, c, e}, q)
}
func (b *Body) Fma(t Type, d Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpFma, []Type{t}, []Operand{d}, []Operand{a, c, e}, q)
}
func (b *Body) Div(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpDiv, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Rem(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpRem, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Abs(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpAbs, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Neg(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpNeg, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Min(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpMin, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Max(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpMax, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Sqrt(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpSqrt, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Rcp(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpRcp, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Rsqrt(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpRsqrt, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Sin(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpSin, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Cos(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpCos, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Ex2(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpEx2, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Lg2(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpLg2, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Tanh(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpTanh, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Testp(t Type, p Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpTestp, []Type{t}, []Operand{p}, []Operand{a}, q)
}
func (b *Body) Copysign(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpCopysign, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) Sad(t Type, d Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpSad, []Type{t}, []Operand{d}, []Operand{a, c, e}, q)
}

// ---- Extended-precision integer arithmetic --------------------------------

func (b *Body) AddCC(t Type, d Reg, a, c Operand) *Instr {
	return b.inst(OpAddCC, []Type{t}, []Operand{d}, []Operand{a, c}, nil)
}
func (b *Body) AddC(t Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpAddC, []Type{t}, []Operand{d}, []Operand{a, c}, q)
}
func (b *Body) SubCC(t Type, d Reg, a, c Operand) *Instr {
	return b.inst(OpSubCC, []Type{t}, []Operand{d}, []Operand{a, c}, nil)
}
func (b *Body) SubC(t Type, d Reg, a, c Operand) *Instr {
	return b.inst(OpSubC, []Type{t}, []Operand{d}, []Operand{a, c}, nil)
}
func (b *Body) MadCC(t Type, d Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpMadCC, []Type{t}, []Operand{d}, []Operand{a, c, e}, q)
}
func (b *Body) MadC(t Type, d Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpMadC, []Type{t}, []Operand{d}, []Operand{a, c, e}, q)
}

// ---- Bit manipulation -----------------------------------------------------

func (b *Body) Popc(t Type, d Reg, a Operand) *Instr {
	return b.inst(OpPopc, []Type{t}, []Operand{d}, []Operand{a}, nil)
}
func (b *Body) Clz(t Type, d Reg, a Operand) *Instr {
	return b.inst(OpClz, []Type{t}, []Operand{d}, []Operand{a}, nil)
}
func (b *Body) Brev(t Type, d Reg, a Operand) *Instr {
	return b.inst(OpBrev, []Type{t}, []Operand{d}, []Operand{a}, nil)
}
func (b *Body) Bfind(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpBfind, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) Fns(d Reg, mask, base, off Operand) *Instr {
	return b.inst(OpFns, []Type{B32}, []Operand{d}, []Operand{mask, base, off}, nil)
}
func (b *Body) Bfe(t Type, d Reg, a, pos, len Operand) *Instr {
	return b.inst(OpBfe, []Type{t}, []Operand{d}, []Operand{a, pos, len}, nil)
}
func (b *Body) Bfi(t Type, f Reg, a, c, pos, len Operand) *Instr {
	return b.inst(OpBfi, []Type{t}, []Operand{f}, []Operand{a, c, pos, len}, nil)
}
func (b *Body) Szext(t Type, d Reg, a, n Operand, q ...Qual) *Instr {
	return b.inst(OpSzext, []Type{t}, []Operand{d}, []Operand{a, n}, q)
}
func (b *Body) Bmsk(t Type, d Reg, a, n Operand, q ...Qual) *Instr {
	return b.inst(OpBmsk, []Type{t}, []Operand{d}, []Operand{a, n}, q)
}
func (b *Body) Dp4a(at, bt Type, d Reg, a, c, e Operand) *Instr {
	return b.inst(OpDp4a, []Type{at, bt}, []Operand{d}, []Operand{a, c, e}, nil)
}
func (b *Body) Dp2a(at, bt Type, d Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpDp2a, []Type{at, bt}, []Operand{d}, []Operand{a, c, e}, q)
}
func (b *Body) Prmt(t Type, d Reg, a, c, sel Operand) *Instr {
	return b.inst(OpPrmt, []Type{t}, []Operand{d}, []Operand{a, c, sel}, nil)
}
func (b *Body) Lop3(t Type, d Reg, a, c, e, lut Operand) *Instr {
	return b.inst(OpLop3, []Type{t}, []Operand{d}, []Operand{a, c, e, lut}, nil)
}

// ---- Comparison and selection ---------------------------------------------

func (b *Body) Setp(t Type, cmp Cmp, p Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpSetp, []Type{t}, []Operand{p}, []Operand{a, c}, append(q, cmp))
}

// SetpBool emits the three-operand form, setp.cmpop.boolop, writing both
// destination predicates.
func (b *Body) SetpBool(t Type, cmp Cmp, op BoolOp, p, s Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpSetp, []Type{t}, []Operand{Or(p, s)}, []Operand{a, c, e},
		append(q, cmp, op))
}

func (b *Body) Set(dt, st Type, cmp Cmp, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpSet, []Type{dt, st}, []Operand{d}, []Operand{a, c}, append(q, cmp))
}
func (b *Body) Selp(t Type, d Reg, a, c Operand, p Reg, q ...Qual) *Instr {
	return b.inst(OpSelp, []Type{t}, []Operand{d}, []Operand{a, c, p}, q)
}

// Slct selects on the sign of e. ct must be S32 or F32.
func (b *Body) Slct(dt, ct Type, d Reg, a, c, e Operand, q ...Qual) *Instr {
	return b.inst(OpSlct, []Type{dt, ct}, []Operand{d}, []Operand{a, c, e}, q)
}

// ---- Logic and shift ------------------------------------------------------

func (b *Body) And(t Type, d Reg, a, c Operand) *Instr {
	return b.inst(OpAnd, []Type{t}, []Operand{d}, []Operand{a, c}, nil)
}
func (b *Body) Or(t Type, d Reg, a, c Operand) *Instr {
	return b.inst(OpOr, []Type{t}, []Operand{d}, []Operand{a, c}, nil)
}
func (b *Body) Xor(t Type, d Reg, a, c Operand) *Instr {
	return b.inst(OpXor, []Type{t}, []Operand{d}, []Operand{a, c}, nil)
}
func (b *Body) Not(t Type, d Reg, a Operand) *Instr {
	return b.inst(OpNot, []Type{t}, []Operand{d}, []Operand{a}, nil)
}
func (b *Body) Cnot(t Type, d Reg, a Operand) *Instr {
	return b.inst(OpCnot, []Type{t}, []Operand{d}, []Operand{a}, nil)
}
func (b *Body) Shl(t Type, d Reg, a, n Operand) *Instr {
	return b.inst(OpShl, []Type{t}, []Operand{d}, []Operand{a, n}, nil)
}
func (b *Body) Shr(t Type, d Reg, a, n Operand) *Instr {
	return b.inst(OpShr, []Type{t}, []Operand{d}, []Operand{a, n}, nil)
}

// Shf is a funnel shift; supply DirL or DirR and Clamp or Wrap.
func (b *Body) Shf(t Type, d Reg, lo, hi, n Operand, q ...Qual) *Instr {
	return b.inst(OpShf, []Type{t}, []Operand{d}, []Operand{lo, hi, n}, q)
}

// ---- Data movement and conversion -----------------------------------------

func (b *Body) Mov(t Type, d Reg, a Operand) *Instr {
	return b.inst(OpMov, []Type{t}, []Operand{d}, []Operand{a}, nil)
}

// MovSReg moves a special register into d using the register's own type.
func (b *Body) MovSReg(d Reg, s SReg) *Instr {
	return b.inst(OpMov, []Type{s.Type()}, []Operand{d}, []Operand{s}, nil)
}

func (b *Body) Cvt(dt, st Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpCvt, []Type{dt, st}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) CvtPack(dt, st Type, d Reg, a, c Operand, q ...Qual) *Instr {
	return b.inst(OpCvtPack, []Type{dt, st}, []Operand{d}, []Operand{a, c}, q)
}

// Cvta converts a state-space address to a generic address; add the To flag
// to convert in the opposite direction. t is U32 or U64 and must match the
// module's address size.
func (b *Body) Cvta(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpCvta, []Type{t}, []Operand{d}, []Operand{a}, q)
}

// Ld loads from addr into d. Pass Vec(...) as d for a vector load; the .vN
// qualifier follows from the operand count.
func (b *Body) Ld(t Type, d Operand, addr Mem, q ...Qual) *Instr {
	return b.inst(OpLd, []Type{t}, []Operand{d}, []Operand{addr}, q)
}
func (b *Body) Ldu(t Type, d Operand, addr Mem, q ...Qual) *Instr {
	return b.inst(OpLdu, []Type{t}, []Operand{d}, []Operand{addr}, q)
}

// St stores v to addr. Pass Vec(...) as v for a vector store.
func (b *Body) St(t Type, addr Mem, v Operand, q ...Qual) *Instr {
	return b.inst(OpSt, []Type{t}, nil, []Operand{addr, v}, q)
}

func (b *Body) Prefetch(addr Mem, q ...Qual) *Instr {
	return b.inst(OpPrefetch, nil, nil, []Operand{addr}, q)
}
func (b *Body) PrefetchU(addr Mem, q ...Qual) *Instr {
	return b.inst(OpPrefetchU, nil, nil, []Operand{addr}, q)
}
func (b *Body) Isspacep(p Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpIsspacep, nil, []Operand{p}, []Operand{a}, q)
}
func (b *Body) Mapa(t Type, d Reg, a, rank Operand, q ...Qual) *Instr {
	return b.inst(OpMapa, []Type{t}, []Operand{d}, []Operand{a, rank}, q)
}
func (b *Body) Getctarank(t Type, d Reg, a Operand, q ...Qual) *Instr {
	return b.inst(OpGetctarank, []Type{t}, []Operand{d}, []Operand{a}, q)
}
func (b *Body) ApplyPriority(addr Mem, size Operand, q ...Qual) *Instr {
	return b.inst(OpApplyPriority, nil, nil, []Operand{addr, size}, q)
}
func (b *Body) Discard(addr Mem, size Operand, q ...Qual) *Instr {
	return b.inst(OpDiscard, nil, nil, []Operand{addr, size}, q)
}

// ---- Control flow ---------------------------------------------------------

func (b *Body) Bra(l *Label, q ...Qual) *Instr {
	return b.inst(OpBra, nil, nil, []Operand{l}, q)
}

// BrxIdx emits an indexed branch together with its .branchtargets directive,
// which is appended at the end of the body by the printer.
func (b *Body) BrxIdx(idx Reg, targets []*Label, q ...Qual) *Instr {
	bt := &BranchTargets{Name: "$L__bt" + itoa(b.btSeq), Targets: targets}
	b.btSeq++
	in := b.inst(OpBrxIdx, nil, nil, []Operand{idx, bt}, q)
	b.items = append(b.items, bt)
	return in
}

// Call emits a direct call. Param marshalling is the caller's job; see
// Invoke for the complete ABI sequence.
func (b *Body) Call(fn *Func, args []Operand, rets []Reg, q ...Qual) *Instr {
	var dst []Operand
	if len(rets) > 0 {
		g := make(group, len(rets))
		for i, r := range rets {
			g[i] = r
		}
		dst = []Operand{g}
	}
	src := []Operand{fn}
	if len(args) > 0 {
		src = append(src, group(args))
	}
	return b.inst(OpCall, nil, dst, src, q)
}

// CallIndirect emits an indirect call through fn, described by proto. If
// targets is non-empty a .calltargets directive is emitted alongside.
func (b *Body) CallIndirect(fn Reg, proto *Proto, args []Operand, rets []Reg, targets []*Func, q ...Qual) *Instr {
	var dst []Operand
	if len(rets) > 0 {
		g := make(group, len(rets))
		for i, r := range rets {
			g[i] = r
		}
		dst = []Operand{g}
	}
	src := []Operand{fn}
	if len(args) > 0 {
		src = append(src, group(args))
	}
	if len(targets) > 0 {
		ct := &CallTargets{Name: "$L__ct" + itoa(b.ctSeq), Targets: targets}
		b.ctSeq++
		b.items = append(b.items, ct)
		src = append(src, ct)
	} else {
		src = append(src, proto)
	}
	return b.inst(OpCall, nil, dst, src, q)
}

func (b *Body) Ret(q ...Qual) *Instr { return b.inst(OpRet, nil, nil, nil, q) }
func (b *Body) Exit() *Instr         { return b.inst(OpExit, nil, nil, nil, nil) }

// ---- Synchronization and communication ------------------------------------

func (b *Body) BarSync(id Operand, count ...Operand) *Instr {
	src := append([]Operand{id}, count...)
	return b.inst(OpBarSync, nil, nil, src, nil)
}
func (b *Body) BarArrive(id, count Operand) *Instr {
	return b.inst(OpBarArrive, nil, nil, []Operand{id, count}, nil)
}
func (b *Body) BarrierSync(id Operand, q ...Qual) *Instr {
	return b.inst(OpBarrierSync, nil, nil, []Operand{id}, q)
}
func (b *Body) BarWarpSync(mask Operand) *Instr {
	return b.inst(OpBarWarpSync, nil, nil, []Operand{mask}, nil)
}
func (b *Body) BarrierClusterArrive(q ...Qual) *Instr {
	return b.inst(OpBarrierClusterArrive, nil, nil, nil, q)
}
func (b *Body) BarrierClusterWait(q ...Qual) *Instr {
	return b.inst(OpBarrierClusterWait, nil, nil, nil, q)
}
func (b *Body) Membar(q ...Qual) *Instr {
	return b.inst(OpMembar, nil, nil, nil, q)
}
func (b *Body) Fence(q ...Qual) *Instr {
	return b.inst(OpFence, nil, nil, nil, q)
}

// Atom emits an atomic read-modify-write. Pass the comparand and the value
// for AtomCAS; a single value otherwise.
func (b *Body) Atom(t Type, d Reg, addr Mem, vals []Operand, q ...Qual) *Instr {
	return b.inst(OpAtom, []Type{t}, []Operand{d}, append([]Operand{addr}, vals...), q)
}

// Red emits a reduction with no result register.
func (b *Body) Red(t Type, addr Mem, v Operand, q ...Qual) *Instr {
	return b.inst(OpRed, []Type{t}, nil, []Operand{addr, v}, q)
}

func (b *Body) ShflSync(t Type, d Operand, a, c, e, mask Operand, q ...Qual) *Instr {
	return b.inst(OpShflSync, []Type{t}, []Operand{d}, []Operand{a, c, e, mask}, q)
}
func (b *Body) VoteSync(t Type, d Reg, p, mask Operand, q ...Qual) *Instr {
	return b.inst(OpVoteSync, []Type{t}, []Operand{d}, []Operand{p, mask}, q)
}
func (b *Body) MatchSync(t Type, d Reg, a, mask Operand, q ...Qual) *Instr {
	return b.inst(OpMatchSync, []Type{t}, []Operand{d}, []Operand{a, mask}, q)
}
func (b *Body) Activemask(t Type, d Reg) *Instr {
	return b.inst(OpActivemask, []Type{t}, []Operand{d}, nil, nil)
}
func (b *Body) ReduxSync(t Type, d Reg, a, mask Operand, q ...Qual) *Instr {
	return b.inst(OpReduxSync, []Type{t}, []Operand{d}, []Operand{a, mask}, q)
}
func (b *Body) ElectSync(p Reg, mask Operand) *Instr {
	return b.inst(OpElectSync, []Type{B32}, []Operand{p}, []Operand{mask}, nil)
}
func (b *Body) GridDepLaunch() *Instr {
	return b.inst(OpGridDepLaunch, nil, nil, nil, nil)
}
func (b *Body) GridDepWait() *Instr {
	return b.inst(OpGridDepWait, nil, nil, nil, nil)
}

// ---- Stack manipulation ---------------------------------------------------

func (b *Body) Alloca(t Type, d Reg, size, align Operand) *Instr {
	return b.inst(OpAlloca, []Type{t}, []Operand{d}, []Operand{size, align}, nil)
}
func (b *Body) StackSave(t Type, d Reg) *Instr {
	return b.inst(OpStackSave, []Type{t}, []Operand{d}, nil, nil)
}
func (b *Body) StackRestore(t Type, a Operand) *Instr {
	return b.inst(OpStackRestore, []Type{t}, nil, []Operand{a}, nil)
}

// ---- Miscellaneous --------------------------------------------------------

func (b *Body) Trap() *Instr  { return b.inst(OpTrap, nil, nil, nil, nil) }
func (b *Body) Brkpt() *Instr { return b.inst(OpBrkpt, nil, nil, nil, nil) }
func (b *Body) Nanosleep(t Type, n Operand) *Instr {
	return b.inst(OpNanosleep, []Type{t}, nil, []Operand{n}, nil)
}
func (b *Body) Pmevent(n Operand, q ...Qual) *Instr {
	return b.inst(OpPmevent, nil, nil, []Operand{n}, q)
}
func (b *Body) SetMaxNReg(t Type, n Operand, q ...Qual) *Instr {
	return b.inst(OpSetMaxNReg, []Type{t}, nil, []Operand{n}, q)
}

// ---- ABI helper -----------------------------------------------------------

// Invoke emits the complete call sequence for fn: a nested scope declaring
// one .param variable per argument and return value, the stores that
// marshal the arguments, the call itself, and the loads that retrieve the
// results.
func (b *Body) Invoke(fn *Func, args []Operand, argTypes []Type, rets []Reg) *Block {
	blk := &Block{}
	inner := &Body{Regs: b.Regs, labels: b.labels}

	var argParams []*Var
	for i, t := range argTypes {
		p := &Var{Space: ParamSpace, Type: t, Name: "param" + itoa(i)}
		blk.Locals = append(blk.Locals, p)
		argParams = append(argParams, p)
		inner.St(t, At(p), args[i], ParamSpace)
	}

	var retParams []*Var
	for i, r := range rets {
		p := &Var{Space: ParamSpace, Type: r.Type(), Name: "retval" + itoa(i)}
		blk.Locals = append(blk.Locals, p)
		retParams = append(retParams, p)
	}

	callArgs := make([]Operand, len(argParams))
	for i, p := range argParams {
		callArgs[i] = p
	}
	callRets := make([]Operand, len(retParams))
	for i, p := range retParams {
		callRets[i] = p
	}

	var dst []Operand
	if len(callRets) > 0 {
		dst = []Operand{group(callRets)}
	}
	src := []Operand{fn}
	if len(callArgs) > 0 {
		src = append(src, group(callArgs))
	}
	inner.inst(OpCall, nil, dst, src, []Qual{Uni})

	for i, r := range rets {
		inner.Ld(r.Type(), r, At(retParams[i]), ParamSpace)
	}

	blk.Items = inner.items
	b.items = append(b.items, blk)
	return blk
}