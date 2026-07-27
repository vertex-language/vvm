package ptx

func (cb *CodeBuilder) AndB32(d Reg, a, b any) *CodeBuilder  { return cb.op2("and", B32, d, a, b) }
func (cb *CodeBuilder) AndB64(d Reg, a, b any) *CodeBuilder  { return cb.op2("and", B64, d, a, b) }
func (cb *CodeBuilder) OrB32(d Reg, a, b any) *CodeBuilder   { return cb.op2("or", B32, d, a, b) }
func (cb *CodeBuilder) OrB64(d Reg, a, b any) *CodeBuilder   { return cb.op2("or", B64, d, a, b) }
func (cb *CodeBuilder) XorB32(d Reg, a, b any) *CodeBuilder  { return cb.op2("xor", B32, d, a, b) }
func (cb *CodeBuilder) XorB64(d Reg, a, b any) *CodeBuilder  { return cb.op2("xor", B64, d, a, b) }
func (cb *CodeBuilder) NotB32(d Reg, a any) *CodeBuilder     { return cb.op1("not", B32, d, a) }
func (cb *CodeBuilder) NotB64(d Reg, a any) *CodeBuilder     { return cb.op1("not", B64, d, a) }

func (cb *CodeBuilder) AndPred(p Reg, q, r any) *CodeBuilder { return cb.op2("and", Pred, p, q, r) }
func (cb *CodeBuilder) OrPred(p Reg, q, r any) *CodeBuilder  { return cb.op2("or", Pred, p, q, r) }
func (cb *CodeBuilder) XorPred(p Reg, q, r any) *CodeBuilder { return cb.op2("xor", Pred, p, q, r) }
func (cb *CodeBuilder) NotPred(p Reg, q any) *CodeBuilder    { return cb.op1("not", Pred, p, q) }

func (cb *CodeBuilder) ShlB32(d Reg, a, b any) *CodeBuilder { return cb.op2("shl", B32, d, a, b) }
func (cb *CodeBuilder) ShlB64(d Reg, a, b any) *CodeBuilder { return cb.op2("shl", B64, d, a, b) }
func (cb *CodeBuilder) ShrU32(d Reg, a, b any) *CodeBuilder { return cb.op2("shr", U32, d, a, b) }
func (cb *CodeBuilder) ShrS32(d Reg, a, b any) *CodeBuilder { return cb.op2("shr", S32, d, a, b) }
func (cb *CodeBuilder) ShrU64(d Reg, a, b any) *CodeBuilder { return cb.op2("shr", U64, d, a, b) }
func (cb *CodeBuilder) ShrS64(d Reg, a, b any) *CodeBuilder { return cb.op2("shr", S64, d, a, b) }

// Shf emits a funnel shift: shf.<dir>.<mode>.b32 d, a, b, c
// dir is "l" or "r"; mode is "clamp" or "wrap".
func (cb *CodeBuilder) Shf(dir, mode string, d Reg, a, b, c any) *CodeBuilder {
	return cb.op3("shf."+dir+"."+mode, B32, d, a, b, c)
}