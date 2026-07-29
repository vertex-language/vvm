package ptx

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/gvir"
)

// §4.3 capability gating for this one artifact.
//
// Rule 1: a kernel uses a gated feature if it appears in its signature, its
// group declarations, its body, or the body of any func reachable from it.
// Rule 2: such a kernel is excluded from every artifact where the feature is
// unavailable. Rule 3: a func is emitted if any emitted kernel reaches it.
// Rule 4 — exclusion from *every* artifact — is a whole-module judgement and
// belongs to ir/verify, not to a single backend.

type keepSet struct {
	kernels map[string]bool
	funcs   map[string]bool
}

func (l *lowerer) gate() (keepSet, error) {
	keep := keepSet{kernels: map[string]bool{}, funcs: map[string]bool{}}

	for _, k := range l.gm.Kernels {
		reach := l.reachable(k)
		feats := l.kernelFeatures(k, reach)

		excluded := false
		for _, f := range gvir.GatedFeatures {
			if !feats[f] {
				continue
			}
			ok, err := gvir.Supports(gvir.BackendPTX, l.arch, f)
			if err != nil {
				return keep, err
			}
			if !ok {
				l.excluded = append(l.excluded, Exclusion{Kernel: k.Name, Feature: f})
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		// §9.2: subgroup_size is expressible on ptx, but the width is fixed at
		// 32. A different width is a lowering error, not an exclusion — the
		// kernel is asking for something this backend cannot deliver at all.
		if k.SubgroupSize != 0 && k.SubgroupSize != 32 {
			return keep, fmt.Errorf(
				"gpu/lower/ptx: kernel %s requests subgroup_size %d; the ptx subgroup width is fixed at 32 (§9.2)",
				k.Name, k.SubgroupSize)
		}

		keep.kernels[k.Name] = true
		for name := range reach {
			keep.funcs[name] = true
		}
	}
	return keep, nil
}

// reachable returns the transitive set of func names k calls.
func (l *lowerer) reachable(k *gvir.Kernel) map[string]bool {
	out := map[string]bool{}
	var walk func(b *gvir.Body)
	walk = func(b *gvir.Body) {
		for _, blk := range b.AllBlocks() {
			for _, in := range blk.Lines {
				if in.Op != gvir.OpCall || len(in.Args) == 0 {
					continue
				}
				name := in.Args[0].Ident
				if out[name] {
					continue
				}
				out[name] = true
				if f := l.gm.FuncByName(name); f != nil {
					walk(&f.Body)
				}
			}
		}
	}
	walk(&k.Body)
	return out
}

// kernelFeatures collects §4.3 feature use for k, transitively.
func (l *lowerer) kernelFeatures(k *gvir.Kernel, reach map[string]bool) map[gvir.Feature]bool {
	set := map[gvir.Feature]bool{}
	add := func(t gvir.Type) {
		if t == nil {
			return
		}
		for _, f := range l.gm.TypeFeatures(t) {
			set[f] = true
		}
	}

	if k.SubgroupSize != 0 {
		set[gvir.FeatureSubgroupSize] = true
	}
	for _, p := range k.Params {
		add(p.Type)
	}
	for _, g := range k.Groups {
		add(g.Type)
	}
	l.bodyFeatures(&k.Body, add)

	for name := range reach {
		f := l.gm.FuncByName(name)
		if f == nil {
			continue
		}
		for _, p := range f.Params {
			add(p.Type)
		}
		add(f.Ret)
		l.bodyFeatures(&f.Body, add)
	}
	return set
}

// bodyFeatures reports the types a body names. Every gated type reaches an
// instruction suffix somewhere — there is no way to compute on a bf16 or an f64
// without spelling it — so the suffix channel is the complete leaf set.
func (l *lowerer) bodyFeatures(b *gvir.Body, add func(gvir.Type)) {
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			add(in.Suffix)
			if in.Op == gvir.OpCall && len(in.Args) > 0 {
				if f := l.gm.FuncByName(in.Args[0].Ident); f != nil {
					add(f.Ret)
				}
			}
		}
	}
}