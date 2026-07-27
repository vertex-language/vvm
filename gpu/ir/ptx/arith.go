package ptx

// ---- Generic integer/FP arithmetic --------------------------------------

// Add emits add<opts>.<t> d, a, b.
func (cb *CodeBuilder) Add(t Type, d Reg, a, b any, opts ...Opt) *CodeBuilder {
	return cb.op2("add", t, d, a, b, opts...)
}

// Sub emits sub<opts>.<t> d, a, b.
func (cb *CodeBuilder) Sub(t Type, d Reg, a, b any, opts ...Opt) *CodeBuilder {
	return cb.op2("sub", t, d, a, b, opts...)
}

// Setp emits setp.<cmp><opts>.<t> p, a, b.
func (cb *CodeBuilder) Setp(cmp CmpOp, t Type, p Reg, a, b any, opts ...Opt) *CodeBuilder {
	return cb.op2("setp."+string(cmp), t, p, a, b, opts...)
}

// ---- Integer add/sub -----------------------------------------------------

func (cb *CodeBuilder) AddS32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("add", S32, d, a, b, o...) }
func (cb *CodeBuilder) AddU32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("add", U32, d, a, b, o...) }
func (cb *CodeBuilder) AddS64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("add", S64, d, a, b, o...) }
func (cb *CodeBuilder) AddU64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("add", U64, d, a, b, o...) }
func (cb *CodeBuilder) SubS32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("sub", S32, d, a, b, o...) }
func (cb *CodeBuilder) SubU32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("sub", U32, d, a, b, o...) }
func (cb *CodeBuilder) SubS64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("sub", S64, d, a, b, o...) }
func (cb *CodeBuilder) SubU64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("sub", U64, d, a, b, o...) }

// ---- Integer mul/mad/div/rem ---------------------------------------------

func (cb *CodeBuilder) MulLoS32(d Reg, a, b any) *CodeBuilder   { return cb.op2("mul.lo", S32, d, a, b) }
func (cb *CodeBuilder) MulLoU32(d Reg, a, b any) *CodeBuilder   { return cb.op2("mul.lo", U32, d, a, b) }
func (cb *CodeBuilder) MulLoS64(d Reg, a, b any) *CodeBuilder   { return cb.op2("mul.lo", S64, d, a, b) }
func (cb *CodeBuilder) MulLoU64(d Reg, a, b any) *CodeBuilder   { return cb.op2("mul.lo", U64, d, a, b) }
func (cb *CodeBuilder) MulHiS32(d Reg, a, b any) *CodeBuilder   { return cb.op2("mul.hi", S32, d, a, b) }
func (cb *CodeBuilder) MulHiU32(d Reg, a, b any) *CodeBuilder   { return cb.op2("mul.hi", U32, d, a, b) }
func (cb *CodeBuilder) MulWideS32(d Reg, a, b any) *CodeBuilder { return cb.op2("mul.wide", S32, d, a, b) }
func (cb *CodeBuilder) MulWideU32(d Reg, a, b any) *CodeBuilder { return cb.op2("mul.wide", U32, d, a, b) }

func (cb *CodeBuilder) MadLoS32(d Reg, a, b, c any) *CodeBuilder { return cb.op3("mad.lo", S32, d, a, b, c) }
func (cb *CodeBuilder) MadLoU32(d Reg, a, b, c any) *CodeBuilder { return cb.op3("mad.lo", U32, d, a, b, c) }
func (cb *CodeBuilder) MadLoS64(d Reg, a, b, c any) *CodeBuilder { return cb.op3("mad.lo", S64, d, a, b, c) }
func (cb *CodeBuilder) MadLoU64(d Reg, a, b, c any) *CodeBuilder { return cb.op3("mad.lo", U64, d, a, b, c) }
func (cb *CodeBuilder) MadWideU32(d Reg, a, b, c any) *CodeBuilder {
	return cb.op3("mad.wide", U32, d, a, b, c)
}

func (cb *CodeBuilder) DivS32(d Reg, a, b any) *CodeBuilder { return cb.op2("div", S32, d, a, b) }
func (cb *CodeBuilder) DivU32(d Reg, a, b any) *CodeBuilder { return cb.op2("div", U32, d, a, b) }
func (cb *CodeBuilder) DivS64(d Reg, a, b any) *CodeBuilder { return cb.op2("div", S64, d, a, b) }
func (cb *CodeBuilder) DivU64(d Reg, a, b any) *CodeBuilder { return cb.op2("div", U64, d, a, b) }
func (cb *CodeBuilder) RemS32(d Reg, a, b any) *CodeBuilder { return cb.op2("rem", S32, d, a, b) }
func (cb *CodeBuilder) RemU32(d Reg, a, b any) *CodeBuilder { return cb.op2("rem", U32, d, a, b) }
func (cb *CodeBuilder) RemS64(d Reg, a, b any) *CodeBuilder { return cb.op2("rem", S64, d, a, b) }
func (cb *CodeBuilder) RemU64(d Reg, a, b any) *CodeBuilder { return cb.op2("rem", U64, d, a, b) }

// ---- abs/neg/min/max -------------------------------------------------------

func (cb *CodeBuilder) AbsS32(d Reg, a any) *CodeBuilder { return cb.op1("abs", S32, d, a) }
func (cb *CodeBuilder) AbsS64(d Reg, a any) *CodeBuilder { return cb.op1("abs", S64, d, a) }
func (cb *CodeBuilder) NegS32(d Reg, a any) *CodeBuilder { return cb.op1("neg", S32, d, a) }
func (cb *CodeBuilder) NegS64(d Reg, a any) *CodeBuilder { return cb.op1("neg", S64, d, a) }

func (cb *CodeBuilder) MinS32(d Reg, a, b any) *CodeBuilder { return cb.op2("min", S32, d, a, b) }
func (cb *CodeBuilder) MinU32(d Reg, a, b any) *CodeBuilder { return cb.op2("min", U32, d, a, b) }
func (cb *CodeBuilder) MinS64(d Reg, a, b any) *CodeBuilder { return cb.op2("min", S64, d, a, b) }
func (cb *CodeBuilder) MinU64(d Reg, a, b any) *CodeBuilder { return cb.op2("min", U64, d, a, b) }
func (cb *CodeBuilder) MaxS32(d Reg, a, b any) *CodeBuilder { return cb.op2("max", S32, d, a, b) }
func (cb *CodeBuilder) MaxU32(d Reg, a, b any) *CodeBuilder { return cb.op2("max", U32, d, a, b) }
func (cb *CodeBuilder) MaxS64(d Reg, a, b any) *CodeBuilder { return cb.op2("max", S64, d, a, b) }
func (cb *CodeBuilder) MaxU64(d Reg, a, b any) *CodeBuilder { return cb.op2("max", U64, d, a, b) }

// ---- Bit manipulation --------------------------------------------------------

func (cb *CodeBuilder) Popc(d Reg, a any) *CodeBuilder { return cb.op1("popc", B32, d, a) }
func (cb *CodeBuilder) Clz(d Reg, a any) *CodeBuilder  { return cb.op1("clz", B32, d, a) }
func (cb *CodeBuilder) Brev(d Reg, a any) *CodeBuilder { return cb.op1("brev", B32, d, a) }

// Bfe emits bfe.<t> d, a, b, c (bit-field extract).
func (cb *CodeBuilder) Bfe(t Type, d Reg, a, b, c any) *CodeBuilder {
	return cb.op3("bfe", t, d, a, b, c)
}

// Bfi emits bfi.<t> f, a, b, c, d (bit-field insert).
func (cb *CodeBuilder) Bfi(t Type, f Reg, a, b, c, d any) *CodeBuilder {
	return cb.emit("bfi"+t.String(), f, toOperand(a), toOperand(b), toOperand(c), toOperand(d))
}

// ---- Floating point ------------------------------------------------------------

func (cb *CodeBuilder) AddF16(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("add", F16, d, a, b, o...) }
func (cb *CodeBuilder) AddF32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("add", F32, d, a, b, o...) }
func (cb *CodeBuilder) AddF64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("add", F64, d, a, b, o...) }
func (cb *CodeBuilder) SubF32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("sub", F32, d, a, b, o...) }
func (cb *CodeBuilder) SubF64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("sub", F64, d, a, b, o...) }
func (cb *CodeBuilder) MulF32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("mul", F32, d, a, b, o...) }
func (cb *CodeBuilder) MulF64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("mul", F64, d, a, b, o...) }

func (cb *CodeBuilder) FmaF32(d Reg, a, b, c any, o ...Opt) *CodeBuilder { return cb.op3("fma", F32, d, a, b, c, o...) }
func (cb *CodeBuilder) FmaF64(d Reg, a, b, c any, o ...Opt) *CodeBuilder { return cb.op3("fma", F64, d, a, b, c, o...) }

func (cb *CodeBuilder) DivF32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("div", F32, d, a, b, o...) }
func (cb *CodeBuilder) DivF64(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("div", F64, d, a, b, o...) }

func (cb *CodeBuilder) SqrtF32(d Reg, a any, o ...Opt) *CodeBuilder  { return cb.op1("sqrt", F32, d, a, o...) }
func (cb *CodeBuilder) SqrtF64(d Reg, a any, o ...Opt) *CodeBuilder  { return cb.op1("sqrt", F64, d, a, o...) }
func (cb *CodeBuilder) RcpF32(d Reg, a any, o ...Opt) *CodeBuilder   { return cb.op1("rcp", F32, d, a, o...) }
func (cb *CodeBuilder) RcpF64(d Reg, a any, o ...Opt) *CodeBuilder   { return cb.op1("rcp", F64, d, a, o...) }
func (cb *CodeBuilder) RsqrtF32(d Reg, a any, o ...Opt) *CodeBuilder { return cb.op1("rsqrt", F32, d, a, o...) }
func (cb *CodeBuilder) RsqrtF64(d Reg, a any, o ...Opt) *CodeBuilder { return cb.op1("rsqrt", F64, d, a, o...) }
func (cb *CodeBuilder) SinF32(d Reg, a any, o ...Opt) *CodeBuilder   { return cb.op1("sin", F32, d, a, o...) }
func (cb *CodeBuilder) CosF32(d Reg, a any, o ...Opt) *CodeBuilder   { return cb.op1("cos", F32, d, a, o...) }
func (cb *CodeBuilder) Ex2F32(d Reg, a any, o ...Opt) *CodeBuilder   { return cb.op1("ex2", F32, d, a, o...) }
func (cb *CodeBuilder) Lg2F32(d Reg, a any, o ...Opt) *CodeBuilder   { return cb.op1("lg2", F32, d, a, o...) }

func (cb *CodeBuilder) AbsF32(d Reg, a any, o ...Opt) *CodeBuilder { return cb.op1("abs", F32, d, a, o...) }
func (cb *CodeBuilder) AbsF64(d Reg, a any, o ...Opt) *CodeBuilder { return cb.op1("abs", F64, d, a, o...) }
func (cb *CodeBuilder) NegF32(d Reg, a any, o ...Opt) *CodeBuilder { return cb.op1("neg", F32, d, a, o...) }
func (cb *CodeBuilder) NegF64(d Reg, a any, o ...Opt) *CodeBuilder { return cb.op1("neg", F64, d, a, o...) }
func (cb *CodeBuilder) MinF32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("min", F32, d, a, b, o...) }
func (cb *CodeBuilder) MaxF32(d Reg, a, b any, o ...Opt) *CodeBuilder { return cb.op2("max", F32, d, a, b, o...) }

// ---- setp wrappers -----------------------------------------------------------------

func (cb *CodeBuilder) SetpEqS32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpEq, S32, p, a, b) }
func (cb *CodeBuilder) SetpNeS32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpNe, S32, p, a, b) }
func (cb *CodeBuilder) SetpLtS32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpLt, S32, p, a, b) }
func (cb *CodeBuilder) SetpLeS32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpLe, S32, p, a, b) }
func (cb *CodeBuilder) SetpGtS32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpGt, S32, p, a, b) }
func (cb *CodeBuilder) SetpGeS32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpGe, S32, p, a, b) }

func (cb *CodeBuilder) SetpEqU32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpEq, U32, p, a, b) }
func (cb *CodeBuilder) SetpNeU32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpNe, U32, p, a, b) }
func (cb *CodeBuilder) SetpLtU32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpLt, U32, p, a, b) }
func (cb *CodeBuilder) SetpLeU32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpLe, U32, p, a, b) }
func (cb *CodeBuilder) SetpGtU32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpGt, U32, p, a, b) }
func (cb *CodeBuilder) SetpGeU32(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpGe, U32, p, a, b) }

func (cb *CodeBuilder) SetpEqS64(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpEq, S64, p, a, b) }
func (cb *CodeBuilder) SetpLtS64(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpLt, S64, p, a, b) }
func (cb *CodeBuilder) SetpGeS64(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpGe, S64, p, a, b) }
func (cb *CodeBuilder) SetpEqU64(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpEq, U64, p, a, b) }
func (cb *CodeBuilder) SetpLtU64(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpLt, U64, p, a, b) }
func (cb *CodeBuilder) SetpGeU64(p Reg, a, b any) *CodeBuilder { return cb.Setp(CmpGe, U64, p, a, b) }

func (cb *CodeBuilder) SetpEqF32(p Reg, a, b any, o ...Opt) *CodeBuilder { return cb.Setp(CmpEq, F32, p, a, b, o...) }
func (cb *CodeBuilder) SetpNeF32(p Reg, a, b any, o ...Opt) *CodeBuilder { return cb.Setp(CmpNe, F32, p, a, b, o...) }
func (cb *CodeBuilder) SetpLtF32(p Reg, a, b any, o ...Opt) *CodeBuilder { return cb.Setp(CmpLt, F32, p, a, b, o...) }
func (cb *CodeBuilder) SetpLeF32(p Reg, a, b any, o ...Opt) *CodeBuilder { return cb.Setp(CmpLe, F32, p, a, b, o...) }
func (cb *CodeBuilder) SetpGtF32(p Reg, a, b any, o ...Opt) *CodeBuilder { return cb.Setp(CmpGt, F32, p, a, b, o...) }
func (cb *CodeBuilder) SetpGeF32(p Reg, a, b any, o ...Opt) *CodeBuilder { return cb.Setp(CmpGe, F32, p, a, b, o...) }
func (cb *CodeBuilder) SetpLtF64(p Reg, a, b any, o ...Opt) *CodeBuilder { return cb.Setp(CmpLt, F64, p, a, b, o...) }

// ---- select ---------------------------------------------------------------------------

// Selp emits selp.<t> d, a, b, p.
func (cb *CodeBuilder) Selp(t Type, d Reg, a, b any, p Reg) *CodeBuilder {
	return cb.op3("selp", t, d, a, b, p)
}
func (cb *CodeBuilder) SelpS32(d Reg, a, b any, p Reg) *CodeBuilder { return cb.Selp(S32, d, a, b, p) }
func (cb *CodeBuilder) SelpU32(d Reg, a, b any, p Reg) *CodeBuilder { return cb.Selp(U32, d, a, b, p) }
func (cb *CodeBuilder) SelpB32(d Reg, a, b any, p Reg) *CodeBuilder { return cb.Selp(B32, d, a, b, p) }
func (cb *CodeBuilder) SelpF32(d Reg, a, b any, p Reg) *CodeBuilder { return cb.Selp(F32, d, a, b, p) }
func (cb *CodeBuilder) SelpF64(d Reg, a, b any, p Reg) *CodeBuilder { return cb.Selp(F64, d, a, b, p) }

// Slct emits slct.<t>.s32 d, a, b, c (select on sign of c).
func (cb *CodeBuilder) Slct(t Type, d Reg, a, b, c any) *CodeBuilder {
	return cb.op3("slct", Type{name: t.Name() + ".s32", bits: t.Bits()}, d, a, b, c)
}
func (cb *CodeBuilder) SlctF32(d Reg, a, b, c any) *CodeBuilder { return cb.Slct(F32, d, a, b, c) }
func (cb *CodeBuilder) SlctS32(d Reg, a, b, c any) *CodeBuilder { return cb.Slct(S32, d, a, b, c) }