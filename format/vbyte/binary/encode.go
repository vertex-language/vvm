// encode.go
// Package binary implements .vbyte, the frontend boundary (README arrow 1).
// The format is a tagged, varint-heavy serialization of vir.Module: pre-parsed
// and portable, with no textual re-lexing needed on load.
package binary

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Format header: magic + one format-version byte. Bump the version on any
// incompatible layout change. v2 added inline-asm body lines (BodyLine) and
// tracked the ir/vir rename from Fn/Const/Inst-style names. v3 moves
// AsmDialect from a per-asm-block field to a module-scoped header field
// (§1.2 rule 11), matching the module.go/ir.md update.
var magic = []byte{'V', 'B', 'Y', 'T'}

const version = 3

// Encode serializes an (assumed verified) module to .vbyte bytes.
func Encode(m *vir.Module) ([]byte, error) {
	w := &writer{}
	w.raw(magic)
	w.b(version)

	w.str(m.Name)
	if m.Target != nil {
		w.b(1)
		w.str(m.Target.Arch)
		w.str(m.Target.OS)
		w.str(m.Target.ABI)
		w.u(uint64(len(m.Target.Tiers)))
		for _, t := range m.Target.Tiers {
			w.str(t)
		}
	} else {
		w.b(0)
	}

	if m.AsmDialect != nil {
		w.b(1)
		w.str(string(*m.AsmDialect))
	} else {
		w.b(0)
	}

	w.u(uint64(len(m.Structs)))
	for _, s := range m.Structs {
		w.str(s.Name)
		w.u(uint64(len(s.Fields)))
		for _, f := range s.Fields {
			w.str(f.Name)
			w.typ(f.Type)
		}
	}

	w.u(uint64(len(m.FunctionSignatures)))
	for _, sig := range m.FunctionSignatures {
		w.str(sig.Name)
		w.u(uint64(len(sig.Params))) // []Type, not []Param
		for _, p := range sig.Params {
			w.typ(p)
		}
		w.bool(sig.Variadic)
		w.typ(sig.Ret)
	}

	w.u(uint64(len(m.Constants)))
	for _, c := range m.Constants {
		w.str(c.Name)
		w.typ(c.Type)
		w.operand(c.Value)
	}

	w.u(uint64(len(m.Globals)))
	for _, g := range m.Globals {
		w.str(g.Name)
		w.typ(g.Type)
		w.bool(g.Export)
		w.bool(g.TLS)
		w.u(uint64(g.Align))
		w.init(g.Init)
	}

	w.u(uint64(len(m.Links)))
	for _, l := range m.Links {
		w.str(string(l.Kind))
		w.str(l.Name)
	}

	w.u(uint64(len(m.Externs)))
	for _, g := range m.Externs {
		w.str(g.Dependency)
		w.u(uint64(len(g.Functions)))
		for _, f := range g.Functions {
			w.str(f.Name)
			w.params(f.Params)
			w.bool(f.Variadic)
			w.typ(f.Ret)
			w.attrs(f.Attrs)
		}
	}

	w.u(uint64(len(m.Functions)))
	for _, f := range m.Functions {
		w.str(f.Name)
		w.params(f.Params)
		w.typ(f.Ret)
		w.attrs(f.Attrs)
		w.bool(f.Export)
		w.block(f.Entry)
		w.u(uint64(len(f.Blocks)))
		for _, b := range f.Blocks {
			w.block(b)
		}
	}
	return w.buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// writer
// ---------------------------------------------------------------------------

type writer struct{ buf bytes.Buffer }

func (w *writer) raw(p []byte) { w.buf.Write(p) }
func (w *writer) b(v byte)     { w.buf.WriteByte(v) }
func (w *writer) bool(v bool) {
	if v {
		w.b(1)
	} else {
		w.b(0)
	}
}
func (w *writer) u(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	w.buf.Write(tmp[:n])
}
func (w *writer) s(v int64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutVarint(tmp[:], v)
	w.buf.Write(tmp[:n])
}
func (w *writer) str(s string) {
	w.u(uint64(len(s)))
	w.buf.WriteString(s)
}
func (w *writer) f64(v float64) { w.u(math.Float64bits(v)) }

// ---------------------------------------------------------------------------
// Types (types.go)
// ---------------------------------------------------------------------------

const (
	tagNilType byte = iota
	tagInt
	tagFloat
	tagPtr
	tagVoid
	tagVec
	tagStruct
	tagArray
)

func (w *writer) typ(t vir.Type) {
	switch x := t.(type) {
	case nil:
		w.b(tagNilType)
	case vir.IntType:
		w.b(tagInt)
		w.u(uint64(x.Bits))
	case vir.FloatType:
		w.b(tagFloat)
		w.u(uint64(x.Bits))
	case vir.PtrType:
		w.b(tagPtr)
	case vir.VoidType:
		w.b(tagVoid)
	case vir.VecType:
		w.b(tagVec)
		w.typ(x.Elem)
		w.u(uint64(x.Len))
	case vir.StructType:
		w.b(tagStruct)
		w.str(x.Name)
	case vir.ArrayType:
		w.b(tagArray)
		w.typ(x.Elem)
		w.u(uint64(x.Len))
	default:
		panic(fmt.Sprintf("vbyte: unknown Type %T", t))
	}
}

// ---------------------------------------------------------------------------
// Operands (operand.go)
// ---------------------------------------------------------------------------

func (w *writer) operand(o vir.Operand) {
	w.b(byte(o.Kind))
	switch o.Kind {
	case vir.OperandIdent:
		w.str(o.Ident)
	case vir.OperandInt:
		w.s(o.Int)
	case vir.OperandFloat:
		w.f64(o.Float)
	case vir.OperandString:
		w.str(o.Str)
	case vir.OperandBool:
		w.bool(o.Bool)
	case vir.OperandNull:
	case vir.OperandType:
		w.typ(o.Type)
	case vir.OperandOrdering:
		w.str(o.Ordering)
	case vir.OperandVector:
		w.u(uint64(len(o.Vector)))
		for _, v := range o.Vector {
			w.s(v)
		}
	default:
		panic(fmt.Sprintf("vbyte: unknown OperandKind %d", o.Kind))
	}
}

// ---------------------------------------------------------------------------
// ConstInit (module.go §8)
// ---------------------------------------------------------------------------

const (
	tagInitNil byte = iota // ConstInit == nil (unverified/incomplete module)
	tagInitLiteral
	tagInitZero
	tagInitAddressOf
	tagInitAggregate
	tagInitByteString
)

func (w *writer) init(i vir.ConstInit) {
	if i == nil {
		w.b(tagInitNil)
		return
	}
	switch x := i.(type) {
	case vir.InitLiteral:
		w.b(tagInitLiteral)
		w.operand(x.Value)
	case vir.InitZero:
		w.b(tagInitZero)
	case vir.InitAddressOf:
		w.b(tagInitAddressOf)
		w.str(x.Name)
	case vir.InitAggregate:
		w.b(tagInitAggregate)
		w.u(uint64(len(x.Elems)))
		for _, e := range x.Elems {
			w.init(e)
		}
	case vir.InitByteString:
		w.b(tagInitByteString)
		w.str(string(x.Data))
	default:
		panic(fmt.Sprintf("vbyte: unknown ConstInit %T", i))
	}
}

// ---------------------------------------------------------------------------
// Params / attrs (module.go)
// ---------------------------------------------------------------------------

func (w *writer) params(ps []vir.Param) {
	w.u(uint64(len(ps)))
	for _, p := range ps {
		w.str(p.Name)
		w.typ(p.Type)
		w.str(p.ByVal)
		w.str(p.SRet)
	}
}

func (w *writer) attrs(as []vir.FunctionAttribute) {
	w.u(uint64(len(as)))
	for _, a := range as {
		w.str(string(a))
	}
}

// ---------------------------------------------------------------------------
// Blocks / body lines / instructions (module.go)
// ---------------------------------------------------------------------------

const (
	tagBodyInstruction byte = iota
	tagBodyAsm
)

func (w *writer) block(b *vir.Block) {
	w.str(b.Label)
	w.u(uint64(len(b.Lines)))
	for _, ln := range b.Lines {
		w.bodyLine(ln)
	}
	w.term(b.Term)
}

func (w *writer) bodyLine(ln vir.BodyLine) {
	switch {
	case ln.Instruction != nil:
		w.b(tagBodyInstruction)
		w.instruction(*ln.Instruction)
	case ln.Asm != nil:
		w.b(tagBodyAsm)
		w.asmBlock(*ln.Asm)
	default:
		panic("vbyte: body line has neither Instruction nor Asm set")
	}
}

func (w *writer) instruction(i vir.Instruction) {
	w.str(i.Result)
	w.str(i.Op.String())
	w.typ(i.Suffix)
	w.str(i.Sig)
	w.u(uint64(i.Align))
	w.u(uint64(len(i.Args)))
	for _, a := range i.Args {
		w.operand(a)
	}
}

// ---------------------------------------------------------------------------
// Inline assembly (module.go §4). The block's dialect is no longer encoded
// here: it comes from the module-scoped AsmDialect header field (see
// Encode above), per §1.2 rule 11.
// ---------------------------------------------------------------------------

func (w *writer) asmBlock(a vir.AsmBlock) {
	w.u(uint64(len(a.Bindings)))
	for _, bind := range a.Bindings {
		w.asmBinding(bind)
	}
	w.u(uint64(len(a.Code)))
	for _, line := range a.Code {
		w.asmCodeLine(line)
	}
}

func (w *writer) asmBinding(b vir.AsmBinding) {
	w.str(string(b.Kind))
	w.str(b.Register)
	w.u(uint64(len(b.Registers)))
	for _, r := range b.Registers {
		w.str(r)
	}
	w.str(b.Ident)
}

func (w *writer) asmCodeLine(l vir.AsmCodeLine) {
	if l.LabelDeclaration != "" {
		w.b(1)
		w.str(l.LabelDeclaration)
		return
	}
	w.b(0)
	w.str(l.Mnemonic)
	w.u(uint64(len(l.Operands)))
	for _, op := range l.Operands {
		w.asmOperand(op)
	}
}

func (w *writer) asmOperand(o vir.AsmOperand) {
	w.str(string(o.Kind))
	switch o.Kind {
	case vir.AsmOperandKindRegister:
		w.str(o.Register)
	case vir.AsmOperandKindImmediate:
		w.operand(o.Immediate)
	case vir.AsmOperandKindMemory:
		w.str(o.Memory)
	case vir.AsmOperandKindLabel:
		w.str(o.Label)
	default:
		panic(fmt.Sprintf("vbyte: unknown AsmOperandKind %q", o.Kind))
	}
}

// ---------------------------------------------------------------------------
// Terminators (module.go §5)
// ---------------------------------------------------------------------------

const (
	tagBranch byte = iota
	tagBranchIf
	tagSwitch
	tagReturn
	tagTailCall
	tagTrap
	tagUnreachable
)

func (w *writer) term(t vir.Terminator) {
	switch x := t.(type) {
	case vir.Branch:
		w.b(tagBranch)
		w.str(x.Label)
	case vir.BranchIf:
		w.b(tagBranchIf)
		w.operand(x.Cond)
		w.str(x.Then)
		w.str(x.Else)
	case vir.Switch:
		w.b(tagSwitch)
		w.operand(x.Value)
		w.str(x.Default)
		w.u(uint64(len(x.Cases)))
		for _, c := range x.Cases {
			w.s(c.Value)
			w.str(c.Label)
		}
	case vir.Return:
		w.b(tagReturn)
		if x.Value != nil {
			w.b(1)
			w.operand(*x.Value)
		} else {
			w.b(0)
		}
	case vir.TailCall:
		w.b(tagTailCall)
		w.str(x.Callee)
		w.str(x.Sig)
		w.u(uint64(len(x.Args)))
		for _, a := range x.Args {
			w.operand(a)
		}
	case vir.Trap:
		w.b(tagTrap)
	case vir.Unreachable:
		w.b(tagUnreachable)
	default:
		panic(fmt.Sprintf("vbyte: unknown Terminator %T", t))
	}
}