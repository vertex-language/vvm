// Package binary implements .vbyte, the frontend boundary (README arrow 1).
// The format is a tagged, varint-heavy serialization of vir.Module: pre-parsed
// and portable, with no textual re-lexing needed on load.
package binary

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/vertex-language/vvm/ir/vir"
)

// Format header: magic + one format-version byte. Bump the version on any
// incompatible layout change.
var magic = []byte{'V', 'B', 'Y', 'T'}

const version = 1

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

	w.u(uint64(len(m.Structs)))
	for _, s := range m.Structs {
		w.str(s.Name)
		w.u(uint64(len(s.Fields)))
		for _, f := range s.Fields {
			w.str(f.Name)
			w.typ(f.Type)
		}
	}

	w.u(uint64(len(m.FnSigs)))
	for _, s := range m.FnSigs {
		w.str(s.Name)
		w.u(uint64(len(s.Params)))
		for _, p := range s.Params {
			w.typ(p)
		}
		w.bool(s.Variadic)
		w.typ(s.Ret)
	}

	w.u(uint64(len(m.Consts)))
	for _, c := range m.Consts {
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
		w.str(g.Dep)
		w.u(uint64(len(g.Fns)))
		for _, f := range g.Fns {
			w.str(f.Name)
			w.params(f.Params)
			w.bool(f.Variadic)
			w.typ(f.Ret)
			w.attrs(f.Attrs)
		}
	}

	w.u(uint64(len(m.Funcs)))
	for _, f := range m.Funcs {
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

// Type tags.
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
	}
}

func (w *writer) operand(o vir.Operand) {
	w.b(byte(o.Kind))
	switch o.Kind {
	case vir.OIdent:
		w.str(o.Ident)
	case vir.OInt:
		w.s(o.Int)
	case vir.OFloat:
		w.f64(o.Float)
	case vir.OString:
		w.str(o.Str)
	case vir.OBool:
		w.bool(o.Bool)
	case vir.ONull:
	case vir.OType:
		w.typ(o.Type)
	case vir.OOrdering:
		w.str(o.Ord)
	case vir.OVecLit:
		w.u(uint64(len(o.Vec)))
		for _, v := range o.Vec {
			w.s(v)
		}
	}
}

// ConstInit tags.
const (
	tagInitLit byte = iota
	tagInitZero
	tagInitAddr
	tagInitAgg
	tagInitBytes
)

func (w *writer) init(i vir.ConstInit) {
	switch x := i.(type) {
	case vir.InitLit:
		w.b(tagInitLit)
		w.operand(x.Value)
	case vir.InitZero:
		w.b(tagInitZero)
	case vir.InitAddr:
		w.b(tagInitAddr)
		w.str(x.Name)
	case vir.InitAgg:
		w.b(tagInitAgg)
		w.u(uint64(len(x.Elems)))
		for _, e := range x.Elems {
			w.init(e)
		}
	case vir.InitBytes:
		w.b(tagInitBytes)
		w.str(string(x.Data))
	}
}

func (w *writer) params(ps []vir.Param) {
	w.u(uint64(len(ps)))
	for _, p := range ps {
		w.str(p.Name)
		w.typ(p.Type)
		w.str(p.ByVal)
		w.str(p.SRet)
	}
}

func (w *writer) attrs(as []vir.FnAttr) {
	w.u(uint64(len(as)))
	for _, a := range as {
		w.str(string(a))
	}
}

func (w *writer) block(b *vir.Block) {
	w.str(b.Label)
	w.u(uint64(len(b.Insts)))
	for _, i := range b.Insts {
		w.str(i.Result)
		w.str(i.Op)
		w.typ(i.Suffix)
		w.str(i.Sig)
		w.u(uint64(i.Align))
		w.u(uint64(len(i.Args)))
		for _, a := range i.Args {
			w.operand(a)
		}
	}
	w.term(b.Term)
}

// Terminator tags.
const (
	tagBr byte = iota
	tagBrIf
	tagSwitch
	tagReturn
	tagTailCall
	tagTrap
	tagUnreachable
)

func (w *writer) term(t vir.Terminator) {
	switch x := t.(type) {
	case vir.Br:
		w.b(tagBr)
		w.str(x.Label)
	case vir.BrIf:
		w.b(tagBrIf)
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
	}
}