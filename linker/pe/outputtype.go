package pe

import "fmt"

// OutputType is the kind of PE image being produced.
type OutputType uint8

const (
	OutputExec OutputType = iota + 1 // .exe, fixed base, no base relocations
	OutputPIE                        // .exe, relocatable base (ASLR-friendly)
	OutputShared                     // .dll, has its own export directory
)

func (o OutputType) String() string {
	switch o {
	case OutputExec:
		return "exec"
	case OutputPIE:
		return "pie"
	case OutputShared:
		return "shared"
	default:
		return fmt.Sprintf("OutputType(%d)", uint8(o))
	}
}