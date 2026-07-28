package ptx

// SubQual is a state-space sub-qualifier: ".shared::cluster",
// ".param::entry", and so on.
type SubQual uint8

const (
	NoSub SubQual = iota
	SubCTA
	SubCluster
	SubEntry
	SubFunc
)

func (q SubQual) String() string {
	switch q {
	case SubCTA:
		return "cta"
	case SubCluster:
		return "cluster"
	case SubEntry:
		return "entry"
	case SubFunc:
		return "func"
	}
	return ""
}

// Space is a PTX state space, optionally carrying a sub-qualifier. The base
// space occupies the low byte and the sub-qualifier the high byte, so Space
// values are constants and remain comparable with ==.
//
// The register and parameter spaces are named RegSpace and ParamSpace
// because Reg is the register-value type and Param is the parameter type.
type Space uint16

const (
	NoSpace Space = iota
	RegSpace
	SRegSpace
	Const
	Global
	Local
	ParamSpace
	Shared
	Tex // deprecated in PTX; retained for backward compatibility
)

// Sub returns s with a sub-qualifier attached:
// Shared.Sub(SubCluster) renders as ".shared::cluster".
func (s Space) Sub(q SubQual) Space { return s.Base() | Space(q)<<8 }

// Base returns s stripped of any sub-qualifier.
func (s Space) Base() Space { return s & 0xFF }

// SubQual returns the sub-qualifier attached to s, or NoSub.
func (s Space) SubQual() SubQual { return SubQual(s >> 8) }

var spaceNames = map[Space]string{
	RegSpace:   "reg",
	SRegSpace:  "sreg",
	Const:      "const",
	Global:     "global",
	Local:      "local",
	ParamSpace: "param",
	Shared:     "shared",
	Tex:        "tex",
}

// String returns the dotted space specifier, e.g. ".shared::cluster".
func (s Space) String() string {
	n, ok := spaceNames[s.Base()]
	if !ok {
		return ""
	}
	if q := s.SubQual(); q != NoSub {
		return "." + n + "::" + q.String()
	}
	return "." + n
}

// Name returns the bare base space name without the leading dot.
func (s Space) Name() string { return spaceNames[s.Base()] }

// IsValid reports whether s names a real state space.
func (s Space) IsValid() bool { _, ok := spaceNames[s.Base()]; return ok }

// legalSub records which sub-qualifiers each base space accepts. Verify uses
// this; the builder does not enforce it.
var legalSub = map[Space]map[SubQual]bool{
	Shared:     {SubCTA: true, SubCluster: true},
	ParamSpace: {SubEntry: true, SubFunc: true},
}