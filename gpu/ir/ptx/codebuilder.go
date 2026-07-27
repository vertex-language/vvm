package ptx

import (
	"fmt"
	"strconv"
)

// CodeBuilder is an append-only instruction body. Every emit method
// returns the receiver so calls can be chained. Instructions are stored
// as typed values, so the printer is a dumb serializer.
type CodeBuilder struct {
	Regs   *RegFile
	Items  []CodeItem
	Locals VariableList // function-scope .shared / .local variables

	pendingGuard string
	labelNames   map[string]int
	btCount      int
}

// NewCodeBuilder returns an empty body with a fresh register file.
func NewCodeBuilder() *CodeBuilder {
	return &CodeBuilder{Regs: newRegFile(), labelNames: map[string]int{}}
}

// emit appends one instruction, consuming any pending guard.
func (cb *CodeBuilder) emit(opcode string, ops ...Operand) *CodeBuilder {
	ins := &Instruction{Guard: cb.pendingGuard, Opcode: opcode, Operands: ops}
	cb.pendingGuard = ""
	cb.Items = append(cb.Items, ins)
	return cb
}

// ---- Guards ----------------------------------------------------------

// Guard predicates the next emitted instruction: @%p ...
func (cb *CodeBuilder) Guard(p Reg) *CodeBuilder {
	cb.pendingGuard = "@" + p.Text()
	return cb
}

// GuardNot predicates the next emitted instruction on !p: @!%p ...
func (cb *CodeBuilder) GuardNot(p Reg) *CodeBuilder {
	cb.pendingGuard = "@!" + p.Text()
	return cb
}

// ---- Labels & branching ---------------------------------------------

// NewLabel creates a fresh label; duplicate names are made unique.
func (cb *CodeBuilder) NewLabel(name string) *Label {
	n := cb.labelNames[name]
	cb.labelNames[name] = n + 1
	if n > 0 {
		name = name + "_" + strconv.Itoa(n)
	}
	return &Label{name: name}
}

// Bind places a label at the current position.
func (cb *CodeBuilder) Bind(l *Label) *CodeBuilder {
	cb.Items = append(cb.Items, &LabelBind{Label: l})
	return cb
}

// Bra emits an unconditional branch (honors a pending guard).
func (cb *CodeBuilder) Bra(l *Label) *CodeBuilder { return cb.emit("bra", l) }

// Goto is an alias for Bra.
func (cb *CodeBuilder) Goto(l *Label) *CodeBuilder { return cb.Bra(l) }

// BraIf emits "@%p bra l;".
func (cb *CodeBuilder) BraIf(p Reg, l *Label) *CodeBuilder { return cb.Guard(p).Bra(l) }

// BraIfNot emits "@!%p bra l;".
func (cb *CodeBuilder) BraIfNot(p Reg, l *Label) *CodeBuilder { return cb.GuardNot(p).Bra(l) }

// BrxIdx emits an indexed branch with its .branchtargets directive.
func (cb *CodeBuilder) BrxIdx(idx Reg, targets []*Label) *CodeBuilder {
	name := "$Ltargets" + strconv.Itoa(cb.btCount)
	cb.btCount++
	cb.Items = append(cb.Items, &BranchTargets{Name: name, Targets: targets})
	return cb.emit("brx.idx", idx, Sym(name))
}

// ---- Control ---------------------------------------------------------

func (cb *CodeBuilder) Ret() *CodeBuilder  { return cb.emit("ret") }
func (cb *CodeBuilder) Exit() *CodeBuilder { return cb.emit("exit") }
func (cb *CodeBuilder) Trap() *CodeBuilder { return cb.emit("trap") }
func (cb *CodeBuilder) Nop() *CodeBuilder  { return cb.emit("nop") }

// Call emits a full ABI call: call.uni (rets), fn, (args);
// Param setup (st.param / ld.param) is the caller's responsibility or
// can be done with the typed St/Ld emitters.
func (cb *CodeBuilder) Call(rets []Operand, fn string, args []Operand) *CodeBuilder {
	var ops []Operand
	if len(rets) > 0 {
		ops = append(ops, group(rets))
	}
	ops = append(ops, Sym(fn))
	if len(args) > 0 {
		ops = append(ops, group(args))
	}
	return cb.emit("call.uni", ops...)
}

// ---- Raw escape hatch --------------------------------------------------

// Raw appends a verbatim instruction line. Raw instructions still
// participate in guards and label placement.
func (cb *CodeBuilder) Raw(line string) *CodeBuilder {
	ins := &Instruction{Guard: cb.pendingGuard, Raw: line}
	cb.pendingGuard = ""
	cb.Items = append(cb.Items, ins)
	return cb
}

// Rawf is Raw with fmt.Sprintf formatting.
func (cb *CodeBuilder) Rawf(format string, args ...any) *CodeBuilder {
	return cb.Raw(fmt.Sprintf(format, args...))
}

// ---- Misc --------------------------------------------------------------

// Blank inserts an explicit blank line for readability.
func (cb *CodeBuilder) Blank() *CodeBuilder {
	cb.Items = append(cb.Items, &BlankLine{})
	return cb
}

// Comment attaches a trailing comment to the most recent instruction.
func (cb *CodeBuilder) Comment(s string) *CodeBuilder {
	for i := len(cb.Items) - 1; i >= 0; i-- {
		if ins, ok := cb.Items[i].(*Instruction); ok {
			ins.Comment = s
			break
		}
	}
	return cb
}

// Loc emits a .loc debug directive.
func (cb *CodeBuilder) Loc(fileIdx, line, col int) *CodeBuilder {
	cb.Items = append(cb.Items, &LocDirective{File: fileIdx, Line: line, Col: col})
	return cb
}

// DeclLocal declares a function-scope variable (.shared / .local).
func (cb *CodeBuilder) DeclLocal(v Variable) *CodeBuilder {
	cb.Locals.Add(v)
	return cb
}

// ---- Generic typed cores ------------------------------------------------
// Named wrappers in arith.go / logic.go / mov.go / memory.go / sync.go
// funnel through these.

func (cb *CodeBuilder) op1(base string, t Type, d Reg, a any, opts ...Opt) *CodeBuilder {
	return cb.emit(base+optString(opts)+t.String(), d, toOperand(a))
}

func (cb *CodeBuilder) op2(base string, t Type, d Reg, a, b any, opts ...Opt) *CodeBuilder {
	return cb.emit(base+optString(opts)+t.String(), d, toOperand(a), toOperand(b))
}

func (cb *CodeBuilder) op3(base string, t Type, d Reg, a, b, c any, opts ...Opt) *CodeBuilder {
	return cb.emit(base+optString(opts)+t.String(), d, toOperand(a), toOperand(b), toOperand(c))
}