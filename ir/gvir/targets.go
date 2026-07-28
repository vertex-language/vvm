// targets.go
package gvir

import "fmt"

// Canonical backend vocabulary (§3), the arch tables maintained outside the
// grammar, the aliases the verifier rejects, and the §4.3 capability data.
//
// The three backends here are the complete target set; §1 says nothing in
// the language anticipates a fourth, so this is a closed vocabulary too.

type BackendKind string

const (
	BackendPTX    BackendKind = "ptx"
	BackendAMDGCN BackendKind = "amdgcn"
	BackendMSL    BackendKind = "msl"
)

// ArtifactOrder is the fixed backend order the availability bitmask in
// gvir_arch.md §4.3 depends on: ptx, then amdgcn archs in declaration
// order, then msl (§3).
var ArtifactOrder = []BackendKind{BackendPTX, BackendAMDGCN, BackendMSL}

// Backend is one entry in the target declaration. Archs is empty for the
// optional-arch backends when omitted; amdgcn requires at least one and
// produces one artifact per arch.
type Backend struct {
	Kind  BackendKind
	Archs []string
}

type Target struct {
	Backends []Backend
}

func (t *Target) Backend(k BackendKind) *Backend {
	if t == nil {
		return nil
	}
	for i := range t.Backends {
		if t.Backends[i].Kind == k {
			return &t.Backends[i]
		}
	}
	return nil
}

// Artifact is one lowering output: a (backend, arch) pair.
type Artifact struct {
	Backend BackendKind
	Arch    string
}

func (a Artifact) String() string { return fmt.Sprintf("%s[%s]", a.Backend, a.Arch) }

// Artifacts returns the artifact list in bitmask order (§3), substituting
// the declared default for an omitted ptx or msl arch. An amdgcn backend
// with no arch contributes nothing — that omission is a verification error
// caught in ir/verify, not silently defaulted here.
func (t *Target) Artifacts() []Artifact {
	if t == nil {
		return nil
	}
	var out []Artifact
	for _, kind := range ArtifactOrder {
		b := t.Backend(kind)
		if b == nil {
			continue
		}
		if len(b.Archs) == 0 {
			if def := DefaultArch(kind); def != "" {
				out = append(out, Artifact{Backend: kind, Arch: def})
			}
			continue
		}
		for _, arch := range b.Archs {
			out = append(out, Artifact{Backend: kind, Arch: arch})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Gated features (§4.3). Exactly three; this is the only gating mechanism.
// ---------------------------------------------------------------------------

type Feature uint8

const (
	FeatureBF16 Feature = iota
	FeatureF64
	FeatureSubgroupSize
)

// GatedFeatures is the complete §4.3 list.
var GatedFeatures = []Feature{FeatureBF16, FeatureF64, FeatureSubgroupSize}

func (f Feature) String() string {
	switch f {
	case FeatureBF16:
		return "bf16"
	case FeatureF64:
		return "f64"
	case FeatureSubgroupSize:
		return "subgroup_size"
	}
	return "feature?"
}

// archCaps records, per arch, whether each §4.3 gated feature is available.
type archCaps struct {
	BF16         bool
	F64          bool
	SubgroupSize bool
}

// The arch tables are the "table maintained outside the grammar" §3 refers
// to. Adding hardware is a table edit; the language does not change.
var ptxArchs = map[string]archCaps{
	"sm_70":  {F64: true, SubgroupSize: true},
	"sm_72":  {F64: true, SubgroupSize: true},
	"sm_75":  {F64: true, SubgroupSize: true},
	"sm_80":  {BF16: true, F64: true, SubgroupSize: true},
	"sm_86":  {BF16: true, F64: true, SubgroupSize: true},
	"sm_87":  {BF16: true, F64: true, SubgroupSize: true},
	"sm_89":  {BF16: true, F64: true, SubgroupSize: true},
	"sm_90":  {BF16: true, F64: true, SubgroupSize: true},
	"sm_90a": {BF16: true, F64: true, SubgroupSize: true},
}

var amdgcnArchs = map[string]archCaps{
	"gfx900":  {F64: true, SubgroupSize: true},
	"gfx906":  {F64: true, SubgroupSize: true},
	"gfx908":  {F64: true, SubgroupSize: true},
	"gfx90a":  {BF16: true, F64: true, SubgroupSize: true},
	"gfx940":  {BF16: true, F64: true, SubgroupSize: true},
	"gfx941":  {BF16: true, F64: true, SubgroupSize: true},
	"gfx942":  {BF16: true, F64: true, SubgroupSize: true},
	"gfx1010": {F64: true, SubgroupSize: true},
	"gfx1030": {F64: true, SubgroupSize: true},
	"gfx1031": {F64: true, SubgroupSize: true},
	"gfx1032": {F64: true, SubgroupSize: true},
	"gfx1100": {BF16: true, F64: true, SubgroupSize: true},
	"gfx1101": {BF16: true, F64: true, SubgroupSize: true},
	"gfx1102": {BF16: true, F64: true, SubgroupSize: true},
}

// msl never provides f64 or an expressible subgroup width (§4.2, §9.2).
var mslArchs = map[string]archCaps{
	"metal30": {},
	"metal31": {BF16: true},
	"metal32": {BF16: true},
}

func archTable(b BackendKind) map[string]archCaps {
	switch b {
	case BackendPTX:
		return ptxArchs
	case BackendAMDGCN:
		return amdgcnArchs
	case BackendMSL:
		return mslArchs
	}
	return nil
}

// ArchAliases maps the spellings §3 explicitly rejects to their canonical
// forms. The verifier consults this only to produce a better diagnostic —
// an alias in a target declaration is an error, never a silent rewrite.
var ArchAliases = map[BackendKind]map[string]string{
	BackendPTX: {
		"sm70": "sm_70", "sm75": "sm_75", "sm80": "sm_80",
		"sm86": "sm_86", "sm89": "sm_89", "sm90": "sm_90",
	},
	BackendAMDGCN: {
		"gfx9": "gfx900", "gfx10": "gfx1030", "gfx11": "gfx1100",
	},
	BackendMSL: {
		"metal3": "metal30", "metal3.0": "metal30",
		"metal3.1": "metal31", "metal3.2": "metal32",
	},
}

// ArchAlias reports whether s is a rejected alias and, if so, what it was
// probably meant to say.
func ArchAlias(b BackendKind, s string) (string, bool) {
	canonical, ok := ArchAliases[b][s]
	return canonical, ok
}

// KnownArch reports whether arch appears in b's maintained table.
func KnownArch(b BackendKind, arch string) bool {
	_, ok := archTable(b)[arch]
	return ok
}

// DefaultArch returns the arch assumed when a backend's arch list is
// omitted (§3). amdgcn has none: its arch list is required.
func DefaultArch(b BackendKind) string {
	switch b {
	case BackendPTX:
		return "sm_70"
	case BackendMSL:
		return "metal32"
	}
	return ""
}

// FloorArch returns the normative minimum (§3). Every §11 opcode is
// available on every artifact at or above the floor, which is why §4.3
// gates only three things.
func FloorArch(b BackendKind) string {
	switch b {
	case BackendPTX:
		return "sm_70"
	case BackendAMDGCN:
		return "gfx900"
	case BackendMSL:
		return "metal30"
	}
	return ""
}

// Supports reports whether a gated feature is available on one artifact.
func Supports(b BackendKind, arch string, f Feature) (bool, error) {
	caps, ok := archTable(b)[arch]
	if !ok {
		if canonical, isAlias := ArchAlias(b, arch); isAlias {
			return false, fmt.Errorf("%s arch %q is an alias, not a canonical name — write %q (§3)", b, arch, canonical)
		}
		return false, fmt.Errorf("unknown %s arch %q (§3)", b, arch)
	}
	switch f {
	case FeatureBF16:
		return caps.BF16, nil
	case FeatureF64:
		return caps.F64, nil
	case FeatureSubgroupSize:
		return caps.SubgroupSize, nil
	}
	return false, fmt.Errorf("unknown feature %d", f)
}

func (a Artifact) Supports(f Feature) (bool, error) { return Supports(a.Backend, a.Arch, f) }

// TypeFeatures reports which §4.3 gated features a type's use implies. It
// is the leaf of the gating analysis: walking signatures, group decls and
// call graphs to decide kernel exclusion is ir/verify's job (§4.3 rules
// 1-4), but "does this type need bf16 or f64" is answered once, here.
func (m *Module) TypeFeatures(t Type) []Feature {
	set := map[Feature]bool{}
	m.collectTypeFeatures(t, set, map[string]bool{})
	var out []Feature
	for _, f := range GatedFeatures {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}

func (m *Module) collectTypeFeatures(t Type, set map[Feature]bool, seen map[string]bool) {
	switch x := t.(type) {
	case FloatType:
		if x.Brain {
			set[FeatureBF16] = true
		}
		if x.Bits == 64 && !x.Brain {
			set[FeatureF64] = true
		}
	case VecType:
		m.collectTypeFeatures(x.Elem, set, seen)
	case ArrayType:
		m.collectTypeFeatures(x.Elem, set, seen)
	case StructType:
		if seen[x.Name] {
			return
		}
		seen[x.Name] = true
		if s := m.StructByName(x.Name); s != nil {
			for _, f := range s.Fields {
				m.collectTypeFeatures(f.Type, set, seen)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Resource limits (§6.5). Checked at lowering time; exceeding one is a
// lowering error for that artifact, not a verification error.
// ---------------------------------------------------------------------------

// PortableGroupLimit is the static `group` budget that lowers everywhere.
const PortableGroupLimit = 32 << 10

// StaticGroupLimit reports a backend's static `group` budget per kernel.
func StaticGroupLimit(b BackendKind) int {
	switch b {
	case BackendPTX:
		return 48 << 10
	case BackendAMDGCN:
		return 64 << 10
	case BackendMSL:
		return 32 << 10
	}
	return 0
}