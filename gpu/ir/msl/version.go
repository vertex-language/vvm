package msl

import "fmt"

// Version identifies an MSL language revision. It is advisory: MSL has no
// in-source version pragma; the version maps to the -std= compiler flag
// and is used only for linting by the text printer.
//
// Pre-3.0 split revisions (ios-metal2.x / macos-metal2.x) can be spelled
// via the OS field but are not given named constants.
type Version struct {
	Major, Minor int
	OS           string // "" (unified), "ios", or "macos" for pre-3.0 revisions
}

// Named language revisions.
var (
	Metal23 = Version{Major: 2, Minor: 3} // iOS 14 / macOS 11: function pointers, intersection fns
	Metal24 = Version{Major: 2, Minor: 4} // iOS 15 / macOS 12
	Metal30 = Version{Major: 3, Minor: 0} // iOS 16 / macOS 13: unified revision, mesh shaders
	Metal31 = Version{Major: 3, Minor: 1} // iOS 17 / macOS 14: bfloat, atomic texture ops
	Metal32 = Version{Major: 3, Minor: 2} // iOS 18 / macOS 15: shader logging — current default
	Metal40 = Version{Major: 4, Minor: 0} // iOS 26 / macOS 26: tensors, cooperative tensors, Shader ML
	Metal41 = Version{Major: 4, Minor: 1} // iOS 27 / macOS 27
)

// GTE reports whether v is the same revision as or a later revision than o.
// The OS field is ignored for comparison.
func (v Version) GTE(o Version) bool {
	if v.Major != o.Major {
		return v.Major > o.Major
	}
	return v.Minor >= o.Minor
}

// Std returns the value for the metal compiler's -std= flag,
// e.g. "metal3.2" or "ios-metal2.4".
func (v Version) Std() string {
	if v.OS != "" {
		return fmt.Sprintf("%s-metal%d.%d", v.OS, v.Major, v.Minor)
	}
	return fmt.Sprintf("metal%d.%d", v.Major, v.Minor)
}

// String implements fmt.Stringer.
func (v Version) String() string { return v.Std() }