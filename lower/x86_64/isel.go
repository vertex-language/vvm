// isel.go
package x86_64

import (
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

func errBadModule(format string, a ...any) error { return fmt.Errorf(format, a...) }

func lowerFunc(m *vir.Module, ix *index, f *vir.Function) (Func, error) {
	l := newLayout(ix)

	types, order, err := typeFunc(l, f)
	if err != nil {
		return Func{}, err
	}
	fr, err := BuildFrame(l, f, order, types)
	if err != nil {
		return Func{}, err
	}

	s := &sel{
		m: m, ix: ix, l: l, f: f, fr: fr, types: types,
		os: m.Target.OS, tiers: tierSet(m.Target.Tiers),
	}
	for _, b := range f.AllBlocks() {
		if b.Label != "" {
			s.emit(Inst{Op: "label", Lbl: blockLabel(f.Name, b.Label)})
		}
		for _, line := range b.Lines {
			if err := s.selInst(line); err != nil {
				return Func{}, fmt.Errorf("%s: %w", line.Op, err)
			}
		}
		if err := s.selTerm(b.Term); err != nil {
			return Func{}, err
		}
	}

	code, fx, err := s.finish()
	if err != nil {
		return Func{}, err
	}
	return Func{Name: f.Name, Code: code, Align: 16, Export: f.Export, Fixups: fx}, nil
}

type sel struct {
	m     *vir.Module
	ix    *index
	l     *Layout
	f     *vir.Function
	fr    *Frame
	types map[string]vir.Type
	os    string
	tiers map[string]bool
	out   []Inst
}

func (s *sel) emit(i Inst) { s.out = append(s.out, i) }

func tierSet(tiers []string) map[string]bool {
	m := map[string]bool{}
	for _, t := range tiers {
		m[t] = true
	}
	return m
}

func blockLabel(fn, lbl string) string { return ".L." + fn + "." + lbl }

func checkValueType(t vir.Type) error {
	switch x := t.(type) {
	case vir.IntType:
		if x.Bits > 64 {
			return todo("i%d values need a register pair", x.Bits)
		}
		return nil
	case vir.PtrType, vir.ValistType, vir.FloatType:
		return nil
	case vir.VecType:
		return todo("vector values")
	}
	return errBadModule("type %s cannot name a value", t)
}

func widthOf(t vir.Type) int {
	switch x := t.(type) {
	case vir.IntType:
		switch {
		case x.Bits <= 8:
			return 1
		case x.Bits <= 16:
			return 2
		case x.Bits <= 32:
			return 4
		default:
			return 8
		}
	case vir.FloatType:
		if x.Bits == 32 {
			return 4
		}
		return 8
	default:
		return 8 
	}
}

func (s *sel) loadOperand(o vir.Operand, dst Reg) {
	switch o.Kind {
	case vir.OperandIdent:
		if g, ok := s.ix.globals[o.Ident]; ok {
			_ = g
			s.emit(Inst{Op: "lea", D: R(dst), S: MemRIP(o.Ident, 0)})
			return
		}
		if c, ok := s.ix.consts[o.Ident]; ok {
			s.emit(Inst{Op: "mov", D: R(dst), S: Imm(c.Value.Int), Sz: 8})
			return
		}
		if off, ok := s.fr.ParamStackOff(o.Ident); ok {
			if s.paramByVal(o.Ident) {
				s.emit(Inst{Op: "lea", D: R(dst), S: Mem(RRBP, int32(off))})
			} else {
				s.emit(Inst{Op: "mov", D: R(dst), S: Mem(RRBP, int32(off)), Sz: 8})
			}
			return
		}
		if slot, ok := s.fr.SlotOf(o.Ident); ok {
			s.emit(Inst{Op: "mov", D: R(dst), S: Slot(slot), Sz: 8})
			return
		}
		s.emit(Inst{Op: "lea", D: R(dst), S: MemRIP(o.Ident, 0)})
	case vir.OperandInt:
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(o.Int), Sz: 8})
	case vir.OperandFloat:
		if o.Type == vir.F32 {
			u := uint64(math.Float32bits(float32(o.Float)))
			s.emit(Inst{Op: "mov", D: R(dst), S: Imm(int64(u)), Sz: 4})
		} else {
			u := math.Float64bits(o.Float)
			s.emit(Inst{Op: "movabs", D: R(dst), S: Imm(int64(u))})
		}
	case vir.OperandBool:
		v := int64(0)
		if o.Bool {
			v = 1
		}
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(v), Sz: 8})
	case vir.OperandNull:
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(0), Sz: 8})
	default:
		s.emit(Inst{Op: "mov", D: R(dst), S: Imm(0), Sz: 8})
	}
}

func (s *sel) paramByVal(name string) bool {
	for _, p := range s.f.Params {
		if p.Name == name {
			return p.ByVal != ""
		}
	}
	return false
}

func (s *sel) storeReg(src Reg, name string) {
	if slot, ok := s.fr.SlotOf(name); ok {
		s.emit(Inst{Op: "mov", D: Slot(slot), S: R(src), Sz: 8})
	}
}

func (s *sel) maskTo(r Reg, bits int) {
	if bits >= 32 || bits == 0 {
		return
	}
	mask := int64(1)<<uint(bits) - 1
	s.emit(Inst{Op: "and", D: R(r), S: Imm(mask), Sz: 8})
}

func (s *sel) selInst(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpLoc:
		return nil 

	case vir.OpAdd, vir.OpSub, vir.OpMul, vir.OpDiv,
		vir.OpAnd, vir.OpOr, vir.OpXor:
		return s.selBinALU(in)

	case vir.OpSqrt:
		return s.selFloatSqrt(in)

	case vir.OpShl, vir.OpLShr, vir.OpAShr:
		return s.selShift(in)

	case vir.OpRotl, vir.OpRotr:
		return s.selRotate(in)

	case vir.OpNeg, vir.OpNot:
		return s.selUnary(in)

	case vir.OpAbs:
		return s.selAbs(in)

	case vir.OpUDiv, vir.OpSDiv, vir.OpURem, vir.OpSRem:
		return s.selDivide(in)

	case vir.OpSAddO, vir.OpUAddO, vir.OpSSubO, vir.OpUSubO, vir.OpSMulO, vir.OpUMulO:
		return s.selOverflow(in)

	case vir.OpUMulH, vir.OpSMulH:
		return s.selWideningMul(in)

	case vir.OpEq, vir.OpNe, vir.OpSlt, vir.OpSgt, vir.OpSle, vir.OpSge,
		vir.OpUlt, vir.OpUgt, vir.OpUle, vir.OpUge, vir.OpLt, vir.OpGt, vir.OpLe, vir.OpGe:
		return s.selCompare(in)

	case vir.OpSelect:
		return s.selSelect(in)

	case vir.OpSMin, vir.OpSMax, vir.OpUMin, vir.OpUMax:
		return s.selIntMinMax(in)

	case vir.OpMin, vir.OpMax:
		if vir.IsFloat(s.types[in.Result]) {
			return s.selFloatMinMax(in)
		}
		return s.selIntMinMax(in)

	case vir.OpTrunc, vir.OpZext, vir.OpSext, vir.OpBitcast,
		vir.OpFpromote, vir.OpFdemote, vir.OpSfromint, vir.OpUfromint,
		vir.OpStoint, vir.OpUtoint, vir.OpStointSat, vir.OpUtointSat:
		return s.selConvert(in)

	case vir.OpLoad, vir.OpLoadVol:
		return s.selLoad(in)
	case vir.OpStore, vir.OpStoreVol:
		return s.selStore(in)

	case vir.OpField:
		return s.selField(in)
	case vir.OpIndex:
		return s.selIndex(in)
	case vir.OpAlloca:
		return s.selAlloca(in)

	case vir.OpMemcopy, vir.OpMemmove, vir.OpMemset:
		return s.selBulk(in)

	case vir.OpAtomicLoad, vir.OpAtomicStore, vir.OpAtomicAdd, vir.OpAtomicSub,
		vir.OpAtomicAnd, vir.OpAtomicOr, vir.OpAtomicXor, vir.OpAtomicXchg,
		vir.OpCmpxchg, vir.OpFence:
		return s.selAtomic(in)

	case vir.OpCall:
		return s.selCall(in)
	case vir.OpSyscall:
		return s.selSyscall(in)

	case vir.OpVaStart:
		return s.selVaStart(in)
	case vir.OpVaArg:
		return s.selVaArg(in)
	case vir.OpVaEnd:
		return nil 

	case vir.OpPopcnt:
		return s.selPopcnt(in)
	case vir.OpCtlz, vir.OpCttz:
		return s.selBitScan(in)
	case vir.OpBSwap:
		return s.selBswap(in)
	case vir.OpBitrev:
		return s.selBitrev(in)

	case vir.OpUAddSat, vir.OpSAddSat, vir.OpUSubSat, vir.OpSSubSat:
		return s.selSaturating(in)

	case vir.OpFma, vir.OpCopysign, vir.OpFloor, vir.OpCeil, vir.OpTruncF, vir.OpNearest:
		return s.selFloatIntrinsics(in)

	case vir.OpSplat, vir.OpExtract, vir.OpInsert, vir.OpShuffle,
		vir.OpMaskedLoad, vir.OpMaskedStore, vir.OpGather, vir.OpScatter,
		vir.OpReduceAdd, vir.OpReduceMin, vir.OpReduceMax,
		vir.OpReduceAnd, vir.OpReduceOr, vir.OpReduceXor:
		return todo("vector op %s", in.Op)

	default:
		return todo("opcode %s", in.Op)
	}
}

var aluName = map[vir.Opcode]string{
	vir.OpAdd: "add", vir.OpSub: "sub",
	vir.OpAnd: "and", vir.OpOr: "or", vir.OpXor: "xor",
}

func (s *sel) selBinALU(in *vir.Instruction) error {
	t := s.types[in.Result]
	if vir.IsFloat(t) {
		return s.selFloatBinALU(in)
	}
	if err := checkValueType(t); err != nil {
		return err
	}
	w := widthOf(t)

	s.loadOperand(in.Args[0], RRAX)
	if in.Op == vir.OpMul {
		s.loadOperand(in.Args[1], RRCX)
		s.emit(Inst{Op: "imul2", D: R(RRAX), S: R(RRCX), Sz: w})
	} else {
		s.loadOperand(in.Args[1], RRCX)
		s.emit(Inst{Op: aluName[in.Op], D: R(RRAX), S: R(RRCX), Sz: w})
	}
	s.maskTo(RRAX, intBits(t))
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selFloatBinALU(in *vir.Instruction) error {
	t := s.types[in.Result]
	isF32 := t == vir.F32
	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)

	movOpTo := "movq_to_xmm"
	movOpFrom := "movq_from_xmm"
	sz := 8
	if isF32 {
		movOpTo = "movd_to_xmm"
		movOpFrom = "movd_from_xmm"
		sz = 4
	}
	s.emit(Inst{Op: movOpTo, D: R(RXMM0), S: R(RRAX), Sz: sz})
	s.emit(Inst{Op: movOpTo, D: R(RXMM1), S: R(RRCX), Sz: sz})

	var sseOp string
	switch in.Op {
	case vir.OpAdd:
		sseOp = "addsd"
		if isF32 { sseOp = "addss" }
	case vir.OpSub:
		sseOp = "subsd"
		if isF32 { sseOp = "subss" }
	case vir.OpMul:
		sseOp = "mulsd"
		if isF32 { sseOp = "mulss" }
	case vir.OpDiv:
		sseOp = "divsd"
		if isF32 { sseOp = "divss" }
	}
	s.emit(Inst{Op: sseOp, D: R(RXMM0), S: R(RXMM1)})
	s.emit(Inst{Op: movOpFrom, D: R(RRAX), S: R(RXMM0), Sz: sz})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selFloatSqrt(in *vir.Instruction) error {
	t := s.types[in.Result]
	isF32 := t == vir.F32
	s.loadOperand(in.Args[0], RRAX)
	movOpTo := "movq_to_xmm"
	movOpFrom := "movq_from_xmm"
	sz := 8
	sseOp := "sqrtsd"
	if isF32 {
		movOpTo = "movd_to_xmm"
		movOpFrom = "movd_from_xmm"
		sz = 4
		sseOp = "sqrtss"
	}
	s.emit(Inst{Op: movOpTo, D: R(RXMM0), S: R(RRAX), Sz: sz})
	s.emit(Inst{Op: sseOp, D: R(RXMM0), S: R(RXMM0)})
	s.emit(Inst{Op: movOpFrom, D: R(RRAX), S: R(RXMM0), Sz: sz})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selFloatMinMax(in *vir.Instruction) error {
	t := s.types[in.Result]
	isF32 := t == vir.F32
	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)

	movOpTo := "movq_to_xmm"
	movOpFrom := "movq_from_xmm"
	sz := 8
	if isF32 {
		movOpTo = "movd_to_xmm"
		movOpFrom = "movd_from_xmm"
		sz = 4
	}
	s.emit(Inst{Op: movOpTo, D: R(RXMM0), S: R(RRAX), Sz: sz})
	s.emit(Inst{Op: movOpTo, D: R(RXMM1), S: R(RRCX), Sz: sz})

	isNan := ".Lminmax.nan." + uniq(s)
	done := ".Lminmax.done." + uniq(s)

	ucomi := "ucomisd"
	minmax := "minsd"
	bitOp := "orpd"
	addOp := "addsd"
	if in.Op == vir.OpMax {
		minmax = "maxsd"
		bitOp = "andpd"
	}
	if isF32 {
		ucomi = "ucomiss"
		if in.Op == vir.OpMin { minmax = "minss"; bitOp = "orps" } else { minmax = "maxss"; bitOp = "andps" }
		addOp = "addss"
	}

	s.emit(Inst{Op: ucomi, D: R(RXMM0), S: R(RXMM0)})
	s.emit(Inst{Op: "jcc", CC: CondP, Lbl: isNan})
	s.emit(Inst{Op: ucomi, D: R(RXMM1), S: R(RXMM1)})
	s.emit(Inst{Op: "jcc", CC: CondP, Lbl: isNan})

	s.emit(Inst{Op: minmax, D: R(RXMM0), S: R(RXMM1)})
	s.emit(Inst{Op: bitOp, D: R(RXMM0), S: R(RXMM1)})
	s.emit(Inst{Op: "jmp", Lbl: done})

	s.emit(Inst{Op: "label", Lbl: isNan})
	s.emit(Inst{Op: addOp, D: R(RXMM0), S: R(RXMM1)})

	s.emit(Inst{Op: "label", Lbl: done})
	s.emit(Inst{Op: movOpFrom, D: R(RRAX), S: R(RXMM0), Sz: sz})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selUnary(in *vir.Instruction) error {
	t := s.types[in.Result]
	w := widthOf(t)
	s.loadOperand(in.Args[0], RRAX)
	op := "neg"
	if in.Op == vir.OpNot {
		op = "not"
	}
	s.emit(Inst{Op: op, S: R(RRAX), Sz: w})
	s.maskTo(RRAX, intBits(t))
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selAbs(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	cw := 4
	if bits > 32 {
		cw = 8
	}

	s.loadOperand(in.Args[0], RRAX)
	if bits < 32 {
		s.sext32(RRAX, bits)
	}
	s.emit(Inst{Op: "mov", D: R(RRCX), S: R(RRAX), Sz: 8})
	s.emit(Inst{Op: "sar", D: R(RRCX), S: Imm(int64(cw*8 - 1)), Sz: cw})
	s.emit(Inst{Op: "xor", D: R(RRAX), S: R(RRCX), Sz: cw})
	s.emit(Inst{Op: "sub", D: R(RRAX), S: R(RRCX), Sz: cw})
	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selOverflow(in *vir.Instruction) error {
	bits := intBits(in.Suffix)
	cw := 4
	if bits > 32 {
		cw = 8
	}
	signed := in.Op == vir.OpSAddO || in.Op == vir.OpSSubO || in.Op == vir.OpSMulO

	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	if signed && bits < 32 {
		s.sext32(RRAX, bits)
		s.sext32(RRCX, bits)
	}

	var cc byte
	switch in.Op {
	case vir.OpSAddO:
		s.emit(Inst{Op: "add", D: R(RRAX), S: R(RRCX), Sz: cw})
		cc = CondO
	case vir.OpUAddO:
		s.emit(Inst{Op: "add", D: R(RRAX), S: R(RRCX), Sz: cw})
		cc = CondB
	case vir.OpSSubO:
		s.emit(Inst{Op: "sub", D: R(RRAX), S: R(RRCX), Sz: cw})
		cc = CondO
	case vir.OpUSubO:
		s.emit(Inst{Op: "sub", D: R(RRAX), S: R(RRCX), Sz: cw})
		cc = CondB
	case vir.OpSMulO:
		s.emit(Inst{Op: "imul2", D: R(RRAX), S: R(RRCX), Sz: cw})
		cc = CondO
	case vir.OpUMulO:
		s.emit(Inst{Op: "mul", S: R(RRCX), Sz: cw})
		cc = CondO
	default:
		return todo("overflow predicate %s", in.Op)
	}
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(0), Sz: 8})
	s.emit(Inst{Op: "setcc", D: R(RRAX), CC: cc})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selWideningMul(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	cw := 4
	if bits > 32 {
		cw = 8
	}
	signed := in.Op == vir.OpSMulH

	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	if signed && bits < 32 {
		s.sext32(RRAX, bits)
		s.sext32(RRCX, bits)
	}
	op := "mul"
	if signed {
		op = "imul1"
	}
	s.emit(Inst{Op: op, S: R(RRCX), Sz: cw})
	s.maskTo(RRDX, bits)
	s.storeReg(RRDX, in.Result)
	return nil
}

func (s *sel) selSaturating(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	cw := 4
	if bits > 32 {
		cw = 8
	}
	isAdd := in.Op == vir.OpUAddSat || in.Op == vir.OpSAddSat
	signed := in.Op == vir.OpSAddSat || in.Op == vir.OpSSubSat

	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	if signed && bits < 32 {
		s.sext32(RRAX, bits)
		s.sext32(RRCX, bits)
	}
	s.emit(Inst{Op: "mov", D: R(RR11), S: R(RRAX), Sz: 8})

	switch in.Op {
	case vir.OpUAddSat:
		s.emit(Inst{Op: "mov", D: R(RRDX), S: Imm(-1), Sz: 8})
	case vir.OpUSubSat:
		s.emit(Inst{Op: "mov", D: R(RRDX), S: Imm(0), Sz: 8})
	case vir.OpSAddSat, vir.OpSSubSat:
		maxVal := (int64(1) << uint(bits-1)) - 1
		s.emit(Inst{Op: "sar", D: R(RR11), S: Imm(int64(cw*8 - 1)), Sz: cw})
		s.emit(Inst{Op: "xor", D: R(RR11), S: Imm(maxVal), Sz: cw})
	}

	op := "add"
	if !isAdd {
		op = "sub"
	}
	s.emit(Inst{Op: op, D: R(RRAX), S: R(RRCX), Sz: cw})

	switch in.Op {
	case vir.OpUAddSat, vir.OpUSubSat:
		s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRDX), CC: CondB, Sz: cw})
	case vir.OpSAddSat, vir.OpSSubSat:
		s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RR11), CC: CondO, Sz: cw})
	}
	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selShift(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)

	s.loadOperand(in.Args[0], RRAX)
	if in.Op == vir.OpAShr && bits < 32 {
		s.sext32(RRAX, bits)
	}
	s.loadOperand(in.Args[1], RRCX)
	op := map[vir.Opcode]string{vir.OpShl: "shl", vir.OpLShr: "shr", vir.OpAShr: "sar"}[in.Op]
	s.emit(Inst{Op: op, D: R(RRAX), S: R(RRCX), Sz: 4})
	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selRotate(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	w := widthOf(t)

	op := "rol"
	if in.Op == vir.OpRotr {
		op = "ror"
	}
	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	s.emit(Inst{Op: op, D: R(RRAX), S: R(RRCX), Sz: w})
	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) sext32(r Reg, bits int) {
	if bits >= 32 {
		return
	}
	sh := int64(32 - bits)
	s.emit(Inst{Op: "shl", D: R(r), S: Imm(sh), Sz: 4})
	s.emit(Inst{Op: "sar", D: R(r), S: Imm(sh), Sz: 4})
}

func (s *sel) selDivide(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	signed := in.Op == vir.OpSDiv || in.Op == vir.OpSRem
	rem := in.Op == vir.OpURem || in.Op == vir.OpSRem

	w := 4
	if bits > 32 {
		w = 8
	}

	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	if signed {
		s.sext32(RRAX, bits)
		s.emit(Inst{Op: "cqo", Sz: w})
		s.emit(Inst{Op: "idiv", S: R(RRCX), Sz: w})
	} else {
		s.emit(Inst{Op: "xor", D: R(RRDX), S: R(RRDX), Sz: w})
		s.emit(Inst{Op: "div", S: R(RRCX), Sz: w})
	}
	res := RRAX
	if rem {
		res = RRDX
	}
	s.maskTo(res, bits)
	s.storeReg(res, in.Result)
	return nil
}

func (s *sel) cmpCC(in *vir.Instruction) (cc byte, signed bool, w int) {
	ot := in.Suffix
	w = widthOf(ot)
	switch in.Op {
	case vir.OpEq:
		return CondE, false, w
	case vir.OpNe:
		return CondNE, false, w
	case vir.OpSlt, vir.OpLt:
		return CondL, true, w
	case vir.OpSgt, vir.OpGt:
		return CondG, true, w
	case vir.OpSle, vir.OpLe:
		return CondLE, true, w
	case vir.OpSge, vir.OpGe:
		return CondGE, true, w
	case vir.OpUlt:
		return CondB, false, w
	case vir.OpUgt:
		return CondA, false, w
	case vir.OpUle:
		return CondBE, false, w
	default:
		return CondAE, false, w
	}
}

func (s *sel) selCompare(in *vir.Instruction) error {
	if vir.IsFloat(in.Suffix) {
		return s.selFloatCompare(in)
	}
	cc, signed, w := s.cmpCC(in)
	bits := 8 * w
	if vir.IsInt(in.Suffix) {
		bits = in.Suffix.(vir.IntType).Bits
	}
	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	if signed && bits < 32 {
		s.sext32(RRAX, bits)
		s.sext32(RRCX, bits)
		w = 4
	}
	s.emit(Inst{Op: "cmp", D: R(RRAX), S: R(RRCX), Sz: w})
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(0), Sz: 8})
	s.emit(Inst{Op: "setcc", D: R(RRAX), CC: cc})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selFloatCompare(in *vir.Instruction) error {
	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)

	isF32 := in.Suffix == vir.F32
	movOpTo := "movq_to_xmm"
	ucomi := "ucomisd"
	sz := 8
	if isF32 {
		movOpTo = "movd_to_xmm"
		ucomi = "ucomiss"
		sz = 4
	}
	s.emit(Inst{Op: movOpTo, D: R(RXMM0), S: R(RRAX), Sz: sz})
	s.emit(Inst{Op: movOpTo, D: R(RXMM1), S: R(RRCX), Sz: sz})

	s.emit(Inst{Op: ucomi, D: R(RXMM0), S: R(RXMM1)})

	s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(0), Sz: 8})
	s.emit(Inst{Op: "mov", D: R(RRCX), S: Imm(1), Sz: 8})

	var cc byte
	unorderedVal := int64(0)
	switch in.Op {
	case vir.OpEq:
		cc = CondE
	case vir.OpNe:
		cc = CondNE
		unorderedVal = 1
	case vir.OpLt, vir.OpSlt:
		cc = CondB
	case vir.OpGt, vir.OpSgt:
		cc = CondA
	case vir.OpLe, vir.OpSle:
		cc = CondBE
	case vir.OpGe, vir.OpSge:
		cc = CondAE
	default:
		cc = CondAE
	}

	s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRCX), CC: cc, Sz: 8})
	if unorderedVal == 0 {
		s.emit(Inst{Op: "mov", D: R(RRDX), S: Imm(0), Sz: 8})
		s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRDX), CC: CondP, Sz: 8})
	} else {
		s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRCX), CC: CondP, Sz: 8})
	}

	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selSelect(in *vir.Instruction) error {
	t := s.types[in.Result]
	w := widthOf(t)
	s.loadOperand(in.Args[1], RRAX)
	s.loadOperand(in.Args[2], RRCX)
	s.loadOperand(in.Args[0], RRDX)
	s.emit(Inst{Op: "test", D: R(RRDX), S: R(RRDX), Sz: 4})
	s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRCX), CC: CondE, Sz: w})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selIntMinMax(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	w := widthOf(t)
	signed := in.Op == vir.OpSMin || in.Op == vir.OpSMax

	var cc byte
	switch in.Op {
	case vir.OpSMin:
		cc = CondGE
	case vir.OpSMax:
		cc = CondLE
	case vir.OpUMin:
		cc = CondAE
	default:
		cc = CondBE
	}

	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[1], RRCX)
	cw := w
	if signed && bits < 32 {
		s.sext32(RRAX, bits)
		s.sext32(RRCX, bits)
		cw = 4
	}
	s.emit(Inst{Op: "cmp", D: R(RRAX), S: R(RRCX), Sz: cw})
	s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRCX), CC: cc, Sz: w})
	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selConvert(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpFpromote, vir.OpFdemote, vir.OpSfromint, vir.OpUfromint,
		vir.OpStoint, vir.OpUtoint, vir.OpStointSat, vir.OpUtointSat:
		return s.selFloatConvert(in)
	}

	dst := s.types[in.Result]
	src := s.operandType(in.Args[0])
	s.loadOperand(in.Args[0], RRAX)

	switch in.Op {
	case vir.OpBitcast:
		s.storeReg(RRAX, in.Result)
		return nil
	case vir.OpTrunc:
		s.maskTo(RRAX, intBits(dst))
	case vir.OpZext:
		s.maskTo(RRAX, intBits(src))
	case vir.OpSext:
		s.sext32(RRAX, intBits(src))
		s.maskTo(RRAX, intBits(dst))
	}
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selFloatConvert(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpFpromote:
		s.loadOperand(in.Args[0], RRAX)
		s.emit(Inst{Op: "movd_to_xmm", D: R(RXMM0), S: R(RRAX), Sz: 4})
		s.emit(Inst{Op: "cvtss2sd", D: R(RXMM0), S: R(RXMM0)})
		s.emit(Inst{Op: "movq_from_xmm", D: R(RRAX), S: R(RXMM0), Sz: 8})
		s.storeReg(RRAX, in.Result)
		return nil

	case vir.OpFdemote:
		s.loadOperand(in.Args[0], RRAX)
		s.emit(Inst{Op: "movq_to_xmm", D: R(RXMM0), S: R(RRAX), Sz: 8})
		s.emit(Inst{Op: "cvtsd2ss", D: R(RXMM0), S: R(RXMM0)})
		s.emit(Inst{Op: "movd_from_xmm", D: R(RRAX), S: R(RXMM0), Sz: 4})
		s.storeReg(RRAX, in.Result)
		return nil

	case vir.OpSfromint:
		dstT := s.types[in.Result]
		srcT := s.operandType(in.Args[0])
		s.loadOperand(in.Args[0], RRAX)
		srcBits := intBits(srcT)
		if srcBits < 32 {
			s.sext32(RRAX, srcBits)
		}
		w := widthOf(srcT)
		if dstT == vir.F32 {
			s.emit(Inst{Op: "cvtsi2ss", D: R(RXMM0), S: R(RRAX), Sz: w})
			s.emit(Inst{Op: "movd_from_xmm", D: R(RRAX), S: R(RXMM0), Sz: 4})
		} else {
			s.emit(Inst{Op: "cvtsi2sd", D: R(RXMM0), S: R(RRAX), Sz: w})
			s.emit(Inst{Op: "movq_from_xmm", D: R(RRAX), S: R(RXMM0), Sz: 8})
		}
		s.storeReg(RRAX, in.Result)
		return nil

	case vir.OpUfromint:
		dstT := s.types[in.Result]
		srcT := s.operandType(in.Args[0])
		s.loadOperand(in.Args[0], RRAX)
		s.maskTo(RRAX, intBits(srcT))
		if dstT == vir.F32 {
			s.emit(Inst{Op: "cvtsi2ss", D: R(RXMM0), S: R(RRAX), Sz: 8})
			s.emit(Inst{Op: "movd_from_xmm", D: R(RRAX), S: R(RXMM0), Sz: 4})
		} else {
			s.emit(Inst{Op: "cvtsi2sd", D: R(RXMM0), S: R(RRAX), Sz: 8})
			s.emit(Inst{Op: "movq_from_xmm", D: R(RRAX), S: R(RXMM0), Sz: 8})
		}
		s.storeReg(RRAX, in.Result)
		return nil

	case vir.OpStoint, vir.OpUtoint:
		dstT := s.types[in.Result]
		srcT := s.operandType(in.Args[0])
		s.loadOperand(in.Args[0], RRAX)
		w := widthOf(dstT)
		if srcT == vir.F32 {
			s.emit(Inst{Op: "movd_to_xmm", D: R(RXMM0), S: R(RRAX), Sz: 4})
			s.emit(Inst{Op: "cvttss2si", D: R(RRAX), S: R(RXMM0), Sz: w})
		} else {
			s.emit(Inst{Op: "movq_to_xmm", D: R(RXMM0), S: R(RRAX), Sz: 8})
			s.emit(Inst{Op: "cvttsd2si", D: R(RRAX), S: R(RXMM0), Sz: w})
		}
		s.maskTo(RRAX, intBits(dstT))
		s.storeReg(RRAX, in.Result)
		return nil

	case vir.OpStointSat, vir.OpUtointSat:
		dstT := s.types[in.Result]
		srcT := s.operandType(in.Args[0])
		bits := intBits(dstT)
		s.loadOperand(in.Args[0], RRAX)

		movOpTo := "movq_to_xmm"
		sz := 8
		cvtt := "cvttsd2si"
		ucomi := "ucomisd"
		if srcT == vir.F32 {
			movOpTo = "movd_to_xmm"
			sz = 4
			cvtt = "cvttss2si"
			ucomi = "ucomiss"
		}
		s.emit(Inst{Op: movOpTo, D: R(RXMM0), S: R(RRAX), Sz: sz})

		s.emit(Inst{Op: cvtt, D: R(RRAX), S: R(RXMM0), Sz: 8})

		if in.Op == vir.OpStointSat {
			maxVal := (int64(1) << uint(bits-1)) - 1
			minVal := -(int64(1) << uint(bits-1))

			s.emit(Inst{Op: "mov", D: R(RRCX), S: Imm(minVal), Sz: 8})
			s.emit(Inst{Op: "cmp", D: R(RRAX), S: R(RRCX), Sz: 8})

			clampHigh := ".Lsat.high." + uniq(s)
			clampDone := ".Lsat.done." + uniq(s)

			s.emit(Inst{Op: "xorpd", D: R(RXMM1), S: R(RXMM1)})
			s.emit(Inst{Op: ucomi, D: R(RXMM0), S: R(RXMM1)})
			s.emit(Inst{Op: "jcc", CC: CondA, Lbl: clampHigh})
			s.emit(Inst{Op: "jmp", Lbl: clampDone})

			s.emit(Inst{Op: "label", Lbl: clampHigh})
			s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(maxVal), Sz: 8})

			s.emit(Inst{Op: "label", Lbl: clampDone})
		} else {
			clampZero := ".Lsat.zero." + uniq(s)
			clampDone := ".Lsat.done." + uniq(s)

			s.emit(Inst{Op: "xorpd", D: R(RXMM1), S: R(RXMM1)})
			s.emit(Inst{Op: ucomi, D: R(RXMM0), S: R(RXMM1)})
			s.emit(Inst{Op: "jcc", CC: CondBE, Lbl: clampZero})
			s.emit(Inst{Op: "jcc", CC: CondP, Lbl: clampZero})
			s.emit(Inst{Op: "jmp", Lbl: clampDone})

			s.emit(Inst{Op: "label", Lbl: clampZero})
			s.emit(Inst{Op: "mov", D: R(RRAX), S: Imm(0), Sz: 8})

			s.emit(Inst{Op: "label", Lbl: clampDone})
		}
		s.maskTo(RRAX, bits)
		s.storeReg(RRAX, in.Result)
		return nil
	}
	return todo("convert %s", in.Op)
}

func (s *sel) selFloatIntrinsics(in *vir.Instruction) error {
	t := s.types[in.Result]
	isF32 := t == vir.F32
	sz := 8
	movOpTo := "movq_to_xmm"
	movOpFrom := "movq_from_xmm"
	roundOp := "roundsd"
	if isF32 {
		sz = 4
		movOpTo = "movd_to_xmm"
		movOpFrom = "movd_from_xmm"
		roundOp = "roundss"
	}

	switch in.Op {
	case vir.OpFma:
		s.loadOperand(in.Args[0], RRAX)
		s.loadOperand(in.Args[1], RRCX)
		s.loadOperand(in.Args[2], RRDX)
		s.emit(Inst{Op: movOpTo, D: R(RXMM0), S: R(RRAX), Sz: sz})
		s.emit(Inst{Op: movOpTo, D: R(RXMM1), S: R(RRCX), Sz: sz})
		s.emit(Inst{Op: movOpTo, D: R(RXMM2), S: R(RRDX), Sz: sz})

		mulOp := "mulsd"
		addOp := "addsd"
		if isF32 {
			mulOp = "mulss"
			addOp = "addss"
		}

		s.emit(Inst{Op: mulOp, D: R(RXMM0), S: R(RXMM1)})
		s.emit(Inst{Op: addOp, D: R(RXMM0), S: R(RXMM2)})
		s.emit(Inst{Op: movOpFrom, D: R(RRAX), S: R(RXMM0), Sz: sz})
		s.storeReg(RRAX, in.Result)
		return nil

	case vir.OpCopysign:
		s.loadOperand(in.Args[0], RRAX)
		s.loadOperand(in.Args[1], RRCX)
		if isF32 {
			s.emit(Inst{Op: "mov", D: R(RR11), S: Imm(int64(0x7FFFFFFF)), Sz: 4})
			s.emit(Inst{Op: "and", D: R(RRAX), S: R(RR11), Sz: 4})
			s.emit(Inst{Op: "mov", D: R(RR11), S: Imm(int64(-2147483648)), Sz: 4})
			s.emit(Inst{Op: "and", D: R(RRCX), S: R(RR11), Sz: 4})
			s.emit(Inst{Op: "or", D: R(RRAX), S: R(RRCX), Sz: 4})
		} else {
			s.emit(Inst{Op: "movabs", D: R(RR11), S: Imm(int64(0x7FFFFFFFFFFFFFFF))})
			s.emit(Inst{Op: "and", D: R(RRAX), S: R(RR11), Sz: 8})
			s.emit(Inst{Op: "movabs", D: R(RR11), S: Imm(int64(-0x8000000000000000))})
			s.emit(Inst{Op: "and", D: R(RRCX), S: R(RR11), Sz: 8})
			s.emit(Inst{Op: "or", D: R(RRAX), S: R(RRCX), Sz: 8})
		}
		s.storeReg(RRAX, in.Result)
		return nil

	case vir.OpFloor, vir.OpCeil, vir.OpTruncF, vir.OpNearest:
		s.loadOperand(in.Args[0], RRAX)
		s.emit(Inst{Op: movOpTo, D: R(RXMM0), S: R(RRAX), Sz: sz})
		var mode int64
		switch in.Op {
		case vir.OpNearest:
			mode = 0
		case vir.OpFloor:
			mode = 1
		case vir.OpCeil:
			mode = 2
		case vir.OpTruncF:
			mode = 3
		}
		s.emit(Inst{Op: roundOp, D: R(RXMM0), S: R(RXMM0), Imm: mode})
		s.emit(Inst{Op: movOpFrom, D: R(RRAX), S: R(RXMM0), Sz: sz})
		s.storeReg(RRAX, in.Result)
		return nil
	}
	return todo("float intrinsic %s", in.Op)
}

func (s *sel) selLoad(in *vir.Instruction) error {
	t := s.types[in.Result]
	w := widthOf(t)
	vol := in.Op == vir.OpLoadVol
	_ = vol
	s.loadOperand(in.Args[0], RRCX)
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Mem(RRCX, 0), Sz: w})
	s.maskTo(RRAX, intBits(t))
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selStore(in *vir.Instruction) error {
	w := widthOf(in.Suffix)
	s.loadOperand(in.Args[0], RRCX)
	s.loadOperand(in.Args[1], RRAX)
	s.emit(Inst{Op: "mov", D: Mem(RRCX, 0), S: R(RRAX), Sz: w})
	return nil
}

func (s *sel) selField(in *vir.Instruction) error {
	structName := in.Args[1].Ident
	field := in.Args[2].Ident
	off, err := s.l.FieldOffset(structName, field)
	if err != nil {
		return err
	}
	s.loadOperand(in.Args[0], RRAX)
	if off != 0 {
		s.emit(Inst{Op: "add", D: R(RRAX), S: Imm(off), Sz: 8})
	}
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selIndex(in *vir.Instruction) error {
	elem := in.Args[1].Type
	es, err := s.l.Size(elem)
	if err != nil {
		return err
	}
	s.loadOperand(in.Args[0], RRAX)
	s.loadOperand(in.Args[2], RRCX)
	if es == 1 || es == 2 || es == 4 || es == 8 {
		s.emit(Inst{Op: "lea", D: R(RRAX), S: MemIndexed(RRAX, RRCX, byte(es), 0)})
	} else {
		s.emit(Inst{Op: "imul3", D: R(RRCX), S: R(RRCX), Imm: es, Sz: 8})
		s.emit(Inst{Op: "add", D: R(RRAX), S: R(RRCX), Sz: 8})
	}
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selAlloca(in *vir.Instruction) error {
	if _, ok := in.Suffix.(vir.ValistType); ok {
		s.emit(Inst{Op: "sub", D: R(RRSP), S: Imm(32), Sz: 8})
		s.emit(Inst{Op: "mov", D: R(RRAX), S: R(RRSP), Sz: 8})
		s.storeReg(RRAX, in.Result)
		return nil
	}
	s.loadOperand(in.Args[0], RRAX)
	s.emit(Inst{Op: "add", D: R(RRAX), S: Imm(15), Sz: 8})
	s.emit(Inst{Op: "and", D: R(RRAX), S: Imm(^int64(15)), Sz: 8})
	s.emit(Inst{Op: "sub", D: R(RRSP), S: R(RRAX), Sz: 8})
	s.emit(Inst{Op: "mov", D: R(RRAX), S: R(RRSP), Sz: 8})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selBulk(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpMemset:
		s.loadOperand(in.Args[0], RRDI)
		s.loadOperand(in.Args[1], RRAX)
		s.loadOperand(in.Args[2], RRCX)
		s.emit(Inst{Op: "rep_stosb"})
		return nil
	case vir.OpMemcopy:
		s.loadOperand(in.Args[0], RRDI)
		s.loadOperand(in.Args[1], RRSI)
		s.loadOperand(in.Args[2], RRCX)
		s.emit(Inst{Op: "cld"})
		s.emit(Inst{Op: "rep_movsb"})
		return nil
	case vir.OpMemmove:
		return s.selMemmove(in)
	}
	return errBadModule("unexpected bulk op")
}

func (s *sel) selMemmove(in *vir.Instruction) error {
	fwd := ".Lmove.fwd." + uniq(s)
	done := ".Lmove.done." + uniq(s)

	s.loadOperand(in.Args[0], RRDI)
	s.loadOperand(in.Args[1], RRSI)
	s.loadOperand(in.Args[2], RRCX)

	s.emit(Inst{Op: "cmp", D: R(RRDI), S: R(RRSI), Sz: 8})
	s.emit(Inst{Op: "jcc", CC: CondBE, Lbl: fwd})

	s.emit(Inst{Op: "lea", D: R(RRDI), S: MemIndexed(RRDI, RRCX, 1, -1)})
	s.emit(Inst{Op: "lea", D: R(RRSI), S: MemIndexed(RRSI, RRCX, 1, -1)})
	s.emit(Inst{Op: "std"})
	s.emit(Inst{Op: "rep_movsb"})
	s.emit(Inst{Op: "cld"})
	s.emit(Inst{Op: "jmp", Lbl: done})

	s.emit(Inst{Op: "label", Lbl: fwd})
	s.emit(Inst{Op: "cld"})
	s.emit(Inst{Op: "rep_movsb"})

	s.emit(Inst{Op: "label", Lbl: done})
	return nil
}

func (s *sel) selAtomic(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpFence:
		s.emit(Inst{Op: "mfence"})
		return nil
	case vir.OpAtomicLoad:
		s.loadOperand(in.Args[0], RRCX)
		s.emit(Inst{Op: "mov", D: R(RRAX), S: Mem(RRCX, 0), Sz: widthOf(s.types[in.Result])})
		s.storeReg(RRAX, in.Result)
		return nil
	case vir.OpAtomicStore:
		w := widthOf(in.Suffix)
		s.loadOperand(in.Args[0], RRCX)
		s.loadOperand(in.Args[1], RRAX)
		s.emit(Inst{Op: "xchg", D: Mem(RRCX, 0), S: R(RRAX), Sz: w})
		return nil
	case vir.OpAtomicAdd:
		s.loadOperand(in.Args[0], RRCX)
		s.loadOperand(in.Args[1], RRAX)
		s.emit(Inst{Op: "lock_xadd", D: Mem(RRCX, 0), S: R(RRAX), Sz: widthOf(s.types[in.Result])})
		s.storeReg(RRAX, in.Result)
		return nil
	case vir.OpAtomicSub:
		w := widthOf(s.types[in.Result])
		s.loadOperand(in.Args[0], RRCX)
		s.loadOperand(in.Args[1], RRAX)
		s.emit(Inst{Op: "neg", S: R(RRAX), Sz: w})
		s.emit(Inst{Op: "lock_xadd", D: Mem(RRCX, 0), S: R(RRAX), Sz: w})
		s.storeReg(RRAX, in.Result)
		return nil
	case vir.OpAtomicXchg:
		w := widthOf(s.types[in.Result])
		s.loadOperand(in.Args[0], RRCX) 
		s.loadOperand(in.Args[1], RRAX) 
		s.emit(Inst{Op: "xchg", D: Mem(RRCX, 0), S: R(RRAX), Sz: w})
		s.storeReg(RRAX, in.Result) 
		return nil
	case vir.OpAtomicAnd:
		return s.selAtomicRMW(in, "and")
	case vir.OpAtomicOr:
		return s.selAtomicRMW(in, "or")
	case vir.OpAtomicXor:
		return s.selAtomicRMW(in, "xor")
	case vir.OpCmpxchg:
		w := widthOf(s.types[in.Result])
		s.loadOperand(in.Args[0], RRDX) 
		s.loadOperand(in.Args[1], RRAX) 
		s.loadOperand(in.Args[2], RRCX) 
		s.emit(Inst{Op: "lock_cmpxchg", D: Mem(RRDX, 0), S: R(RRCX), Sz: w})
		s.storeReg(RRAX, in.Result)
		return nil
	default:
		return todo("atomic %s", in.Op)
	}
}

func (s *sel) selAtomicRMW(in *vir.Instruction, aluOp string) error {
	w := widthOf(s.types[in.Result])
	retry := ".Lrmw.retry." + uniq(s)

	s.loadOperand(in.Args[0], RRDX)
	s.loadOperand(in.Args[1], RR11)

	s.emit(Inst{Op: "label", Lbl: retry})
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Mem(RRDX, 0), Sz: w})
	s.emit(Inst{Op: "mov", D: R(RRCX), S: R(RRAX), Sz: 8})
	s.emit(Inst{Op: aluOp, D: R(RRCX), S: R(RR11), Sz: w})
	s.emit(Inst{Op: "lock_cmpxchg", D: Mem(RRDX, 0), S: R(RRCX), Sz: w})
	s.emit(Inst{Op: "jcc", CC: CondNE, Lbl: retry})

	s.maskTo(RRAX, intBits(s.types[in.Result]))
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selPopcnt(in *vir.Instruction) error {
	if !s.tiers["popcnt"] && !s.tiers["sse4.2"] {
		return errBadModule("popcnt requires a popcnt/sse4.2 target tier")
	}
	w := widthOf(s.types[in.Result])
	s.loadOperand(in.Args[0], RRAX)
	s.emit(Inst{Op: "popcnt", D: R(RRAX), S: R(RRAX), Sz: max2(w, 4)})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selBitScan(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	cw := 4
	if bits > 32 {
		cw = 8
	}
	s.loadOperand(in.Args[0], RRAX)

	if in.Op == vir.OpCttz {
		s.emit(Inst{Op: "bsf", D: R(RRCX), S: R(RRAX), Sz: cw}) 
		s.emit(Inst{Op: "mov", D: R(RRDX), S: Imm(int64(bits)), Sz: 8})
		s.emit(Inst{Op: "cmovcc", D: R(RRCX), S: R(RRDX), CC: CondE, Sz: cw})
		s.maskTo(RRCX, bits)
		s.storeReg(RRCX, in.Result)
		return nil
	}

	s.emit(Inst{Op: "bsr", D: R(RRCX), S: R(RRAX), Sz: cw}) 
	s.emit(Inst{Op: "not", S: R(RRCX), Sz: cw})             
	s.emit(Inst{Op: "lea", D: R(RRAX), S: Mem(RRCX, int32(bits))}) 
	s.emit(Inst{Op: "mov", D: R(RRDX), S: Imm(int64(bits)), Sz: 8})
	s.emit(Inst{Op: "cmovcc", D: R(RRAX), S: R(RRDX), CC: CondE, Sz: cw})
	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selBswap(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	if bits == 8 {
		return errBadModule("bswap illegal on i8")
	}
	w := widthOf(t)
	if w != 4 && w != 8 {
		return todo("bswap i%d", bits)
	}
	s.loadOperand(in.Args[0], RRAX)
	s.emit(Inst{Op: "bswap", D: R(RRAX), Sz: w})
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) selBitrev(in *vir.Instruction) error {
	t := s.types[in.Result]
	bits := intBits(t)
	w := widthOf(t)

	s.loadOperand(in.Args[0], RRAX)

	if w > 1 {
		s.emit(Inst{Op: "bswap", D: R(RRAX), Sz: w})
	}

	s.emit(Inst{Op: "mov", D: R(RRCX), S: R(RRAX), Sz: w})
	s.emit(Inst{Op: "mov", D: R(RR11), S: Imm(int64(0x0F0F0F0F0F0F0F0F)), Sz: 8})
	s.emit(Inst{Op: "and", D: R(RRAX), S: R(RR11), Sz: w})
	s.emit(Inst{Op: "shl", D: R(RRAX), S: Imm(4), Sz: w})
	s.emit(Inst{Op: "not", S: R(RR11), Sz: w})
	s.emit(Inst{Op: "and", D: R(RRCX), S: R(RR11), Sz: w})
	s.emit(Inst{Op: "shr", D: R(RRCX), S: Imm(4), Sz: w})
	s.emit(Inst{Op: "or", D: R(RRAX), S: R(RRCX), Sz: w})

	s.emit(Inst{Op: "mov", D: R(RRCX), S: R(RRAX), Sz: w})
	s.emit(Inst{Op: "mov", D: R(RR11), S: Imm(int64(0x3333333333333333)), Sz: 8})
	s.emit(Inst{Op: "and", D: R(RRAX), S: R(RR11), Sz: w})
	s.emit(Inst{Op: "shl", D: R(RRAX), S: Imm(2), Sz: w})
	s.emit(Inst{Op: "not", S: R(RR11), Sz: w})
	s.emit(Inst{Op: "and", D: R(RRCX), S: R(RR11), Sz: w})
	s.emit(Inst{Op: "shr", D: R(RRCX), S: Imm(2), Sz: w})
	s.emit(Inst{Op: "or", D: R(RRAX), S: R(RRCX), Sz: w})

	s.emit(Inst{Op: "mov", D: R(RRCX), S: R(RRAX), Sz: w})
	s.emit(Inst{Op: "mov", D: R(RR11), S: Imm(int64(0x5555555555555555)), Sz: 8})
	s.emit(Inst{Op: "and", D: R(RRAX), S: R(RR11), Sz: w})
	s.emit(Inst{Op: "shl", D: R(RRAX), S: Imm(1), Sz: w})
	s.emit(Inst{Op: "not", S: R(RR11), Sz: w})
	s.emit(Inst{Op: "and", D: R(RRCX), S: R(RR11), Sz: w})
	s.emit(Inst{Op: "shr", D: R(RRCX), S: Imm(1), Sz: w})
	s.emit(Inst{Op: "or", D: R(RRAX), S: R(RRCX), Sz: w})

	s.maskTo(RRAX, bits)
	s.storeReg(RRAX, in.Result)
	return nil
}

func (s *sel) operandType(o vir.Operand) vir.Type {
	if o.Kind == vir.OperandIdent {
		if t, ok := s.types[o.Ident]; ok {
			return t
		}
		if g, ok := s.ix.globals[o.Ident]; ok {
			_ = g
			return vir.Ptr
		}
	}
	return vir.I64
}

func intBits(t vir.Type) int {
	if x, ok := t.(vir.IntType); ok {
		return x.Bits
	}
	return 64
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}