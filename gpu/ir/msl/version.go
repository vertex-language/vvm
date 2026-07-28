package msl

import "fmt"

// Version identifies an MSL language revision. MSL has no in-source version
// pragma; the version maps to the -std= flag, to the __METAL_VERSION__
// preprocessor macro, and to the feature floors Verify enforces.
//
// Pre-3.0 split revisions (ios-metal2.x / macos-metal2.x) are spelled via OS.
type Version struct {
	Major, Minor int
	OS           string // "" (unified), "ios", or "macos"
}

// Named language revisions.
var (
	Metal23 = Version{Major: 2, Minor: 3} // function pointers, intersection functions
	Metal24 = Version{Major: 2, Minor: 4}
	Metal30 = Version{Major: 3, Minor: 0} // unified revision, mesh shaders
	Metal31 = Version{Major: 3, Minor: 1} // bfloat, atomic texture ops
	Metal32 = Version{Major: 3, Minor: 2} // lambdas, auto, shader logging
	Metal40 = Version{Major: 4, Minor: 0} // tensors, cooperative tensors, Shader ML
	Metal41 = Version{Major: 4, Minor: 1} // low-precision float formats
)

// IsZero reports whether v is the zero Version.
func (v Version) IsZero() bool { return v.Major == 0 && v.Minor == 0 }

// GTE reports whether v is the same revision as or later than o. OS is ignored.
func (v Version) GTE(o Version) bool {
	if v.Major != o.Major {
		return v.Major > o.Major
	}
	return v.Minor >= o.Minor
}

// Macro returns the __METAL_VERSION__ value for this revision: Metal41 is 410.
// Use it to build preprocessor gates; see VersionGate.
func (v Version) Macro() int { return v.Major*100 + v.Minor*10 }

// Std returns the -std= value, e.g. "metal4.1" or "ios-metal2.4".
func (v Version) Std() string {
	if v.OS != "" {
		return fmt.Sprintf("%s-metal%d.%d", v.OS, v.Major, v.Minor)
	}
	return fmt.Sprintf("metal%d.%d", v.Major, v.Minor)
}

func (v Version) String() string { return v.Std() }