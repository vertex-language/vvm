// gating.go
package amdtx

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/gvir"
)

// §4.3 capability gating for one artifact.
//
// Rules 1-3 are implemented here: use is transitive over the call graph, a
// kernel using a feature unavailable on this arch is excluded, and a func is
// emitted only if some emitted kernel reaches it. Rule 4 — exclusion from
// *every* artifact — is a whole-module judgement and belongs to ir/verify.

func (l *lowerer) gate() error {
	l.emitted = map[string]bool{}
	l.reached = map[string]bool{}

	for _, k := range l.src.Kernels {
		feats, err := l.kernelFeatures(k)
		if err != nil {
			return fmt.Errorf("kernel %s: %w", k.Name, err)
		}
		excluded := false
		for _, f := range feats {
			ok, err := gvir.Supports(gvir.BackendAMDGCN, l.arch, f)
			if err != nil {
				return err
			}
			if !ok {
				excluded = true
			}
		}
		if !excluded {
			l.emitted[k.Name] = true
		}
	}
	for _, k := range l.src.Kernels {
		if l.emitted[k.Name] {
			l.reach(&k.Body, map[string]bool{})
		}
	}
	return nil
}

// exclusions rebuilds the reportable list once emitted is settled, so that a
// kernel excluded by two features reports both.
func (l *lowerer) exclusions() []Exclusion {
	var out []Exclusion
	for _, k := range l.src.Kernels {
		if l.emitted[k.Name] {
			continue
		}
		feats, err := l.kernelFeatures(k)
		if err != nil {
			continue
		}
		for _, f := range feats {
			if ok, err := gvir.Supports(gvir.BackendAMDGCN, l.arch, f); err == nil && !ok {
				out = append(out, Exclusion{Kernel: k.Name, Feature: f})
			}
		}
	}
	return out
}

// kernelFeatures collects the §4.3 features a kernel uses: its signature, its
// group declarations, its body, and the signature and body of every func it
// reaches. Use is transitive over the call graph (rule 1).
func (l *lowerer) kernelFeatures(k *gvir.Kernel) ([]gvir.Feature, error) {
	set := map[gvir.Feature]bool{}
	if k.SubgroupSize != 0 {
		set[gvir.FeatureSubgroupSize] = true
	}
	for _, p := range k.Params {
		l.addFeatures(set, p.Type)
	}
	for _, g := range k.Groups {
		l.addFeatures(set, g.Type)
	}
	l.bodyFeatures(set, &k.Body, map[string]bool{})
	return ordered(set), nil
}

func (l *lowerer) bodyFeatures(set map[gvir.Feature]bool, b *gvir.Body, seen map[string]bool) {
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			if in.Suffix != nil {
				l.addFeatures(set, in.Suffix)
			}
			if in.Op != gvir.OpCall || len(in.Args) == 0 || in.Args[0].Kind != gvir.OperandIdent {
				continue
			}
			name := in.Args[0].Ident
			if seen[name] {
				continue
			}
			seen[name] = true
			f := l.src.FuncByName(name)
			if f == nil {
				continue
			}
			for _, p := range f.Params {
				l.addFeatures(set, p.Type)
			}
			l.addFeatures(set, f.Ret)
			l.bodyFeatures(set, &f.Body, seen)
		}
	}
}

func (l *lowerer) addFeatures(set map[gvir.Feature]bool, t gvir.Type) {
	if t == nil {
		return
	}
	for _, f := range l.src.TypeFeatures(t) {
		set[f] = true
	}
}

// reach marks every func an emitted body calls, transitively (rule 3).
func (l *lowerer) reach(b *gvir.Body, seen map[string]bool) {
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			if in.Op != gvir.OpCall || len(in.Args) == 0 || in.Args[0].Kind != gvir.OperandIdent {
				continue
			}
			name := in.Args[0].Ident
			if seen[name] {
				continue
			}
			seen[name] = true
			l.reached[name] = true
			if f := l.src.FuncByName(name); f != nil {
				l.reach(&f.Body, seen)
			}
		}
	}
}

func ordered(set map[gvir.Feature]bool) []gvir.Feature {
	var out []gvir.Feature
	for _, f := range gvir.GatedFeatures {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}