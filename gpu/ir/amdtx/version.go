package amdtx

import (
	"fmt"
	"strconv"
)

// Version is the language version emitted as ".amdtx <Major>.<Minor>".
type Version struct{ Major, Minor int }

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// GTE reports whether v is at least o.
func (v Version) GTE(o Version) bool {
	return v.Major > o.Major || (v.Major == o.Major && v.Minor >= o.Minor)
}

// IsZero reports whether v is the unset version.
func (v Version) IsZero() bool { return v.Major == 0 && v.Minor == 0 }

// V10 is AMDTX 1.0, the only version this package models.
var V10 = Version{1, 0}

// Wave is the wavefront width selected by the .wave directive. It is fixed
// per module and resolved before any width-dependent rule is checked,
// because .lanemask, %exec, %vcc and the legal cross-lane index range all
// follow from it (§4.1).
type Wave uint8

const (
	NoWave Wave = 0
	Wave32 Wave = 32
	Wave64 Wave = 64
)

func (w Wave) String() string  { return strconv.Itoa(int(w)) }
func (w Wave) Bits() int       { return int(w) }
func (w Wave) IsValid() bool   { return w == Wave32 || w == Wave64 }

// MaskWidth returns the physical width of a .lanemask under w.
func (w Wave) MaskWidth() Width {
	if w == Wave32 {
		return B32
	}
	return B64
}