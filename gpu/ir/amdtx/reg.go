package amdtx

import "fmt"

// RegClass is the storage class of a virtual register.
type RegClass int

const (
	SGPR32 RegClass = iota // scalar, 1 dword
	SGPR64                 // scalar pair
	SGPRn                  // scalar tuple (Width dwords)
	VGPR32                 // vector, 1 dword
	VGPR64                 // vector pair
	VGPRn                  // vector tuple
	AGPRn                  // accumulation VGPR tuple (CDNA MFMA)
	Special                // EXEC/VCC/SCC/M0/FLAT_SCRATCH and helpers
)

// Reg is a virtual register reference. Physical assignment happens in
// lower/amdgcn; until then Num is a dense virtual id within its class.
type Reg struct {
	Class RegClass
	Num   int    // virtual number, 1-based (0 = invalid)
	Width int    // dwords occupied
	Name  string // optional symbolic name (%idx)
	spec  special
}

type special int

const (
	specNone special = iota
	specEXEC
	specVCC
	specSCC
	specM0
	specFlatScratch
	specKernargPtr
	specWorkgroupIDX
	specWorkgroupIDY
	specWorkgroupIDZ
	specWorkitemIDX
	specWorkitemIDY
	specWorkitemIDZ
)

// Valid reports whether the register reference is usable.
func (r Reg) Valid() bool { return r.spec != specNone || r.Num > 0 }

func (Reg) isOperand() {}

func (r Reg) textForm() string {
	if r.Name != "" && r.spec == specNone {
		return "%" + r.Name
	}
	switch r.spec {
	case specEXEC:
		return "%exec"
	case specVCC:
		return "%vcc"
	case specSCC:
		return "%scc"
	case specM0:
		return "%m0"
	case specFlatScratch:
		return "%flat_scratch"
	case specKernargPtr:
		return "%kernarg_ptr"
	case specWorkgroupIDX:
		return "%wgid.x"
	case specWorkgroupIDY:
		return "%wgid.y"
	case specWorkgroupIDZ:
		return "%wgid.z"
	case specWorkitemIDX:
		return "%tid.x"
	case specWorkitemIDY:
		return "%tid.y"
	case specWorkitemIDZ:
		return "%tid.z"
	}
	switch r.Class {
	case SGPR32:
		return fmt.Sprintf("%%s%d", r.Num)
	case SGPR64:
		return fmt.Sprintf("%%sd%d", r.Num)
	case SGPRn:
		return fmt.Sprintf("%%sq%d", r.Num)
	case VGPR32:
		return fmt.Sprintf("%%v%d", r.Num)
	case VGPR64:
		return fmt.Sprintf("%%vd%d", r.Num)
	case VGPRn:
		return fmt.Sprintf("%%vq%d", r.Num)
	case AGPRn:
		return fmt.Sprintf("%%aq%d", r.Num)
	}
	return "%?"
}

// IsVector reports whether the register lives in the VGPR/AGPR file.
func (r Reg) IsVector() bool {
	return r.Class == VGPR32 || r.Class == VGPR64 || r.Class == VGPRn || r.Class == AGPRn
}

// Well-known physical registers, referenceable directly in the IR.
var (
	EXEC        = Reg{Class: Special, spec: specEXEC, Width: 2}
	VCC         = Reg{Class: Special, spec: specVCC, Width: 2}
	SCC         = Reg{Class: Special, spec: specSCC, Width: 1}
	M0          = Reg{Class: Special, spec: specM0, Width: 1}
	FlatScratch = Reg{Class: Special, spec: specFlatScratch, Width: 2}
)

// RegFile hands out virtual registers, one dense counter per class.
type RegFile struct {
	sgpr int
	vgpr int
	agpr int
}

func (rf *RegFile) SGPR() Reg { rf.sgpr++; return Reg{Class: SGPR32, Num: rf.sgpr, Width: 1} }
func (rf *RegFile) VGPR() Reg { rf.vgpr++; return Reg{Class: VGPR32, Num: rf.vgpr, Width: 1} }

func (rf *RegFile) SGPRPair() Reg { rf.sgpr++; return Reg{Class: SGPR64, Num: rf.sgpr, Width: 2} }
func (rf *RegFile) VGPRPair() Reg { rf.vgpr++; return Reg{Class: VGPR64, Num: rf.vgpr, Width: 2} }

func (rf *RegFile) SGPRTuple(n int) Reg {
	rf.sgpr++
	return Reg{Class: SGPRn, Num: rf.sgpr, Width: n}
}
func (rf *RegFile) VGPRTuple(n int) Reg {
	rf.vgpr++
	return Reg{Class: VGPRn, Num: rf.vgpr, Width: n}
}

// AGPR allocates a single accumulation VGPR (CDNA MFMA only).
func (rf *RegFile) AGPR() Reg { rf.agpr++; return Reg{Class: AGPRn, Num: rf.agpr, Width: 1} }

// AGPRTuple allocates an n-dword accumulation VGPR tuple.
func (rf *RegFile) AGPRTuple(n int) Reg {
	rf.agpr++
	return Reg{Class: AGPRn, Num: rf.agpr, Width: n}
}

// Named allocates a register of the given class with a symbolic name.
func (rf *RegFile) Named(class RegClass, name string) Reg {
	var r Reg
	switch class {
	case SGPR32, SGPR64, SGPRn:
		rf.sgpr++
		r = Reg{Class: class, Num: rf.sgpr}
	default:
		rf.vgpr++
		r = Reg{Class: class, Num: rf.vgpr}
	}
	r.Width = widthOf(class)
	r.Name = name
	return r
}

func widthOf(c RegClass) int {
	switch c {
	case SGPR64, VGPR64:
		return 2
	default:
		return 1
	}
}