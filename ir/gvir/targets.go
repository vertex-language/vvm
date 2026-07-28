// targets.go
package gvir

import "strings"

// Target vocabulary (§3) and capability gating (§4.3). As in vir, the
// canonical sets are what the verifier consults and anything appearing in
// an alias map is rejected outright — `gfx11`, `sm90`, and `metal3.2` are
// errors, not spellings to be normalized.
//
// The arch tables are the "table maintained outside the grammar" §3 refers
// to. They are data: extend them as hardware ships, without touching the
// grammar or the type system.

type BackendName string

const (
	PTX    BackendName = "ptx"
	AMDGCN BackendName = "amdgcn"
	MSL    BackendName = "msl"
)

// Backend is one entry of the target-decl (§3). Archs is empty when the
// list was omitted, which is legal for ptx and msl and an error for
// amdgcn — that backend has no JIT fallback, so it has no default.
type Backend struct {
	Name  BackendName
	Archs []string
}

// Defaults for an omitted arch list (§3).
const (
	DefaultPTXArch = "sm_70"
	DefaultMSLArch = "metal32"
)

// ArchListRequired reports whether b must carry an explicit arch list.
func ArchListRequired(b BackendName) bool { return b == AMDGCN }

// Launcher error codes referenced by §4.3, §6.3, and §6.5.
const (
	ErrUnavailable = "GVIR_ERR_UNAVAILABLE"
	ErrResources   = "GVIR_ERR_RESOURCES"
)

// --- ptx -------------------------------------------------------------------

// PTXArchs lists the recognized minimum-SM identifiers. An omitted list
// means DefaultPTXArch and JIT-forward.
var PTXArchs = map[string]bool{
	"sm_70": true, "sm_72": true, "sm_75": true,
	"sm_80": true, "sm_86": true, "sm_87": true, "sm_89": true,
	"sm_90": true, "sm_90a": true,
	"sm_100": true, "sm_120": true,
}

// --- amdgcn ----------------------------------------------------------------

// AMDArch records the per-target capabilities gating depends on. bf16 is
// tracked as an explicit flag rather than derived from an ordering,
// because gfx names are not totally ordered by their numeric part —
// gfx1030 is numerically larger than gfx90a but is an earlier RDNA part
// without the CDNA2 bf16 support §4.3 keys on.
type AMDArch struct {
	BF16 bool
	// Wave lists the selectable subgroup widths (§9.2).
	Wave []int
}

var AMDGCNArchs = map[string]AMDArch{
	"gfx900":  {BF16: false, Wave: []int{64}},
	"gfx906":  {BF16: false, Wave: []int{64}},
	"gfx908":  {BF16: false, Wave: []int{64}},
	"gfx90a":  {BF16: true, Wave: []int{64}},
	"gfx940":  {BF16: true, Wave: []int{64}},
	"gfx941":  {BF16: true, Wave: []int{64}},
	"gfx942":  {BF16: true, Wave: []int{64}},
	"gfx1010": {BF16: false, Wave: []int{32, 64}},
	"gfx1030": {BF16: false, Wave: []int{32, 64}},
	"gfx1100": {BF16: true, Wave: []int{32, 64}},
	"gfx1101": {BF16: true, Wave: []int{32, 64}},
	"gfx1102": {BF16: true, Wave: []int{32, 64}},
	"gfx1200": {BF16: true, Wave: []int{32, 64}},
	"gfx1201": {BF16: true, Wave: []int{32, 64}},
}

// --- msl -------------------------------------------------------------------

// MSLArchs maps each language floor to its rank. The floor gates language
// features only (§3).
var MSLArchs = map[string]int{
	"metal30": 0,
	"metal31": 1,
	"metal32": 2,
	"metal40": 3,
	"metal41": 4,
}

// --- aliases ---------------------------------------------------------------

// ArchAliases maps rejected spellings to their canonical form. Present so
// the verifier can say "write gfx1100, not gfx11" rather than merely
// "unknown arch"; it never resolves them (§3).
var ArchAliases = map[string]string{
	"sm70": "sm_70", "sm75": "sm_75", "sm80": "sm_80", "sm86": "sm_86",
	"sm89": "sm_89", "sm90": "sm_90", "sm100": "sm_100",
	"compute_80": "sm_80", "compute_90": "sm_90",

	"gfx9": "gfx900", "gfx90": "gfx900", "gfx10": "gfx1010",
	"gfx11": "gfx1100", "gfx12": "gfx1200",
	"vega": "gfx900", "mi250x": "gfx90a", "mi300x": "gfx942",
	"rdna3": "gfx1100", "cdna3": "gfx942",

	"metal3": "metal30", "metal3.0": "metal30", "metal3.1": "metal31",
	"metal3.2": "metal32", "metal4": "metal40", "metal4.0": "metal40",
	"metal4.1": "metal41",
}

// BackendAliases likewise names rejected backend spellings.
var BackendAliases = map[string]BackendName{
	"nvptx": PTX, "cuda": PTX, "nvidia": PTX,
	"rocm": AMDGCN, "hip": AMDGCN, "amd": AMDGCN,
	"metal": MSL, "apple": MSL,
}

// ValidArch reports whether arch is canonical for b.
func ValidArch(b BackendName, arch string) bool {
	switch b {
	case PTX:
		return PTXArchs[arch]
	case AMDGCN:
		_, ok := AMDGCNArchs[arch]
		return ok
	case MSL:
		_, ok := MSLArchs[arch]
		return ok
	}
	return false
}

// --- artifacts -------------------------------------------------------------

// Artifact is one (backend, arch) pair — the unit §3 emits one binary for
// and §4.3 includes or excludes a kernel from.
type Artifact struct {
	Backend BackendName
	Arch    string
}

func (a Artifact) String() string { return string(a.Backend) + "[" + a.Arch + "]" }

// Artifacts expands the module's target-decl into the artifact set,
// substituting the §3 defaults for an omitted ptx or msl arch list. An
// amdgcn entry with no archs contributes nothing — that is a verification
// error, reported there rather than papered over here.
func (m *Module) Artifacts() []Artifact {
	var out []Artifact
	for _, b := range m.Targets {
		archs := b.Archs
		if len(archs) == 0 {
			switch b.Name {
			case PTX:
				archs = []string{DefaultPTXArch}
			case MSL:
				archs = []string{DefaultMSLArch}
			default:
				continue
			}
		}
		for _, a := range archs {
			out = append(out, Artifact{Backend: b.Name, Arch: a})
		}
	}
	return out
}

// --- capability gating (§4.3) ----------------------------------------------

// Feature is a gated language feature. This is the whole list: §4.3 is the
// only gating mechanism, and there is no module-wide rejection.
type Feature uint8

const (
	FeatureBF16 Feature = iota
	FeatureF64
	FeatureSubgroupSize
)

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

// FeatureOfType returns the gated feature a type carries, if any.
func FeatureOfType(t Type) (Feature, bool) {
	if f, ok := ElemOrSelf(t).(FloatType); ok {
		switch f.Kind {
		case KindBF16:
			return FeatureBF16, true
		case KindF64:
			return FeatureF64, true
		}
	}
	return 0, false
}

// Available reports whether feature f is available on artifact a.
func Available(f Feature, a Artifact) bool {
	switch f {
	case FeatureBF16:
		switch a.Backend {
		case PTX:
			return smVersion(a.Arch) >= 80
		case AMDGCN:
			return AMDGCNArchs[a.Arch].BF16
		case MSL:
			return MSLArchs[a.Arch] >= MSLArchs["metal31"]
		}
	case FeatureF64:
		return a.Backend != MSL
	case FeatureSubgroupSize:
		return a.Backend != MSL
	}
	return false
}

// smVersion parses "sm_90a" as 90. Returns -1 for a non-sm identifier.
func smVersion(arch string) int {
	if !strings.HasPrefix(arch, "sm_") {
		return -1
	}
	n, digits := 0, 0
	for _, c := range arch[3:] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		digits++
	}
	if digits == 0 {
		return -1
	}
	return n
}

// SubgroupWidths lists the widths a subgroup_size attribute may request on
// a. Empty means the attribute is not expressible there — msl, which is
// why it is gated (§9.2).
func SubgroupWidths(a Artifact) []int {
	switch a.Backend {
	case PTX:
		return []int{32} // fixed; subgroup_size N with N != 32 is rejected
	case AMDGCN:
		return AMDGCNArchs[a.Arch].Wave
	}
	return nil
}

// --- resource limits (§6.5) ------------------------------------------------

// GroupMemoryLimit is the per-group byte budget for static group plus
// dynamic_group. Exceeding it is a lowering error for that artifact — not
// UB and not a gated exclusion. The ptx figure is the default; specific
// arches allow a higher opt-in.
func GroupMemoryLimit(b BackendName) int {
	switch b {
	case PTX:
		return 48 << 10
	case AMDGCN:
		return 64 << 10
	case MSL:
		return 32 << 10
	}
	return 0
}

// PortableGroupLimit is the figure to stay under to lower everywhere.
const PortableGroupLimit = 32 << 10