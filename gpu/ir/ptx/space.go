package ptx

// SubQual is a state-space sub-qualifier (".shared::cluster",
// ".param::entry", ...).
type SubQual string

const (
	Cta     SubQual = "cta"
	Cluster SubQual = "cluster"
	Entry   SubQual = "entry"
	Func    SubQual = "func"
)

// Space is a PTX state space, optionally with a sub-qualifier.
//
// Note: the register and parameter spaces are named RegSpace and
// ParamSpace because Reg and Param are the register-value and
// kernel-parameter types in this package.
type Space struct {
	name string
	sub  SubQual
}

func (s Space) String() string {
	if s.sub != "" {
		return "." + s.name + "::" + string(s.sub)
	}
	return "." + s.name
}

// Sub returns the space with a sub-qualifier attached:
// Shared.Sub(Cluster) -> ".shared::cluster".
func (s Space) Sub(q SubQual) Space { s.sub = q; return s }

// Name returns the bare space name without the leading dot.
func (s Space) Name() string { return s.name }

var (
	RegSpace   = Space{name: "reg"}
	SReg       = Space{name: "sreg"}
	Const      = Space{name: "const"}
	Global     = Space{name: "global"}
	Local      = Space{name: "local"}
	ParamSpace = Space{name: "param"}
	Shared     = Space{name: "shared"}
)