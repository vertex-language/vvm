package amdtx

import "fmt"

// Label is a symbolic branch target within a code builder.
type Label struct{ name string }

// CodeBuilder accumulates virtual instructions for one kernel/function body.
type CodeBuilder struct {
	Regs  RegFile
	insts []Inst

	nlabel int
	// control stack tracks open If/Else/Loop for structured lowering.
	ctrl []ctrlFrame
}

type ctrlFrame struct {
	kind    ctrlKind
	endName string
	elseSet bool
}

type ctrlKind int

const (
	ctrlIf ctrlKind = iota
	ctrlLoop
)

func newCodeBuilder() *CodeBuilder { return &CodeBuilder{} }

// Insts exposes the emitted instruction stream (read-only view).
func (cb *CodeBuilder) Insts() []Inst { return cb.insts }

func (cb *CodeBuilder) emit(in Inst) { cb.insts = append(cb.insts, in) }

// ---- special ABI registers ---------------------------------------------------

func (cb *CodeBuilder) ExecMask() Reg    { return EXEC }
func (cb *CodeBuilder) VCC() Reg         { return VCC }
func (cb *CodeBuilder) SCC() Reg         { return SCC }
func (cb *CodeBuilder) KernargPtr() Reg  { return Reg{Class: Special, spec: specKernargPtr, Width: 2} }
func (cb *CodeBuilder) WorkgroupIDX() Reg { return Reg{Class: Special, spec: specWorkgroupIDX, Width: 1} }
func (cb *CodeBuilder) WorkgroupIDY() Reg { return Reg{Class: Special, spec: specWorkgroupIDY, Width: 1} }
func (cb *CodeBuilder) WorkgroupIDZ() Reg { return Reg{Class: Special, spec: specWorkgroupIDZ, Width: 1} }
func (cb *CodeBuilder) WorkitemIDX() Reg { return Reg{Class: Special, spec: specWorkitemIDX, Width: 1} }
func (cb *CodeBuilder) WorkitemIDY() Reg { return Reg{Class: Special, spec: specWorkitemIDY, Width: 1} }
func (cb *CodeBuilder) WorkitemIDZ() Reg { return Reg{Class: Special, spec: specWorkitemIDZ, Width: 1} }

// asOperand coerces an int literal or an Operand into an Operand.
func asOp(v any) Operand {
	switch x := v.(type) {
	case Operand:
		return x
	case Reg:
		return x
	case int:
		return Inl(uint64(x))
	case uint32:
		return Inl(uint64(x))
	case uint64:
		return Lit(x)
	}
	panic(fmt.Sprintf("amdtx: bad operand %T", v))
}

// ---- scalar ALU --------------------------------------------------------------

func (cb *CodeBuilder) alu2(op Op, ty Type, d Reg, a, b any) {
	cb.emit(Inst{Op: op, Type: ty, Dst: []Reg{d}, Src: []Operand{asOp(a), asOp(b)}})
}
func (cb *CodeBuilder) alu1(op Op, ty Type, d Reg, a any) {
	cb.emit(Inst{Op: op, Type: ty, Dst: []Reg{d}, Src: []Operand{asOp(a)}})
}

func (cb *CodeBuilder) SAndB32(d, a, b Reg)   { cb.alu2(OpSAndB32, B32, d, a, b) }
func (cb *CodeBuilder) SOrB32(d, a, b Reg)    { cb.alu2(OpSOrB32, B32, d, a, b) }
func (cb *CodeBuilder) SXorB32(d, a, b Reg)   { cb.alu2(OpSXorB32, B32, d, a, b) }
func (cb *CodeBuilder) SNandB32(d, a, b Reg)  { cb.alu2(OpSNandB32, B32, d, a, b) }
func (cb *CodeBuilder) SNorB32(d, a, b Reg)   { cb.alu2(OpSNorB32, B32, d, a, b) }
func (cb *CodeBuilder) SAndn2B32(d, a, b Reg) { cb.alu2(OpSAndn2B32, B32, d, a, b) }
func (cb *CodeBuilder) SLshlB32(d, a, b any)  { cb.alu2(OpSLshlB32, B32, d.(Reg), a, b) }
func (cb *CodeBuilder) SLshrB32(d Reg, a, b any) { cb.alu2(OpSLshrB32, B32, d, a, b) }
func (cb *CodeBuilder) SAshrI32(d Reg, a, b any) { cb.alu2(OpSAshrI32, S32, d, a, b) }
func (cb *CodeBuilder) SAddU32(d Reg, a, b any)  { cb.alu2(OpSAddU32, U32, d, a, b) }
func (cb *CodeBuilder) SSubU32(d Reg, a, b any)  { cb.alu2(OpSSubU32, U32, d, a, b) }
func (cb *CodeBuilder) SAddcU32(d Reg, a, b any) { cb.alu2(OpSAddcU32, U32, d, a, b) }
func (cb *CodeBuilder) SMulI32(d Reg, a, b any)  { cb.alu2(OpSMulI32, S32, d, a, b) }
func (cb *CodeBuilder) SMovB32(d Reg, src any)   { cb.alu1(OpSMovB32, B32, d, src) }
func (cb *CodeBuilder) SCselectB32(d, a, b Reg)  { cb.alu2(OpSCselectB32, B32, d, a, b) }

// ---- vector ALU --------------------------------------------------------------

func (cb *CodeBuilder) VAndB32(d, a, b Reg) { cb.alu2(OpVAndB32, B32, d, a, b) }
func (cb *CodeBuilder) VOrB32(d, a, b Reg)  { cb.alu2(OpVOrB32, B32, d, a, b) }
func (cb *CodeBuilder) VXorB32(d, a, b Reg) { cb.alu2(OpVXorB32, B32, d, a, b) }
func (cb *CodeBuilder) VNotB32(d, a Reg)    { cb.alu1(OpVNotB32, B32, d, a) }

func (cb *CodeBuilder) VLshlrevB32(d Reg, shift, a any) { cb.alu2(OpVLshlrevB32, B32, d, shift, a) }
func (cb *CodeBuilder) VLshrrevB32(d Reg, shift, a any) { cb.alu2(OpVLshrrevB32, B32, d, shift, a) }
func (cb *CodeBuilder) VAshrrevI32(d Reg, shift, a any) { cb.alu2(OpVAshrrevI32, S32, d, shift, a) }

func (cb *CodeBuilder) VAddU32(d Reg, a, b any)   { cb.alu2(OpVAddU32, U32, d, a, b) }
func (cb *CodeBuilder) VSubU32(d Reg, a, b any)   { cb.alu2(OpVSubU32, U32, d, a, b) }
func (cb *CodeBuilder) VMulLoU32(d Reg, a, b any) { cb.alu2(OpVMulLoU32, U32, d, a, b) }
func (cb *CodeBuilder) VMulHiU32(d Reg, a, b any) { cb.alu2(OpVMulHiU32, U32, d, a, b) }

// VMadU32U24 computes d = a*b + c on the low 24 bits of a and b.
func (cb *CodeBuilder) VMadU32U24(d Reg, a, b, c any) {
	cb.emit(Inst{Op: OpVMadU32U24, Type: U32, Dst: []Reg{d},
		Src: []Operand{asOp(a), asOp(b), asOp(c)}})
}

func (cb *CodeBuilder) VAddCoU32(d Reg, a, b any)  { cb.alu2(OpVAddCoU32, U32, d, a, b) }
func (cb *CodeBuilder) VAddcCoU32(d Reg, a, b any) { cb.alu2(OpVAddcCoU32, U32, d, a, b) }

func (cb *CodeBuilder) VAddF32(d, a, b Reg) { cb.alu2(OpVAddF32, F32, d, a, b) }
func (cb *CodeBuilder) VSubF32(d, a, b Reg) { cb.alu2(OpVSubF32, F32, d, a, b) }
func (cb *CodeBuilder) VMulF32(d, a, b Reg) { cb.alu2(OpVMulF32, F32, d, a, b) }
func (cb *CodeBuilder) VMaxF32(d, a, b Reg) { cb.alu2(OpVMaxF32, F32, d, a, b) }
func (cb *CodeBuilder) VMinF32(d, a, b Reg) { cb.alu2(OpVMinF32, F32, d, a, b) }

// VFmaF32 computes d = a*b + c in one rounding step.
func (cb *CodeBuilder) VFmaF32(d, a, b, c Reg) {
	cb.emit(Inst{Op: OpVFmaF32, Type: F32, Dst: []Reg{d},
		Src: []Operand{a, b, c}})
}

func (cb *CodeBuilder) VRcpF32(d, a Reg)  { cb.alu1(OpVRcpF32, F32, d, a) }
func (cb *CodeBuilder) VRsqF32(d, a Reg)  { cb.alu1(OpVRsqF32, F32, d, a) }
func (cb *CodeBuilder) VSqrtF32(d, a Reg) { cb.alu1(OpVSqrtF32, F32, d, a) }
func (cb *CodeBuilder) VExpF32(d, a Reg)  { cb.alu1(OpVExpF32, F32, d, a) }
func (cb *CodeBuilder) VLogF32(d, a Reg)  { cb.alu1(OpVLogF32, F32, d, a) }

func (cb *CodeBuilder) VCvtF32I32(d, a Reg) { cb.alu1(OpVCvtF32I32, F32, d, a) }
func (cb *CodeBuilder) VCvtI32F32(d, a Reg) { cb.alu1(OpVCvtI32F32, S32, d, a) }

func (cb *CodeBuilder) VMovB32(d Reg, src any) { cb.alu1(OpVMovB32, B32, d, src) }

// VCndmaskB32 selects a or b per-lane based on a predicate/VCC.
func (cb *CodeBuilder) VCndmaskB32(d, a, b, cond Reg) {
	cb.emit(Inst{Op: OpVCndmaskB32, Type: B32, Dst: []Reg{d},
		Src: []Operand{a, b, cond}})
}

// ---- compares ---------------------------------------------------------------

// VCmpLtU32 emits a vector compare and returns the predicate (VCC-shaped) reg.
func (cb *CodeBuilder) VCmpLtU32(a, b any) Reg {
	cb.emit(Inst{Op: OpVCmpLtU32, Type: U32, Dst: []Reg{VCC}, Src: []Operand{asOp(a), asOp(b)}})
	return VCC
}
func (cb *CodeBuilder) VCmpEqU32(a, b any) Reg {
	cb.emit(Inst{Op: OpVCmpEqU32, Type: U32, Dst: []Reg{VCC}, Src: []Operand{asOp(a), asOp(b)}})
	return VCC
}
func (cb *CodeBuilder) VCmpGtU32(a, b any) Reg {
	cb.emit(Inst{Op: OpVCmpGtU32, Type: U32, Dst: []Reg{VCC}, Src: []Operand{asOp(a), asOp(b)}})
	return VCC
}

// SCmpEqU32 emits a scalar compare (result lands in SCC).
func (cb *CodeBuilder) SCmpEqU32(a, b any) Reg {
	cb.emit(Inst{Op: OpSCmpEqU32, Type: U32, Dst: []Reg{SCC}, Src: []Operand{asOp(a), asOp(b)}})
	return SCC
}
func (cb *CodeBuilder) SCmpLtU32(a, b any) Reg {
	cb.emit(Inst{Op: OpSCmpLtU32, Type: U32, Dst: []Reg{SCC}, Src: []Operand{asOp(a), asOp(b)}})
	return SCC
}

// ---- memory -----------------------------------------------------------------

func (cb *CodeBuilder) SLoadDword(d, base Reg, off int32) {
	cb.emit(Inst{Op: OpSLoadDword, Type: B32, Dst: []Reg{d}, Src: []Operand{Off(base, off)}})
}
func (cb *CodeBuilder) SLoadDwordx2(d, base Reg, off int32) {
	cb.emit(Inst{Op: OpSLoadDwordx2, Type: B64, Dst: []Reg{d}, Src: []Operand{Off(base, off)}})
}
func (cb *CodeBuilder) SLoadDwordx4(d, base Reg, off int32) {
	cb.emit(Inst{Op: OpSLoadDwordx4, Type: B32, Dst: []Reg{d}, Src: []Operand{Off(base, off)}})
}

func (cb *CodeBuilder) GlobalLoadDword(d, voff, sbase Reg) {
	cb.emit(Inst{Op: OpGlobalLoadDword, Type: B32, Dst: []Reg{d}, Src: []Operand{voff, sbase}})
}
func (cb *CodeBuilder) GlobalStoreDword(voff, vsrc, sbase Reg) {
	cb.emit(Inst{Op: OpGlobalStoreDword, Type: B32, Src: []Operand{voff, vsrc, sbase}})
}

func (cb *CodeBuilder) FlatLoadDword(d, vaddr Reg) {
	cb.emit(Inst{Op: OpFlatLoadDword, Type: B32, Dst: []Reg{d}, Src: []Operand{vaddr}})
}
func (cb *CodeBuilder) FlatStoreDword(vaddr, vsrc Reg) {
	cb.emit(Inst{Op: OpFlatStoreDword, Type: B32, Src: []Operand{vaddr, vsrc}})
}

func (cb *CodeBuilder) DsReadB32(d, vaddr Reg) {
	cb.emit(Inst{Op: OpDsReadB32, Type: B32, Dst: []Reg{d}, Src: []Operand{vaddr}})
}
func (cb *CodeBuilder) DsWriteB32(vaddr, vsrc Reg) {
	cb.emit(Inst{Op: OpDsWriteB32, Type: B32, Src: []Operand{vaddr, vsrc}})
}

func (cb *CodeBuilder) GlobalAtomicAddU32(d, voff, vsrc, sbase Reg) {
	cb.emit(Inst{Op: OpGlobalAtomicAddU32, Type: U32, Dst: []Reg{d}, Src: []Operand{voff, vsrc, sbase}})
}

// ---- sync / control ---------------------------------------------------------

func (cb *CodeBuilder) Waitcnt(w ...Waitcnt) { cb.emit(Inst{Op: OpWaitcnt, Wait: w}) }

// AutoWaitcnt inserts conservative waitcnts before every consumer of an
// outstanding load. It is a lightweight pass over the current stream.
func (cb *CodeBuilder) AutoWaitcnt() { insertAutoWaitcnt(cb) }

func (cb *CodeBuilder) SBarrier()       { cb.emit(Inst{Op: OpBarrier}) }
func (cb *CodeBuilder) SBarrierSignal() { cb.emit(Inst{Op: OpBarrierSignal}) }
func (cb *CodeBuilder) SBarrierWait()   { cb.emit(Inst{Op: OpBarrierWait}) }

func (cb *CodeBuilder) DsBpermuteB32(d, vidx, vsrc Reg) {
	cb.emit(Inst{Op: OpBpermuteB32, Type: B32, Dst: []Reg{d}, Src: []Operand{vidx, vsrc}})
}
func (cb *CodeBuilder) VReadlaneB32(d, vsrc Reg, lane any) {
	cb.emit(Inst{Op: OpReadlaneB32, Type: B32, Dst: []Reg{d}, Src: []Operand{vsrc, asOp(lane)}})
}
func (cb *CodeBuilder) VWritelaneB32(d Reg, sval, lane any) {
	cb.emit(Inst{Op: OpWritelaneB32, Type: B32, Dst: []Reg{d}, Src: []Operand{asOp(sval), asOp(lane)}})
}

func (cb *CodeBuilder) SEndpgm()      { cb.emit(Inst{Op: OpEndpgm}) }
func (cb *CodeBuilder) SNop(n int)    { cb.emit(Inst{Op: OpNop, Src: []Operand{Inl(uint64(n))}}) }
func (cb *CodeBuilder) STrap(id int)  { cb.emit(Inst{Op: OpTrap, Src: []Operand{Inl(uint64(id))}}) }

// ---- labels & branches ------------------------------------------------------

func (cb *CodeBuilder) NewLabel(name string) Label {
	cb.nlabel++
	return Label{name: fmt.Sprintf("%s%d", name, cb.nlabel)}
}
func (cb *CodeBuilder) Bind(l Label)          { cb.emit(Inst{Op: OpBranch, Label: l.name, Type: Pred}) } // pseudo: label def
func (cb *CodeBuilder) SBranch(l Label)       { cb.emit(Inst{Op: OpBranch, Label: l.name}) }
func (cb *CodeBuilder) SCbranchSCC0(l Label)  { cb.emit(Inst{Op: OpCbranchSCC0, Label: l.name}) }
func (cb *CodeBuilder) SCbranchSCC1(l Label)  { cb.emit(Inst{Op: OpCbranchSCC1, Label: l.name}) }
func (cb *CodeBuilder) SCbranchExecz(l Label) { cb.emit(Inst{Op: OpCbranchExecz, Label: l.name}) }
func (cb *CodeBuilder) SCbranchVccz(l Label)  { cb.emit(Inst{Op: OpCbranchVccz, Label: l.name}) }
func (cb *CodeBuilder) SCbranchVccnz(l Label) { cb.emit(Inst{Op: OpCbranchVccnz, Label: l.name}) }

// ---- structured control -----------------------------------------------------

// If opens a divergent region guarded by cond; lower expands it to
// s_and_saveexec + s_cbranch_execz.
func (cb *CodeBuilder) If(cond Reg) {
	end := cb.NewLabel("endif").name
	cb.ctrl = append(cb.ctrl, ctrlFrame{kind: ctrlIf, endName: end})
	cb.emit(Inst{Op: OpIfBegin, Type: cond.typeForPred(), Src: []Operand{cond}, Label: end})
}

// Else flips the EXEC mask to the complementary lane set.
func (cb *CodeBuilder) Else() {
	if n := len(cb.ctrl); n > 0 && cb.ctrl[n-1].kind == ctrlIf {
		cb.ctrl[n-1].elseSet = true
		cb.emit(Inst{Op: OpElse, Label: cb.ctrl[n-1].endName})
	}
}

// End closes the most recent If.
func (cb *CodeBuilder) End() {
	if n := len(cb.ctrl); n > 0 {
		f := cb.ctrl[n-1]
		cb.ctrl = cb.ctrl[:n-1]
		cb.emit(Inst{Op: OpEndControl, Label: f.endName})
	}
}

// LoopHandle drives a structured loop.
type LoopHandle struct {
	cb      *CodeBuilder
	topName string
	endName string
}

// Loop opens a structured loop.
func (cb *CodeBuilder) Loop() *LoopHandle {
	top := cb.NewLabel("looptop").name
	end := cb.NewLabel("loopend").name
	cb.ctrl = append(cb.ctrl, ctrlFrame{kind: ctrlLoop, endName: end})
	cb.emit(Inst{Op: OpLoopBegin, Label: top})
	return &LoopHandle{cb: cb, topName: top, endName: end}
}

// BreakIf exits the loop when cond is set.
func (lp *LoopHandle) BreakIf(cond Reg) {
	lp.cb.emit(Inst{Op: OpLoopBreakIf, Src: []Operand{cond}, Label: lp.endName})
}

// End closes the loop, branching back to the top.
func (lp *LoopHandle) End() {
	if n := len(lp.cb.ctrl); n > 0 {
		lp.cb.ctrl = lp.cb.ctrl[:n-1]
	}
	lp.cb.emit(Inst{Op: OpLoopEnd, Label: lp.topName, RawText: lp.endName})
}

func (r Reg) typeForPred() Type {
	if r.spec == specVCC {
		return U32
	}
	return Pred
}

// ---- raw escape hatches -----------------------------------------------------

func (cb *CodeBuilder) Raw(text string) { cb.emit(Inst{Op: OpRaw, RawText: text}) }
func (cb *CodeBuilder) RawBytes(b ...byte) { cb.emit(Inst{Op: OpRawBytes, RawData: b}) }
func (cb *CodeBuilder) Rawf(format string, a ...any) {
	cb.emit(Inst{Op: OpRaw, RawText: fmt.Sprintf(format, a...)})
}

// Loc attaches a source location to the next instruction (.loc marker).
func (cb *CodeBuilder) Loc(fileIdx, line, col int) {
	cb.emit(Inst{Op: OpLoc, FileIdx: fileIdx, Line: line, Col: col})
}