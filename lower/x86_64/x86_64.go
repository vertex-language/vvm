// x86_64.go
package x86_64

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	enc "github.com/vertex-language/vvm/isa/x86_64/encoder"
)

// Lower turns a verified x86_64 vir.Module into machine code, symbols, and
// unresolved fixups. It assumes vir.Verify has passed and, for multi-file
// modules, importer.Rewrite has already erased cross-module references.
func Lower(m *vir.Module) (*Program, error) {
	if m.Target == nil || m.Target.Arch != "x86_64" {
		got := "<none>"
		if m.Target != nil {
			got = m.Target.Arch
		}
		return nil, fmt.Errorf("lower/x86_64: target arch must be x86_64, got %s", got)
	}

	p := &Program{}
	idx := newIndex(m)

	for _, g := range m.Globals {
		lg, err := lowerGlobal(m, g)
		if err != nil {
			return nil, fmt.Errorf("global %s: %w", g.Name, err)
		}
		p.Globals = append(p.Globals, lg)
	}
	for _, f := range m.Functions {
		lf, err := lowerFunc(m, idx, f)
		if err != nil {
			return nil, fmt.Errorf("fn %s: %w", f.Name, err)
		}
		p.Funcs = append(p.Funcs, lf)
	}
	return p, nil
}

type Program struct {
	Funcs   []Func
	Globals []Global
}

type Func struct {
	Name   string
	Code   []byte
	Align  uint32
	Export bool
	Fixups []enc.Fixup
}

type Global struct {
	Name   string
	Data   []byte // nil for zero (BSS-style) storage
	Size   uint32 // may exceed len(Data) for zero fill
	Align  uint32
	Export bool
	TLS    bool
	Fixups []enc.Fixup
}

// index is the module-wide symbol/callable lookup isel needs: which names
// are functions (direct call targets), which are globals (RIP-relative
// addressable), and each callable's declared signature for arg layout.
type index struct {
	funcs   map[string]*vir.Function
	externs map[string]*vir.ExternFunction
	globals map[string]*vir.Global
	consts  map[string]*vir.Constant
	structs map[string]*vir.Struct
	sigs    map[string]*vir.FunctionSignature
}

func newIndex(m *vir.Module) *index {
	i := &index{
		funcs:   map[string]*vir.Function{},
		externs: map[string]*vir.ExternFunction{},
		globals: map[string]*vir.Global{},
		consts:  map[string]*vir.Constant{},
		structs: map[string]*vir.Struct{},
		sigs:    map[string]*vir.FunctionSignature{},
	}
	for _, f := range m.Functions {
		i.funcs[f.Name] = f
	}
	for _, g := range m.Externs {
		for _, fn := range g.Functions {
			i.externs[fn.Name] = fn
		}
	}
	for _, g := range m.Globals {
		i.globals[g.Name] = g
	}
	for _, c := range m.Constants {
		i.consts[c.Name] = c
	}
	for _, s := range m.Structs {
		i.structs[s.Name] = s
	}
	for _, s := range m.FunctionSignatures {
		i.sigs[s.Name] = s
	}
	return i
}

// calleeParams returns the declared parameter list and variadic flag for a
// direct-call target named by callee, whether it's a local fn or an extern.
func (ix *index) calleeParams(callee string) (params []vir.Param, variadic bool, ok bool) {
	if f, has := ix.funcs[callee]; has {
		return f.Params, f.Variadic, true
	}
	if f, has := ix.externs[callee]; has {
		return f.Params, f.Variadic, true
	}
	return nil, false, false
}

// todo wraps an "unimplemented but valid" error, suffixed (TODO) so callers
// can tell it apart from a malformed-module error (a plain fmt.Errorf).
func todo(format string, a ...any) error {
	return fmt.Errorf(format+" (TODO)", a...)
}