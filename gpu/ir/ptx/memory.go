package ptx

// ---- Loads -----------------------------------------------------------

// Ld emits ld.<space><opts>.<t> d, [addr].
func (cb *CodeBuilder) Ld(space Space, t Type, d Reg, addr AddrRef, opts ...Opt) *CodeBuilder {
	return cb.emit("ld"+space.String()+optString(opts)+t.String(), d, addr)
}

// LdVolatile emits ld.volatile.<space>.<t> d, [addr].
func (cb *CodeBuilder) LdVolatile(space Space, t Type, d Reg, addr AddrRef) *CodeBuilder {
	return cb.emit("ld.volatile"+space.String()+t.String(), d, addr)
}

// LdAcquire emits ld.acquire.<scope>.<space>.<t> d, [addr].
func (cb *CodeBuilder) LdAcquire(scope Scope, space Space, t Type, d Reg, addr AddrRef) *CodeBuilder {
	return cb.emit("ld.acquire"+string(scope)+space.String()+t.String(), d, addr)
}

// LdV2 / LdV4 emit vector loads: ld.<space>.v4.<t> {d0..}, [addr].
func (cb *CodeBuilder) LdV2(space Space, t Type, dsts []Reg, addr AddrRef, opts ...Opt) *CodeBuilder {
	return cb.ldVec(space, 2, t, dsts, addr, opts)
}
func (cb *CodeBuilder) LdV4(space Space, t Type, dsts []Reg, addr AddrRef, opts ...Opt) *CodeBuilder {
	return cb.ldVec(space, 4, t, dsts, addr, opts)
}
func (cb *CodeBuilder) ldVec(space Space, n int, t Type, dsts []Reg, addr AddrRef, opts []Opt) *CodeBuilder {
	ops := make([]Operand, len(dsts))
	for i, r := range dsts {
		ops[i] = r
	}
	return cb.emit(
		"ld"+space.String()+optString(opts)+".v"+itoa(n)+t.String(),
		vecOperand(ops), addr)
}

// ---- Stores ----------------------------------------------------------

// St emits st.<space><opts>.<t> [addr], v.
func (cb *CodeBuilder) St(space Space, t Type, addr AddrRef, v any, opts ...Opt) *CodeBuilder {
	return cb.emit("st"+space.String()+optString(opts)+t.String(), addr, toOperand(v))
}

// StVolatile emits st.volatile.<space>.<t> [addr], v.
func (cb *CodeBuilder) StVolatile(space Space, t Type, addr AddrRef, v any) *CodeBuilder {
	return cb.emit("st.volatile"+space.String()+t.String(), addr, toOperand(v))
}

// StRelease emits st.release.<scope>.<space>.<t> [addr], v.
func (cb *CodeBuilder) StRelease(scope Scope, space Space, t Type, addr AddrRef, v any) *CodeBuilder {
	return cb.emit("st.release"+string(scope)+space.String()+t.String(), addr, toOperand(v))
}

// StV2 / StV4 emit vector stores.
func (cb *CodeBuilder) StV2(space Space, t Type, addr AddrRef, srcs []Reg) *CodeBuilder {
	return cb.stVec(space, 2, t, addr, srcs)
}
func (cb *CodeBuilder) StV4(space Space, t Type, addr AddrRef, srcs []Reg) *CodeBuilder {
	return cb.stVec(space, 4, t, addr, srcs)
}
func (cb *CodeBuilder) stVec(space Space, n int, t Type, addr AddrRef, srcs []Reg) *CodeBuilder {
	ops := make([]Operand, len(srcs))
	for i, r := range srcs {
		ops[i] = r
	}
	return cb.emit("st"+space.String()+".v"+itoa(n)+t.String(), addr, vecOperand(ops))
}

// ---- Atomics & reductions ---------------------------------------------

// Atom emits atom.<space>.<op>.<t> d, [addr], v[, c].
func (cb *CodeBuilder) Atom(space Space, op AtomOp, t Type, d Reg, addr AddrRef, vs ...any) *CodeBuilder {
	ops := []Operand{d, addr}
	for _, v := range vs {
		ops = append(ops, toOperand(v))
	}
	return cb.emit("atom"+space.String()+"."+string(op)+t.String(), ops...)
}

// Red emits red.<space>.<op>.<t> [addr], v.
func (cb *CodeBuilder) Red(space Space, op RedOp, t Type, addr AddrRef, v any) *CodeBuilder {
	return cb.emit("red"+space.String()+"."+string(op)+t.String(), addr, toOperand(v))
}

// ---- Param wrappers ------------------------------------------------------

func (cb *CodeBuilder) LdParam(t Type, d Reg, name string) *CodeBuilder {
	return cb.emit("ld.param"+t.String(), d, SymAddr(name))
}
func (cb *CodeBuilder) LdParamU32(d Reg, name string) *CodeBuilder { return cb.LdParam(U32, d, name) }
func (cb *CodeBuilder) LdParamU64(d Reg, name string) *CodeBuilder { return cb.LdParam(U64, d, name) }
func (cb *CodeBuilder) LdParamS32(d Reg, name string) *CodeBuilder { return cb.LdParam(S32, d, name) }
func (cb *CodeBuilder) LdParamF32(d Reg, name string) *CodeBuilder { return cb.LdParam(F32, d, name) }
func (cb *CodeBuilder) LdParamF64(d Reg, name string) *CodeBuilder { return cb.LdParam(F64, d, name) }

// ---- Global wrappers -------------------------------------------------------

func (cb *CodeBuilder) LdGlobalU32(d Reg, addr AddrRef, o ...Opt) *CodeBuilder { return cb.Ld(Global, U32, d, addr, o...) }
func (cb *CodeBuilder) LdGlobalU64(d Reg, addr AddrRef, o ...Opt) *CodeBuilder { return cb.Ld(Global, U64, d, addr, o...) }
func (cb *CodeBuilder) LdGlobalS32(d Reg, addr AddrRef, o ...Opt) *CodeBuilder { return cb.Ld(Global, S32, d, addr, o...) }
func (cb *CodeBuilder) LdGlobalF32(d Reg, addr AddrRef, o ...Opt) *CodeBuilder { return cb.Ld(Global, F32, d, addr, o...) }
func (cb *CodeBuilder) LdGlobalF64(d Reg, addr AddrRef, o ...Opt) *CodeBuilder { return cb.Ld(Global, F64, d, addr, o...) }

func (cb *CodeBuilder) StGlobalU32(addr AddrRef, v any, o ...Opt) *CodeBuilder { return cb.St(Global, U32, addr, v, o...) }
func (cb *CodeBuilder) StGlobalU64(addr AddrRef, v any, o ...Opt) *CodeBuilder { return cb.St(Global, U64, addr, v, o...) }
func (cb *CodeBuilder) StGlobalS32(addr AddrRef, v any, o ...Opt) *CodeBuilder { return cb.St(Global, S32, addr, v, o...) }
func (cb *CodeBuilder) StGlobalF32(addr AddrRef, v any, o ...Opt) *CodeBuilder { return cb.St(Global, F32, addr, v, o...) }
func (cb *CodeBuilder) StGlobalF64(addr AddrRef, v any, o ...Opt) *CodeBuilder { return cb.St(Global, F64, addr, v, o...) }

// ---- Shared wrappers ----------------------------------------------------------

func (cb *CodeBuilder) LdSharedU32(d Reg, addr AddrRef) *CodeBuilder { return cb.Ld(Shared, U32, d, addr) }
func (cb *CodeBuilder) LdSharedF32(d Reg, addr AddrRef) *CodeBuilder { return cb.Ld(Shared, F32, d, addr) }
func (cb *CodeBuilder) StSharedU32(addr AddrRef, v any) *CodeBuilder { return cb.St(Shared, U32, addr, v) }
func (cb *CodeBuilder) StSharedF32(addr AddrRef, v any) *CodeBuilder { return cb.St(Shared, F32, addr, v) }

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}