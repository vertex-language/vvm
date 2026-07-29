// gating.go
package msl

import (
	"fmt"

	"github.com/vertex-language/vvm/gpu/ir/msl"
	"github.com/vertex-language/vvm/ir/gvir"
)

// gate implements §4.3 rules 1-4 for this one artifact.
//
// Rule 4 — exclusion from *every* declared artifact — is a whole-module
// judgement ir/verify makes; a kernel excluded here is recorded, not rejected.
func (l *lowerer) gate() error {
	for _, k := range l.src.Kernels {
		feats, err := l.kernelFeatures(k)
		if err != nil {
			return fmt.Errorf("kernel %s: %w", k.Name, err)
		}
		excluded := false
		for _, f := range feats {
			ok, err := gvir.Supports(gvir.BackendMSL, l.arch, f)
			if err != nil {
				return err
			}
			if !ok {
				l.excluded = append(l.excluded, Exclusion{Kernel: k.Name, Feature: f})
				excluded = true
				break
			}
		}
		if !excluded {
			l.emit[k.Name] = true
		}
	}

	// Rule 3: a func is emitted if any emitted kernel reaches it.
	for _, k := range l.src.Kernels {
		if l.emit[k.Name] {
			l.reach(&k.Body)
		}
	}
	return nil
}

func (l *lowerer) reach(b *gvir.Body) {
	for _, callee := range calls(b) {
		if l.emit[callee] {
			continue
		}
		f := l.src.FuncByName(callee)
		if f == nil {
			continue // ir/verify's diagnostic, not ours
		}
		l.emit[callee] = true
		l.reach(&f.Body)
	}
}

func calls(b *gvir.Body) []string {
	var out []string
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			if in.Op == gvir.OpCall && len(in.Args) > 0 && in.Args[0].Kind == gvir.OperandIdent {
				out = append(out, in.Args[0].Ident)
			}
		}
	}
	return out
}

// kernelFeatures collects a kernel's gated feature use per §4.3 rule 1:
// signature, group declarations, body, and the body of every reachable func.
func (l *lowerer) kernelFeatures(k *gvir.Kernel) ([]gvir.Feature, error) {
	set := map[gvir.Feature]bool{}
	if k.SubgroupSize != 0 {
		// §9.2: not expressible on msl, and therefore gated.
		set[gvir.FeatureSubgroupSize] = true
	}
	for _, p := range k.Params {
		l.addFeatures(set, p.Type)
	}
	for _, g := range k.Groups {
		l.addFeatures(set, g.Type)
	}
	if err := l.bodyFeatures(set, &k.Body, map[string]bool{}); err != nil {
		return nil, err
	}
	var out []gvir.Feature
	for _, f := range gvir.GatedFeatures {
		if set[f] {
			out = append(out, f)
		}
	}
	return out, nil
}

func (l *lowerer) bodyFeatures(set map[gvir.Feature]bool, b *gvir.Body, seen map[string]bool) error {
	for _, blk := range b.AllBlocks() {
		for _, in := range blk.Lines {
			l.addFeatures(set, in.Suffix)
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
				return fmt.Errorf("call to undeclared func %q (§6.4)", name)
			}
			l.addFeatures(set, f.Ret)
			for _, p := range f.Params {
				l.addFeatures(set, p.Type)
			}
			if err := l.bodyFeatures(set, &f.Body, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *lowerer) addFeatures(set map[gvir.Feature]bool, t gvir.Type) {
	if t == nil {
		return
	}
	for _, f := range l.src.TypeFeatures(t) {
		set[f] = true
	}
}

// available reports whether every gated feature t implies is available here.
func (l *lowerer) available(t gvir.Type) (bool, error) {
	for _, f := range l.src.TypeFeatures(t) {
		ok, err := gvir.Supports(gvir.BackendMSL, l.arch, f)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// lowerStructs emits every struct whose fields are available on this artifact.
// One that is not can only be referenced by an excluded kernel, so it is
// replaced by a comment rather than dropped silently.
func (l *lowerer) lowerStructs() error {
	for _, s := range l.src.Structs {
		ok, err := l.available(gvir.StructType{Name: s.Name})
		if err != nil {
			return err
		}
		if !ok {
			l.out.Add(&msl.CommentDecl{Text: fmt.Sprintf(
				"struct %s omitted: a field uses a feature unavailable on %s (§4.3)", s.Name, l.arch)})
			continue
		}
		ms := msl.NewStruct(s.Name)
		for _, f := range s.Fields {
			t, err := l.typeOf(f.Type)
			if err != nil {
				return fmt.Errorf("struct %s field %s: %w", s.Name, f.Name, err)
			}
			ms.Field(f.Name, t)
		}
		l.out.Add(ms)
		l.structs[s.Name] = s
	}
	return nil
}