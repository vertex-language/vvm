// crt.go
package crt

import "fmt"

// MainSignature mirrors vir.MainSignature's three recognized shapes
// without importing ir/vir — this package only needs to know which
// argument-staging sequence to emit, not anything about the IR that
// decided it (see README.md for why crt sits below vir entirely).
type MainSignature int

const (
	SignatureBare MainSignature = iota
	SignatureArgcArgv
	SignatureArgcArgvEnvp
)

// Format is the container format the stub's own object bytes must be
// serialized as, to match whatever the rest of the module was compiled
// into.
type Format int

const (
	FormatELF Format = iota
	FormatMachO
	FormatCOFF
)

func (f Format) String() string {
	switch f {
	case FormatELF:
		return "elf"
	case FormatMachO:
		return "macho"
	case FormatCOFF:
		return "coff"
	default:
		return fmt.Sprintf("Format(%d)", int(f))
	}
}

// BuildArgs is everything a stub builder needs to know.
type BuildArgs struct {
	UserMain  string // the real main()'s already-mangled/plain symbol name
	Signature MainSignature
	Format    Format
	NeedsLibC bool // true if the module links libc and must exit() through it
}

// Stub is a real relocatable object containing exactly one exported
// symbol — the process entry point — ready to hand a linker via
// AddObject alongside the module's own object.
type Stub struct {
	Symbol string
	Object []byte
}

// BuildFunc constructs a Stub for one (arch, os) pair.
type BuildFunc func(args BuildArgs) (Stub, error)

var registry = map[[2]string]BuildFunc{}

// Register adds a stub builder for (arch, os). Called from init() by
// whichever file owns that combination.
func Register(arch, os string, f BuildFunc) {
	registry[[2]string{arch, os}] = f
}

// Lookup returns the registered builder for (arch, os), if any.
func Lookup(arch, os string) (BuildFunc, bool) {
	f, ok := registry[[2]string{arch, os}]
	return f, ok
}