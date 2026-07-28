package amdtx

import "strconv"

// CounterKind is a hardware wait counter. vscnt is not part of s_waitcnt:
// on GFX10 and GFX11 it is a separate instruction, so AMDTX writes it as a
// distinct mnemonic (§12.2).
type CounterKind uint8

const (
	NoCounter CounterKind = iota
	VMCnt                 // vector memory loads
	LGKMCnt               // LDS, scalar memory, message
	VSCnt                 // vector memory stores; GFX10/GFX11 only
)

func (k CounterKind) String() string {
	return [...]string{"", "vmcnt", "lgkmcnt", "vscnt"}[k]
}

// Counter is one "name(N)" entry in a waitcnt counter list.
type Counter struct {
	Kind CounterKind
	N    int
}

func (c Counter) Text() string {
	return c.Kind.String() + "(" + strconv.Itoa(c.N) + ")"
}
func (Counter) operand() {}

// VM, LGKM and VS build counter operands.
func VM(n int) Counter   { return Counter{VMCnt, n} }
func LGKM(n int) Counter { return Counter{LGKMCnt, n} }
func VS(n int) Counter   { return Counter{VSCnt, n} }

// Ordering is a fence memory ordering.
type Ordering uint8

const (
	NoOrdering Ordering = iota
	Acquire
	Release
	AcqRel
	SeqCst
)

func (o Ordering) String() string {
	return [...]string{"", ".acquire", ".release", ".acq_rel", ".seq_cst"}[o]
}

// Scope is an AMDHSA synchronisation scope, optionally restricted to a
// single address space. The base scope occupies the low byte and the
// one_as flag a high bit, so Scope values are constants and stay comparable
// with ==.
type Scope uint16

const (
	NoScope Scope = iota
	System
	Agent
	Workgroup
	Wavefront
	SingleThread
)

const oneAsBit Scope = 0x100

// OneAs returns s restricted to a single address space.
func (s Scope) OneAs() Scope { return s.Base() | oneAsBit }

// Base returns s stripped of the one_as restriction.
func (s Scope) Base() Scope { return s &^ oneAsBit }

// IsOneAs reports whether s is address-space restricted.
func (s Scope) IsOneAs() bool { return s&oneAsBit != 0 }

var scopeNames = map[Scope]string{
	System:       "system",
	Agent:        "agent",
	Workgroup:    "workgroup",
	Wavefront:    "wavefront",
	SingleThread: "singlethread",
}

func (s Scope) String() string {
	n, ok := scopeNames[s.Base()]
	if !ok {
		return ""
	}
	if s.IsOneAs() {
		return "." + n + ".one_as"
	}
	return "." + n
}

// IsValid reports whether s names a real scope.
func (s Scope) IsValid() bool { _, ok := scopeNames[s.Base()]; return ok }

// Uniformity is an optional assertion on a structured guard. It is an
// assertion, not a declaration: if it disagrees with the operand's register
// class, verification fails (§10.1).
type Uniformity uint8

const (
	NoUniformity Uniformity = iota
	UniformGuard
	DivergentGuard
)

func (u Uniformity) String() string {
	return [...]string{"", ".uniform", ".divergent"}[u]
}