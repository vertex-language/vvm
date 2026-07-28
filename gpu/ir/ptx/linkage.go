package ptx

// Linkage is a symbol linking directive.
type Linkage uint8

const (
	Default Linkage = iota // no directive
	Visible                // .visible
	Extern                 // .extern
	Weak                   // .weak
	Common                 // .common (variables only)
)

func (l Linkage) String() string {
	switch l {
	case Visible:
		return ".visible"
	case Extern:
		return ".extern"
	case Weak:
		return ".weak"
	case Common:
		return ".common"
	}
	return ""
}

// Attr is a variable or function .attribute qualifier.
type Attr uint8

const (
	Managed Attr = iota + 1
	Unified
)

func (a Attr) String() string {
	switch a {
	case Managed:
		return ".managed"
	case Unified:
		return ".unified"
	}
	return ""
}