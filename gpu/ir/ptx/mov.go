package ptx

// Mov emits mov.<t> d, a.
func (cb *CodeBuilder) Mov(t Type, d Reg, a any) *CodeBuilder { return cb.op1("mov", t, d, a) }

func (cb *CodeBuilder) MovU16(d Reg, a any) *CodeBuilder  { return cb.Mov(U16, d, a) }
func (cb *CodeBuilder) MovU32(d Reg, a any) *CodeBuilder  { return cb.Mov(U32, d, a) }
func (cb *CodeBuilder) MovU64(d Reg, a any) *CodeBuilder  { return cb.Mov(U64, d, a) }
func (cb *CodeBuilder) MovS32(d Reg, a any) *CodeBuilder  { return cb.Mov(S32, d, a) }
func (cb *CodeBuilder) MovS64(d Reg, a any) *CodeBuilder  { return cb.Mov(S64, d, a) }
func (cb *CodeBuilder) MovB32(d Reg, a any) *CodeBuilder  { return cb.Mov(B32, d, a) }
func (cb *CodeBuilder) MovB64(d Reg, a any) *CodeBuilder  { return cb.Mov(B64, d, a) }
func (cb *CodeBuilder) MovF32(d Reg, a any) *CodeBuilder  { return cb.Mov(F32, d, a) }
func (cb *CodeBuilder) MovF64(d Reg, a any) *CodeBuilder  { return cb.Mov(F64, d, a) }
func (cb *CodeBuilder) MovPred(d Reg, a any) *CodeBuilder { return cb.Mov(Pred, d, a) }

// MovAddr emits mov.u64 d, symbol; (take the generic address of a
// module-scope variable, kernel, or function).
func (cb *CodeBuilder) MovAddr(d Reg, symbol string) *CodeBuilder {
	return cb.Mov(U64, d, Sym(symbol))
}

// Cvt emits cvt<opts>.<dst>.<src> d, a.
func (cb *CodeBuilder) Cvt(dst, src Type, d Reg, a any, opts ...Opt) *CodeBuilder {
	return cb.emit("cvt"+optString(opts)+dst.String()+src.String(), d, toOperand(a))
}

// Cvta emits cvta.<space>.u<size> (space address -> generic address).
func (cb *CodeBuilder) Cvta(space Space, d Reg, a any) *CodeBuilder {
	return cb.emit("cvta"+space.String()+".u64", d, toOperand(a))
}

// CvtaTo emits cvta.to.<space>.u64 (generic address -> space address).
func (cb *CodeBuilder) CvtaTo(space Space, d Reg, a any) *CodeBuilder {
	return cb.emit("cvta.to"+space.String()+".u64", d, toOperand(a))
}

func (cb *CodeBuilder) CvtaGlobal(d Reg, a any) *CodeBuilder   { return cb.Cvta(Global, d, a) }
func (cb *CodeBuilder) CvtaShared(d Reg, a any) *CodeBuilder   { return cb.Cvta(Shared, d, a) }
func (cb *CodeBuilder) CvtaLocal(d Reg, a any) *CodeBuilder    { return cb.Cvta(Local, d, a) }
func (cb *CodeBuilder) CvtaToGlobal(d Reg, a any) *CodeBuilder { return cb.CvtaTo(Global, d, a) }
func (cb *CodeBuilder) CvtaToShared(d Reg, a any) *CodeBuilder { return cb.CvtaTo(Shared, d, a) }
func (cb *CodeBuilder) CvtaToLocal(d Reg, a any) *CodeBuilder  { return cb.CvtaTo(Local, d, a) }