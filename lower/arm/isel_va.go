// isel_va.go
package arm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// This backend's valist is a single pointer to the next variadic
// argument — the AAPCS va_list exactly, and a fraction of the x86-64
// backend's 24-byte GP/overflow/save-area struct. It can be that small
// because the prologue pushes r0-r3 immediately below the incoming stack
// arguments (see frame.go), which makes the whole argument list one
// contiguous run of words. There is no register-save-area cursor to keep,
// because there is no boundary to cross.
//
// alloca.valist gives the cursor a stack slot; the valist *value* is that
// slot's address, so va_start/va_arg mutate it in place through the
// pointer, matching how the IR describes them.
func (c *fnLower) selVararg(in *vir.Instruction) error {
	switch in.Op {
	case vir.OpVaStart:
		if !c.f.Variadic {
			return fmt.Errorf("va_start outside a variadic function")
		}
		if len(in.Args) != 2 {
			return fmt.Errorf("va_start needs a valist and the last named parameter")
		}
		// ParamEnd comes from the actual argument layout, not from
		// 8 + 4*(i+1), which is only right when no preceding parameter is
		// byval or 8-byte aligned.
		c.addImm(R0, FP, c.frame.ParamEnd, IP)
		if err := c.into(in.Args[0], R1); err != nil {
			return err
		}
		c.emit(Inst{Op: "str", D: R(R0), M: Mem(R1, 0)})
		return nil

	case vir.OpVaArg:
		w, err := c.width(in)
		if err != nil {
			return err
		}
		if !vir.IsVaArgType(in.Suffix) {
			return fmt.Errorf("va_arg.%s is not a legal destination type (§4.4)", in.Suffix)
		}
		if err := c.into(in.Args[0], R1); err != nil {
			return err
		}
		c.emit(Inst{Op: "ldr", D: R(R0), M: Mem(R1, 0)})
		// Every unnamed argument occupies one whole word, so a narrow
		// type reads the word and masks; the cursor always advances by 4.
		c.emit(Inst{Op: "ldr", D: R(R2), M: MemPost(R0, ArgWordBytes)})
		c.emit(Inst{Op: "str", D: R(R0), M: Mem(R1, 0)})
		c.maskTo(R2, w)
		c.storeSlot(R2, in.Result)
		return nil

	case vir.OpVaEnd:
		// A no-op: the cursor is one pointer into the caller's own
		// argument words and holds no state needing cleanup. It stays
		// mandatory in the IR (§4.4) regardless of that.
		return nil
	}
	return fmt.Errorf("not a variadic-access opcode: %s", in.Op)
}