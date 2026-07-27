// isel_va.go
package x86_64

import "github.com/vertex-language/vvm/ir/vir"

// valist layout (this backend's target-defined choice, 24 bytes, GP-only):
//   +0  gp_offset (u32)          byte offset into reg_save_area for next GP
//   +8  overflow_arg_area (ptr)  next stack vararg
//   +16 reg_save_area (ptr)      base of the 6-GP save area
// (XMM fields are omitted; float varargs are a todo.)
const (
	vaGPOffset = 0
	vaOverflow = 8
	vaRegSave  = 16
)

// selVaStart initializes the valist at dst. gp_offset starts past the named
// GP args; overflow points at the first stack vararg; reg_save_area points
// at the prologue's GP save block.
func (s *sel) selVaStart(in *vir.Instruction) error {
	if !s.f.Variadic {
		return errBadModule("va_start in a non-variadic function")
	}
	dst := in.Args[0]
	s.loadOperand(dst, RRCX) // valist pointer

	s.emit(Inst{Op: "mov", D: Mem(RRCX, vaGPOffset), S: Imm(int64(s.fr.NamedGP) * 8), Sz: 4})

	s.emit(Inst{Op: "lea", D: R(RRAX), S: Mem(RRBP, int32(s.fr.ParamEnd))})
	s.emit(Inst{Op: "mov", D: Mem(RRCX, vaOverflow), S: R(RRAX), Sz: 8})

	s.emit(Inst{Op: "lea", D: R(RRAX), S: Mem(RRBP, int32(s.fr.SaveArea))})
	s.emit(Inst{Op: "mov", D: Mem(RRCX, vaRegSave), S: R(RRAX), Sz: 8})
	return nil
}

// selVaArg reads the next argument. GP/ptr only: if gp_offset < 48, read
// from reg_save_area+gp_offset and advance by 8; else read from the overflow
// area and advance it by 8.
func (s *sel) selVaArg(in *vir.Instruction) error {
	t := s.types[in.Result]
	if vir.IsFloat(t) || vir.IsVec(t) {
		return todo("va_arg of float/vector")
	}
	w := widthOf(t)
	s.loadOperand(in.Args[0], RRCX) // valist pointer

	fromMem := ".Lva.mem." + uniq(s)
	done := ".Lva.done." + uniq(s)

	// gp_offset in edx
	s.emit(Inst{Op: "mov", D: R(RRDX), S: Mem(RRCX, vaGPOffset), Sz: 4})
	s.emit(Inst{Op: "cmp", D: R(RRDX), S: Imm(48), Sz: 4})
	s.emit(Inst{Op: "jcc", CC: CondAE, Lbl: fromMem})

	// register path: addr = reg_save_area + gp_offset
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Mem(RRCX, vaRegSave), Sz: 8})
	s.emit(Inst{Op: "add", D: R(RRAX), S: R(RRDX), Sz: 8})
	s.emit(Inst{Op: "add", D: R(RRDX), S: Imm(8), Sz: 4})
	s.emit(Inst{Op: "mov", D: Mem(RRCX, vaGPOffset), S: R(RRDX), Sz: 4})
	s.emit(Inst{Op: "jmp", Lbl: done})

	// overflow path
	s.emit(Inst{Op: "label", Lbl: fromMem})
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Mem(RRCX, vaOverflow), Sz: 8})
	s.emit(Inst{Op: "lea", D: R(RRDX), S: Mem(RRAX, 8)})
	s.emit(Inst{Op: "mov", D: Mem(RRCX, vaOverflow), S: R(RRDX), Sz: 8})

	s.emit(Inst{Op: "label", Lbl: done})
	// rax now holds the argument's address; load the value.
	s.emit(Inst{Op: "mov", D: R(RRAX), S: Mem(RRAX, 0), Sz: w})
	s.maskTo(RRAX, intBits(t))
	s.storeReg(RRAX, in.Result)
	return nil
}

var vaCounter int

func uniq(s *sel) string {
	vaCounter++
	return s.f.Name + itoa(vaCounter)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}