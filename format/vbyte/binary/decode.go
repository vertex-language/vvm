// decode.go
package binary

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Decode parses .vbyte bytes into an unverified *vir.Module. Callers must run
// vir.Verify before handing the module to anything downstream (README
// invariant 3) — the decoder checks framing, not semantics.
func Decode(data []byte) (m *vir.Module, err error) {
	r := &reader{data: data}
	defer func() {
		if p := recover(); p != nil {
			if de, ok := p.(decodeErr); ok {
				m, err = nil, error(de)
				return
			}
			panic(p)
		}
	}()

	if !bytes.HasPrefix(r.data, magic) {
		return nil, fmt.Errorf("vbyte: bad magic")
	}
	r.pos = len(magic)
	if v := r.b(); v != version {
		return nil, fmt.Errorf("vbyte: unsupported format version %d (have %d)", v, version)
	}

	m = vir.NewModule(r.str())
	if r.b() == 1 {
		t := &vir.Target{Arch: r.str(), OS: r.str(), ABI: r.str()}
		for n := r.u(); n > 0; n-- {
			t.Tiers = append(t.Tiers, r.str())
		}
		m.Target = t
	}

	for n := r.u(); n > 0; n-- {
		s := &vir.Struct{Name: r.str()}
		for k := r.u(); k > 0; k-- {
			s.Fields = append(s.Fields, vir.Field{Name: r.str(), Type: r.typ()})
		}
		m.Structs = append(m.Structs, s)
	}

	for n := r.u(); n > 0; n-- {
		sig := &vir.FunctionSignature{Name: r.str()}
		for k := r.u(); k > 0; k-- {
			sig.Params = append(sig.Params, r.typ())
		}
		sig.Variadic = r.bool()
		sig.Ret = r.typ()
		m.FunctionSignatures = append(m.FunctionSignatures, sig)
	}

	for n := r.u(); n > 0; n-- {
		m.Constants = append(m.Constants, &vir.Constant{Name: r.str(), Type: r.typ(), Value: r.operand()})
	}

	for n := r.u(); n > 0; n-- {
		g := &vir.Global{Name: r.str(), Type: r.typ()}
		g.Export = r.bool()
		g.TLS = r.bool()
		g.Align = int(r.u())
		g.Init = r.init()
		m.Globals = append(m.Globals, g)
	}

	for n := r.u(); n > 0; n-- {
		m.Links = append(m.Links, &vir.Link{Kind: vir.LinkKind(r.str()), Name: r.str()})
	}

	for n := r.u(); n > 0; n-- {
		g := &vir.ExternGroup{Dependency: r.str()}
		for k := r.u(); k > 0; k-- {
			f := &vir.ExternFunction{Name: r.str()}
			f.Params = r.params()
			f.Variadic = r.bool()
			f.Ret = r.typ()
			f.Attrs = r.attrs()
			g.Functions = append(g.Functions, f)
		}
		m.Externs = append(m.Externs, g)
	}

	for n := r.u(); n > 0; n-- {
		f := &vir.Function{Name: r.str()}
		f.Params = r.params()
		f.Ret = r.typ()
		f.Attrs = r.attrs()
		f.Export = r.bool()
		f.Entry = r.block()
		for k := r.u(); k > 0; k-- {
			f.Blocks = append(f.Blocks, r.block())
		}
		m.Functions = append(m.Functions, f)
	}

	if r.pos != len(r.data) {
		return nil, fmt.Errorf("vbyte: %d trailing bytes after module", len(r.data)-r.pos)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// reader — panics with decodeErr internally; Decode converts to error.
// ---------------------------------------------------------------------------

type decodeErr error

type reader struct {
	data []byte
	pos  int
}

func (r *reader) fail(format string, args ...any) {
	panic(decodeErr(fmt.Errorf("vbyte: offset %d: %s", r.pos, fmt.Sprintf(format, args...))))
}

func (r *reader) b() byte {
	if r.pos >= len(r.data) {
		r.fail("unexpected end of input")
	}
	v := r.data[r.pos]
	r.pos++
	return v
}

func (r *reader) bool() bool { return r.b() != 0 }

func (r *reader) u() uint64 {
	v, n := binary.Uvarint(r.data[r.pos:])
	if n <= 0 {
		r.fail("bad uvarint")
	}
	r.pos += n
	return v
}

func (r *reader) s() int64 {
	v, n := binary.Varint(r.data[r.pos:])
	if n <= 0 {
		r.fail("bad varint")
	}
	r.pos += n
	return v
}

func (r *reader) str() string {
	n := int(r.u())
	if n < 0 || r.pos+n > len(r.data) {
		r.fail("string length %d exceeds input", n)
	}
	s := string(r.data[r.pos : r.pos+n])
	r.pos += n
	return s
}

func (r *reader) f64() float64 { return math.Float64frombits(r.u()) }

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

func (r *reader) typ() vir.Type {
	switch tag := r.b(); tag {
	case tagNilType:
		return nil
	case tagInt:
		return vir.IntType{Bits: int(r.u())}
	case tagFloat:
		return vir.FloatType{Bits: int(r.u())}
	case tagPtr:
		return vir.Ptr
	case tagVoid:
		return vir.Void
	case tagVec:
		e := r.typ()
		return vir.VecType{Elem: e, Len: int(r.u())}
	case tagStruct:
		return vir.StructType{Name: r.str()}
	case tagArray:
		e := r.typ()
		return vir.ArrayType{Elem: e, Len: int(r.u())}
	default:
		r.fail("unknown type tag %d", tag)
		return nil
	}
}

// ---------------------------------------------------------------------------
// Operands
// ---------------------------------------------------------------------------

func (r *reader) operand() vir.Operand {
	kind := vir.OperandKind(r.b())
	o := vir.Operand{Kind: kind}
	switch kind {
	case vir.OperandIdent:
		o.Ident = r.str()
	case vir.OperandInt:
		o.Int = r.s()
	case vir.OperandFloat:
		o.Float = r.f64()
	case vir.OperandString:
		o.Str = r.str()
	case vir.OperandBool:
		o.Bool = r.bool()
	case vir.OperandNull:
	case vir.OperandType:
		o.Type = r.typ()
	case vir.OperandOrdering:
		o.Ordering = r.str()
	case vir.OperandVector:
		for n := r.u(); n > 0; n-- {
			o.Vector = append(o.Vector, r.s())
		}
	default:
		r.fail("unknown operand kind %d", kind)
	}
	return o
}

// ---------------------------------------------------------------------------
// ConstInit
// ---------------------------------------------------------------------------

func (r *reader) init() vir.ConstInit {
	switch tag := r.b(); tag {
	case tagInitNil:
		return nil
	case tagInitLiteral:
		return vir.InitLiteral{Value: r.operand()}
	case tagInitZero:
		return vir.InitZero{}
	case tagInitAddressOf:
		return vir.InitAddressOf{Name: r.str()}
	case tagInitAggregate:
		var elems []vir.ConstInit
		for n := r.u(); n > 0; n-- {
			elems = append(elems, r.init())
		}
		return vir.InitAggregate{Elems: elems}
	case tagInitByteString:
		return vir.InitByteString{Data: []byte(r.str())}
	default:
		r.fail("unknown init tag %d", tag)
		return nil
	}
}

// ---------------------------------------------------------------------------
// Params / attrs
// ---------------------------------------------------------------------------

func (r *reader) params() []vir.Param {
	var out []vir.Param
	for n := r.u(); n > 0; n-- {
		out = append(out, vir.Param{Name: r.str(), Type: r.typ(), ByVal: r.str(), SRet: r.str()})
	}
	return out
}

func (r *reader) attrs() []vir.FunctionAttribute {
	var out []vir.FunctionAttribute
	for n := r.u(); n > 0; n-- {
		out = append(out, vir.FunctionAttribute(r.str()))
	}
	return out
}

// ---------------------------------------------------------------------------
// Blocks / body lines / instructions
// ---------------------------------------------------------------------------

func (r *reader) block() *vir.Block {
	b := &vir.Block{Label: r.str()}
	for n := r.u(); n > 0; n-- {
		b.Lines = append(b.Lines, r.bodyLine())
	}
	b.Term = r.term()
	return b
}

func (r *reader) bodyLine() vir.BodyLine {
	switch tag := r.b(); tag {
	case tagBodyInstruction:
		i := r.instruction()
		return vir.BodyLine{Instruction: &i}
	case tagBodyAsm:
		a := r.asmBlock()
		return vir.BodyLine{Asm: &a}
	default:
		r.fail("unknown body line tag %d", tag)
		return vir.BodyLine{}
	}
}

func (r *reader) instruction() vir.Instruction {
	i := vir.Instruction{Result: r.str(), Op: r.str(), Suffix: r.typ(), Sig: r.str()}
	i.Align = int(r.u())
	for k := r.u(); k > 0; k-- {
		i.Args = append(i.Args, r.operand())
	}
	return i
}

// ---------------------------------------------------------------------------
// Inline assembly
// ---------------------------------------------------------------------------

func (r *reader) asmBlock() vir.AsmBlock {
	a := vir.AsmBlock{Dialect: vir.AsmDialect(r.str())}
	for n := r.u(); n > 0; n-- {
		a.Bindings = append(a.Bindings, r.asmBinding())
	}
	for n := r.u(); n > 0; n-- {
		a.Code = append(a.Code, r.asmCodeLine())
	}
	return a
}

func (r *reader) asmBinding() vir.AsmBinding {
	b := vir.AsmBinding{Kind: vir.AsmBindingKind(r.str()), Register: r.str()}
	for n := r.u(); n > 0; n-- {
		b.Registers = append(b.Registers, r.str())
	}
	b.Ident = r.str()
	return b
}

func (r *reader) asmCodeLine() vir.AsmCodeLine {
	if r.b() == 1 {
		return vir.AsmCodeLine{LabelDeclaration: r.str()}
	}
	line := vir.AsmCodeLine{Mnemonic: r.str()}
	for n := r.u(); n > 0; n-- {
		line.Operands = append(line.Operands, r.asmOperand())
	}
	return line
}

func (r *reader) asmOperand() vir.AsmOperand {
	kind := vir.AsmOperandKind(r.str())
	switch kind {
	case vir.AsmOperandKindRegister:
		return vir.AsmOperand{Kind: kind, Register: r.str()}
	case vir.AsmOperandKindImmediate:
		return vir.AsmOperand{Kind: kind, Immediate: r.operand()}
	case vir.AsmOperandKindMemory:
		return vir.AsmOperand{Kind: kind, Memory: r.str()}
	case vir.AsmOperandKindLabel:
		return vir.AsmOperand{Kind: kind, Label: r.str()}
	default:
		r.fail("unknown asm operand kind %q", kind)
		return vir.AsmOperand{}
	}
}

// ---------------------------------------------------------------------------
// Terminators
// ---------------------------------------------------------------------------

func (r *reader) term() vir.Terminator {
	switch tag := r.b(); tag {
	case tagBranch:
		return vir.Branch{Label: r.str()}
	case tagBranchIf:
		return vir.BranchIf{Cond: r.operand(), Then: r.str(), Else: r.str()}
	case tagSwitch:
		sw := vir.Switch{Value: r.operand(), Default: r.str()}
		for n := r.u(); n > 0; n-- {
			sw.Cases = append(sw.Cases, vir.SwitchCase{Value: r.s(), Label: r.str()})
		}
		return sw
	case tagReturn:
		if r.b() == 1 {
			v := r.operand()
			return vir.Return{Value: &v}
		}
		return vir.Return{}
	case tagTailCall:
		tc := vir.TailCall{Callee: r.str(), Sig: r.str()}
		for n := r.u(); n > 0; n-- {
			tc.Args = append(tc.Args, r.operand())
		}
		return tc
	case tagTrap:
		return vir.Trap{}
	case tagUnreachable:
		return vir.Unreachable{}
	default:
		r.fail("unknown terminator tag %d", tag)
		return nil
	}
}