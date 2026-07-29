// msl.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// Options controls one lowering. The zero value is usable; DefaultOptions is
// what Lower uses.
type Options struct {
	// Arch overrides the arch declared in the module's `target` line. It must
	// still be a canonical msl arch (§3).
	Arch string

	// Comments emits `loc` lines, merge annotations and structural notes as
	// MSL comments. They are never directives.
	Comments bool
}

func DefaultOptions() Options { return Options{Comments: true} }

// Exclusion records a kernel dropped from this artifact by §4.3 gating.
type Exclusion struct {
	Kernel  string
	Feature gvir.Feature
}

// Result is one artifact: .gvir declares at most one msl arch, so Lower
// produces exactly one module (§3).
type Result struct {
	Module   *msl.Module
	Arch     string
	Version  msl.Version
	Excluded []Exclusion
}

// Lower lowers a verified .gvir module to MSL with the default options.
func Lower(m *gvir.Module) (*Result, error) { return LowerOptions(m, DefaultOptions()) }

// LowerOptions lowers a verified .gvir module to MSL.
func LowerOptions(m *gvir.Module, opt Options) (*Result, error) {
	arch, err := selectArch(m, opt.Arch)
	if err != nil {
		return nil, err
	}
	ver, err := archVersion(arch)
	if err != nil {
		return nil, err
	}

	l := &lowerer{
		src:     m,
		opt:     opt,
		arch:    arch,
		ver:     ver,
		names:   newNameTable(),
		structs: map[string]*msl.Struct{},
		fields:  map[string][]*msl.Field{},
		res:     &Result{Arch: arch, Version: ver},
	}
	l.out = msl.NewModule(ver)
	l.res.Module = l.out

	l.preamble()

	if err := l.lowerStructs(); err != nil {
		return nil, err
	}
	if err := l.lowerConsts(); err != nil {
		return nil, err
	}

	g, err := newGating(m, arch)
	if err != nil {
		return nil, err
	}
	keep, err := g.kernels()
	if err != nil {
		return nil, err
	}
	l.res.Excluded = g.excluded

	// §4.3 rule 3: a func is emitted only if some emitted kernel reaches it.
	live := g.reachedBy(keep)
	for _, fn := range m.Funcs {
		if !live[fn.Name] {
			continue
		}
		if err := l.lowerFunc(fn); err != nil {
			return nil, err
		}
	}
	for _, k := range m.Kernels {
		if !keep[k.Name] {
			continue
		}
		if err := l.lowerKernel(k); err != nil {
			return nil, err
		}
	}

	return l.res, nil
}

// ---------------------------------------------------------------------------
// Arch and language revision
// ---------------------------------------------------------------------------

func selectArch(m *gvir.Module, override string) (string, error) {
	b := m.Target.Backend(gvir.BackendMSL)
	if b == nil && override == "" {
		return "", fmt.Errorf("lower/msl: module %s declares no msl backend (§3)", m.Name)
	}
	arch := override
	if arch == "" {
		if len(b.Archs) > 0 {
			arch = b.Archs[0]
		} else {
			arch = gvir.DefaultArch(gvir.BackendMSL)
		}
	}
	if !gvir.KnownArch(gvir.BackendMSL, arch) {
		if canonical, ok := gvir.ArchAlias(gvir.BackendMSL, arch); ok {
			return "", fmt.Errorf("lower/msl: %q is an alias, not a canonical arch — write %q (§3)", arch, canonical)
		}
		return "", fmt.Errorf("lower/msl: unknown msl arch %q (§3)", arch)
	}
	return arch, nil
}

func archVersion(arch string) (msl.Version, error) {
	switch arch {
	case "metal30":
		return msl.Metal30, nil
	case "metal31":
		return msl.Metal31, nil
	case "metal32":
		return msl.Metal32, nil
	}
	return msl.Version{}, fmt.Errorf("lower/msl: arch %q has no MSL language revision", arch)
}

// ---------------------------------------------------------------------------
// Module lowerer
// ---------------------------------------------------------------------------

type lowerer struct {
	src  *gvir.Module
	out  *msl.Module
	opt  Options
	arch string
	ver  msl.Version

	names   *nameTable
	structs map[string]*msl.Struct // .gvir struct name -> emitted struct
	fields  map[string][]*msl.Field

	res *Result
}

// preambleText is the whole of this backend's runtime. .gvir pointers are
// address-space-qualified byte addresses with no pointee type (§5, §8.3);
// MSL has no untyped pointer and no integer-to-pointer conversion, so every
// access reinterprets a byte pointer at the accessed type. The overloads are
// resolved by the operand's address space, which is the only place the space
// is written down after lowering.
const preambleText = `template <typename T> inline device T *vv_at(device uchar *p) { return (device T *)p; }
template <typename T> inline constant T *vv_at(constant uchar *p) { return (constant T *)p; }
template <typename T> inline threadgroup T *vv_at(threadgroup uchar *p) { return (threadgroup T *)p; }
template <typename T> inline thread T *vv_at(thread uchar *p) { return (thread T *)p; }

template <typename T> inline device uchar *vv_bytes(device T *p) { return (device uchar *)p; }
template <typename T> inline constant uchar *vv_bytes(constant T *p) { return (constant uchar *)p; }
template <typename T> inline threadgroup uchar *vv_bytes(threadgroup T *p) { return (threadgroup uchar *)p; }
template <typename T> inline thread uchar *vv_bytes(thread T *p) { return (thread uchar *)p; }`

func (l *lowerer) preamble() {
	l.out.Include("metal_atomic")
	l.out.Include("metal_simdgroup")
	l.out.Add(&msl.CommentDecl{Text: fmt.Sprintf(
		"module %s, lowered from .gvir %s for %s", l.src.Name, l.src.Version, l.arch)})

	if !l.src.Profile.Contract {
		// §11.6 contract-off means no mul+add fusion. MSL has no in-source
		// switch for it below Metal 3.2, and the Metal frontend fuses under
		// fast math, so the artifact carries the requirement rather than a
		// pragma this package cannot version-gate honestly.
		l.out.Add(&msl.CommentDecl{Text: "float_profile: contract off — compile with -fno-fast-math (§11.6)"})
	}
	if l.src.Profile.Approx {
		l.out.Add(&msl.CommentDecl{Text: "float_profile: approx on — fast:: forms are reachable (§11.6)"})
	}
	l.out.Add(&msl.CommentDecl{Text: ".gvir pointers are byte addresses; vv_at reinterprets one at the accessed type"})
	l.out.Add(&msl.RawDecl{Text: preambleText})
}

// ---------------------------------------------------------------------------
// Structs and constants (§2, §4.7)
// ---------------------------------------------------------------------------

// lowerStructs emits each .gvir struct with explicit padding wherever MSL's
// own layout would not put a field where §4.7 requires it. §13 compares the
// two byte for byte, so the padding is the thing that makes the comparison
// pass rather than a thing the comparison is allowed to discover.
func (l *lowerer) lowerStructs() error {
	for _, s := range l.src.Structs {
		want, err := l.src.StructLayout(s)
		if err != nil {
			return fmt.Errorf("lower/msl: %w", err)
		}
		out := msl.NewStruct(l.names.ident(s.Name))
		var handles []*msl.Field
		off, pads := 0, 0
		for i, f := range s.Fields {
			ft, err := l.mapType(f.Type)
			if err != nil {
				return fmt.Errorf("lower/msl: struct %s field %s: %w", s.Name, f.Name, err)
			}
			size, align, err := l.mslSizeAlign(f.Type)
			if err != nil {
				return fmt.Errorf("lower/msl: struct %s field %s: %w", s.Name, f.Name, err)
			}
			off = alignUp(off, align)
			at := want.Fields[i].Offset
			if off > at {
				return fmt.Errorf("lower/msl: struct %s field %s: MSL places it at byte %d, §4.7 requires %d",
					s.Name, f.Name, off, at)
			}
			if off < at {
				out.Field(fmt.Sprintf("vv_pad%d", pads), msl.Array(msl.UChar, at-off))
				pads++
				off = at
			}
			handles = append(handles, out.Field(l.names.ident(f.Name), ft))
			off += size
		}
		if off < want.Size {
			out.Field(fmt.Sprintf("vv_pad%d", pads), msl.Array(msl.UChar, want.Size-off))
		}
		l.out.Add(out)
		l.structs[s.Name] = out
		l.fields[s.Name] = handles
	}
	return nil
}

func (l *lowerer) lowerConsts() error {
	for _, c := range l.src.Constants {
		t, err := l.mapType(c.Type)
		if err != nil {
			return fmt.Errorf("lower/msl: const %s: %w", c.Name, err)
		}
		init, err := l.constInit(c.Type, c.Init)
		if err != nil {
			return fmt.Errorf("lower/msl: const %s: %w", c.Name, err)
		}
		l.out.Constant(t, l.names.ident(c.Name), init)
	}
	return nil
}

func (l *lowerer) constInit(t gvir.Type, init gvir.ConstInit) (msl.Expr, error) {
	switch x := init.(type) {
	case gvir.InitZero:
		return msl.Init(), nil // {} zero-initializes every storable type
	case gvir.InitLiteral:
		mt, err := l.mapType(t)
		if err != nil {
			return msl.Expr{}, err
		}
		return literal(x.Value, mt)
	case gvir.InitAggregate:
		elems := make([]msl.Expr, 0, len(x.Elems))
		for i, e := range x.Elems {
			et, err := l.elemType(t, i)
			if err != nil {
				return msl.Expr{}, err
			}
			sub, err := l.constInit(et, e)
			if err != nil {
				return msl.Expr{}, err
			}
			elems = append(elems, sub)
		}
		return msl.Init(elems...), nil
	}
	return msl.Expr{}, fmt.Errorf("unhandled const-init %T", init)
}

// elemType returns the type of the i'th member of an aggregate const-init.
func (l *lowerer) elemType(t gvir.Type, i int) (gvir.Type, error) {
	switch x := t.(type) {
	case gvir.ArrayType:
		return x.Elem, nil
	case gvir.VecType:
		return x.Elem, nil
	case gvir.StructType:
		s := l.src.StructByName(x.Name)
		if s == nil || i >= len(s.Fields) {
			return nil, fmt.Errorf("no field %d of struct %s", i, x.Name)
		}
		return s.Fields[i].Type, nil
	}
	return nil, fmt.Errorf("%s is not an aggregate", t)
}

func alignUp(v, a int) int {
	if a <= 1 {
		return v
	}
	if r := v % a; r != 0 {
		return v + a - r
	}
	return v
}