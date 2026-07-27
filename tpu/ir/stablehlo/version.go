package stablehlo

import "fmt"

// Version is a StableHLO opset version ({Major, Minor, Patch}).
// The minor version is bumped on every opset or serialization-format
// change; see VhloDialect.td for the authoritative log.
type Version struct {
	Major, Minor, Patch int
}

// GTE reports whether v >= o.
func (v Version) GTE(o Version) bool {
	if v.Major != o.Major {
		return v.Major > o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor > o.Minor
	}
	return v.Patch >= o.Patch
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// IsZero reports whether v is the zero Version (meaning "no constraint").
func (v Version) IsZero() bool { return v == Version{} }

// Landmark version constants. Any other released version can be
// constructed directly: stablehlo.Version{1, 12, 0}.
var (
	// V0_9 is the first version with a forward/backward compatibility window.
	V0_9 = Version{0, 9, 0}
	// V1_0 is where the 5yr backward / 2yr forward guarantees begin.
	V1_0 = Version{1, 0, 0}
)

// DefaultVersion is the opset version this package was last audited
// against. New modules default to it as their advisory TargetVersion.
var DefaultVersion = Version{1, 12, 0}

// opMinVersion maps op names to the opset version that introduced them,
// per the VhloDialect.td version log. Advisory; used only by the
// printer's linter. Ops absent from the table are assumed to predate V0_9.
var opMinVersion = map[string]Version{
	"stablehlo.composite": {0, 19, 0},
	"stablehlo.tan":       {1, 4, 0},
}

// MinVersionForOp returns the opset version that introduced the named op,
// if this package knows a constraint for it.
func MinVersionForOp(name string) (Version, bool) {
	v, ok := opMinVersion[name]
	return v, ok
}

// elemMinVersion maps element-type spellings (as they appear immediately
// before the closing '>' of a tensor type) to the version that introduced
// them.
var elemMinVersion = []struct {
	needle string // elem spelling + ">", to avoid prefix collisions (f8E4M3 vs f8E4M3FN)
	name   string
	min    Version
}{
	{"si2>", "si2", Version{1, 2, 0}},
	{"ui2>", "ui2", Version{1, 2, 0}},
	{"f8E4M3>", "f8E4M3", Version{1, 7, 0}},
	{"f8E3M4>", "f8E3M4", Version{1, 7, 0}},
	{"f4E2M1FN>", "f4E2M1FN", Version{1, 8, 0}},
	{"f6E2M3FN>", "f6E2M3FN", Version{1, 8, 0}},
	{"f6E3M2FN>", "f6E3M2FN", Version{1, 8, 0}},
	{"f8E8M0FNU>", "f8E8M0FNU", Version{1, 8, 0}},
}

// TypeFeatureVersion inspects the canonical spelling of t and reports the
// newest opset version any of its element types requires, if any.
func TypeFeatureVersion(t Type) (feature string, min Version, ok bool) {
	if t == nil {
		return "", Version{}, false
	}
	s := t.String()
	for _, e := range elemMinVersion {
		if containsStr(s, e.needle) && (!ok || e.min.GTE(min)) {
			feature, min, ok = e.name, e.min, true
		}
	}
	// Per-axis quantization (0.18.0) is spelled "!quant.uniform<...:dim, ...>".
	if containsStr(s, "!quant.uniform<") && containsStr(s, "{") {
		if !ok || (Version{0, 18, 0}).GTE(min) {
			feature, min, ok = "per-axis quantization", Version{0, 18, 0}, true
		}
	}
	return feature, min, ok
}

func containsStr(s, sub string) bool {
	return len(sub) <= len(s) && indexStr(s, sub) >= 0
}

func indexStr(s, sub string) int {
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}