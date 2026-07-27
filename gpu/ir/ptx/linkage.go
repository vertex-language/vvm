package ptx

// Linkage is a symbol linking directive.
type Linkage int

const (
	Default Linkage = iota // (none)
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