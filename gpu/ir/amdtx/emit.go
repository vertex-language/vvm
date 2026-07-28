package amdtx

// Emit methods build one Instr, append it to the body, and return it. The
// returned pointer is the handle for modifiers, pinned encodings, comments,
// and for any pass that wants to rewrite the instruction later.
//
// Memory helpers take the data register first and derive the mnemonic's
// width suffix from it, so a width/type mismatch is not expressible.

func (b *Body) inst(op Op, w Width, dst, src []Operand, mods []Mod) *Instr {
	return b.add(&Instr{Op: op, Width: w, Dst: dst, Src: src, Mods: mods})
}

// Inst appends an instruction with a mnemonic this package does not model.
// Custom instructions participate in verification, walking and printing
// exactly like modelled ones: they are classified from their mnemonic, and
// their width suffix is read back out of it.
func (b *Body) Inst(mnemonic string, dst Operand, src ...Operand) *Instr {
	var d []Operand
	if dst != nil {
		d = []Operand{dst}
	}
	return b.add(&Instr{Op: OpCustom, custom: mnemonic, Dst: d, Src: src})
}

// InstN is Inst for instructions with several destinations.
func (b *Body) InstN(mnemonic string, dst, src []Operand, mods ...Mod) *Instr {
	return b.add(&Instr{Op: OpCustom, custom: mnemonic, Dst: dst, Src: src, Mods: mods})
}

// ---- Scalar memory --------------------------------------------------------

// SLoad emits s_load_bN from an SGPR base in .constant or .global space.
func (b *Body) SLoad(dst Reg, addr Mem, mods ...Mod) *Instr {
	return b.inst(OpSLoad, dst.Width(), []Operand{dst}, []Operand{addr}, mods)
}

// SStore emits s_store_bN.
func (b *Body) SStore(addr Mem, src Reg, mods ...Mod) *Instr {
	return b.inst(OpSStore, src.Width(), nil, []Operand{addr, src}, mods)
}

// ---- Vector memory --------------------------------------------------------

func (b *Body) GlobalLoad(dst Reg, addr Mem, mods ...Mod) *Instr {
	return b.inst(OpGlobalLoad, dst.Width(), []Operand{dst}, []Operand{addr}, mods)
}
func (b *Body) GlobalStore(addr Mem, src Reg, mods ...Mod) *Instr {
	return b.inst(OpGlobalStore, src.Width(), nil, []Operand{addr, src}, mods)
}
func (b *Body) FlatLoad(dst Reg, addr Mem, mods ...Mod) *Instr {
	return b.inst(OpFlatLoad, dst.Width(), []Operand{dst}, []Operand{addr}, mods)
}
func (b *Body) FlatStore(addr Mem, src Reg, mods ...Mod) *Instr {
	return b.inst(OpFlatStore, src.Width(), nil, []Operand{addr, src}, mods)
}
func (b *Body) ScratchLoad(dst Reg, addr Mem, mods ...Mod) *Instr {
	return b.inst(OpScratchLoad, dst.Width(), []Operand{dst}, []Operand{addr}, mods)
}
func (b *Body) ScratchStore(addr Mem, src Reg, mods ...Mod) *Instr {
	return b.inst(OpScratchStore, src.Width(), nil, []Operand{addr, src}, mods)
}

// BufferLoad emits buffer_load_bN: a VGPR index plus an SGPR descriptor.
func (b *Body) BufferLoad(dst Reg, desc, idx Operand, mods ...Mod) *Instr {
	return b.inst(OpBufferLoad, dst.Width(), []Operand{dst}, []Operand{desc, idx}, mods)
}
func (b *Body) BufferStore(desc, idx Operand, src Reg, mods ...Mod) *Instr {
	return b.inst(OpBufferStore, src.Width(), nil, []Operand{desc, idx, src}, mods)
}

// ---- LDS ------------------------------------------------------------------

func (b *Body) DSLoad(dst Reg, addr Mem, mods ...Mod) *Instr {
	return b.inst(OpDSLoad, dst.Width(), []Operand{dst}, []Operand{addr}, mods)
}
func (b *Body) DSStore(addr Mem, src Reg, mods ...Mod) *Instr {
	return b.inst(OpDSStore, src.Width(), nil, []Operand{addr, src}, mods)
}

// ---- Synchronisation ------------------------------------------------------

// Waitcnt emits an explicit wait. There is no implicit ordering between an
// asynchronous memory operation and its consumer: adjacency conveys nothing
// (P6, §12.1).
func (b *Body) Waitcnt(cs ...Counter) *Instr {
	src := make([]Operand, len(cs))
	for i, c := range cs {
		src[i] = c
	}
	return b.inst(OpWaitcnt, NoWidth, nil, src, nil)
}

// WaitcntVScnt emits the separate store-counter wait. GFX10/GFX11 only (V12).
func (b *Body) WaitcntVScnt(n int) *Instr {
	return b.inst(OpWaitcntVScnt, NoWidth, nil, []Operand{Imm(n)}, nil)
}

// Fence emits an ordered fence. Both an ordering and a scope are required;
// a bare fence is rejected (V38). Lowering expands it into the target's
// cache-maintenance sequence.
func (b *Body) Fence(o Ordering, s Scope) *Instr {
	in := b.inst(OpFence, NoWidth, nil, nil, nil)
	in.Ord, in.Scope = o, s
	return in
}

// Barrier emits s_barrier, which must not appear under divergent control
// flow: all waves of the workgroup have to reach it (V21).
func (b *Body) Barrier() *Instr { return b.inst(OpBarrier, NoWidth, nil, nil, nil) }

// ---- Control flow and termination -----------------------------------------

func (b *Body) Branch(l *Label) *Instr {
	return b.inst(OpBranch, NoWidth, nil, []Operand{l}, nil)
}
func (b *Body) CBranchSCC0(l *Label) *Instr {
	return b.inst(OpCBranchSCC0, NoWidth, nil, []Operand{l}, nil)
}
func (b *Body) CBranchSCC1(l *Label) *Instr {
	return b.inst(OpCBranchSCC1, NoWidth, nil, []Operand{l}, nil)
}
func (b *Body) CBranchExecZ(l *Label) *Instr {
	return b.inst(OpCBranchExecZ, NoWidth, nil, []Operand{l}, nil)
}
func (b *Body) CBranchExecNZ(l *Label) *Instr {
	return b.inst(OpCBranchExecNZ, NoWidth, nil, []Operand{l}, nil)
}

// Call invokes a device function. Every call site is inlined by lowering,
// so the target must be a .func: kernels are not callable (V3) and
// recursive or indirect calls are rejected (V4).
func (b *Body) Call(f *Func, args ...Operand) *Instr {
	return b.inst(OpCall, NoWidth, nil, append([]Operand{f}, args...), nil)
}

// CallSym is Call through a symbol the package does not model. It exists so
// V3 and V4 have something to reject.
func (b *Body) CallSym(target Operand, args ...Operand) *Instr {
	return b.inst(OpCall, NoWidth, nil, append([]Operand{target}, args...), nil)
}

func (b *Body) Ret() *Instr    { return b.inst(OpRet, NoWidth, nil, nil, nil) }
func (b *Body) EndPgm() *Instr { return b.inst(OpEndPgm, NoWidth, nil, nil, nil) }
func (b *Body) Nop(n int) *Instr {
	return b.inst(OpNop, NoWidth, nil, []Operand{Imm(n)}, nil)
}