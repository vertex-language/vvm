// gating.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/gvir"
)

// §4.3 capability gating for this one artifact. Two of the three gated
// features are unconditional on msl — there is no f64 and no expressible
// subgroup width on any Metal target (§4.2, §9.2) — so unlike the other two
// backends this is not a formality: a kernel touching either is excluded here
// and lowers only on ptx and amdgcn.

type gating struct {
	m        *gvir.Module
	arch     string
	calls    map[string][]string // func or kernel name -> called funcs
	excluded []Exclusion
}

func newGating(m *gvir.Module, arch string) (*gating, error) {
	g := &gating{m: m, arch: arch, calls: map[string][]string{}}
	for _, f := range m.Funcs {
		g.calls[f.Name] = callees(&f.Body)
	}
	for _, k := range m.Kernels {
		g.calls[k.Name] = callees(&k.Body)
	}
	return g, nil
}

func callees(b *gvir.Body) []string {
	var out []string
	seen := map[string]bool{}
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			if in.Op != gvir.OpCall || len(in.Args) == 0 {
				continue
			}
			name := in.Args[0].Ident
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// reach walks the call graph from one entry point. Recursion is illegal
// (§6.4), but the visited set costs nothing and keeps a malformed module from
// hanging the backend.
func (g *gating) reach(name string, seen map[string]bool) {
	for _, c := range g.calls[name] {
		if seen[c] {
			continue
		}
		seen[c] = true
		g.reach(c, seen)
	}
}

func (g *gating) reachedBy(keep map[string]bool) map[string]bool {
	live := map[string]bool{}
	for _, k := range g.m.Kernels {
		if keep[k.Name] {
			g.reach(k.Name, live)
		}
	}
	return live
}

// features collects every §4.3 feature a kernel uses, transitively over the
// call graph: its signature, its group declarations, every instruction suffix
// in its body, and the signature and body of every func it reaches (rule 1).
func (g *gating) features(k *gvir.Kernel) []gvir.Feature {
	set := map[gvir.Feature]bool{}
	if k.SubgroupSize != 0 {
		set[gvir.FeatureSubgroupSize] = true
	}
	for _, p := range k.Params {
		g.addTypeFeatures(p.Type, set)
	}
	for _, gv := range k.Groups {
		g.addTypeFeatures(gv.Type, set)
	}
	g.addBodyFeatures(&k.Body, set)

	reached := map[string]bool{}
	g.reach(k.Name, reached)
	for name := range reached {
		f := g.m.FuncByName(name)
		if f == nil {
			continue
		}
		for _, p := range f.Params {
			g.addTypeFeatures(p.Type, set)
		}
		g.addTypeFeatures(f.Ret, set)
		g.addBodyFeatures(&f.Body, set)
	}

	var out []gvir.Feature
	for _, f := range gvir.GatedFeatures {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}

func (g *gating) addTypeFeatures(t gvir.Type, set map[gvir.Feature]bool) {
	if t == nil {
		return
	}
	for _, f := range g.m.TypeFeatures(t) {
		set[f] = true
	}
}

func (g *gating) addBodyFeatures(b *gvir.Body, set map[gvir.Feature]bool) {
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			g.addTypeFeatures(in.Suffix, set)
		}
	}
}

// kernels partitions the module's kernels into those this artifact emits and
// those §4.3 rule 2 excludes. Exclusion is not an error here: rule 4 makes
// exclusion from *every* artifact a gating error, and that is a whole-module
// judgement ir/verify makes.
func (g *gating) kernels() (map[string]bool, error) {
	keep := map[string]bool{}
	for _, k := range g.m.Kernels {
		excluded := false
		for _, f := range g.features(k) {
			ok, err := gvir.Supports(gvir.BackendMSL, g.arch, f)
			if err != nil {
				return nil, fmt.Errorf("lower/msl: %w", err)
			}
			if !ok {
				g.excluded = append(g.excluded, Exclusion{Kernel: k.Name, Feature: f})
				excluded = true
				break
			}
		}
		if !excluded {
			keep[k.Name] = true
		}
	}
	return keep, nil
}