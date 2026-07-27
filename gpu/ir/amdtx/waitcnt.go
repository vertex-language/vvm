package amdtx

import "fmt"

// WaitKind selects which hardware counter an s_waitcnt targets.
type WaitKind int

const (
	WaitVM   WaitKind = iota // vmcnt   — vector memory (global/buffer) loads
	WaitLGKM                 // lgkmcnt — LDS, GDS, constant (scalar) & message
	WaitEXP                  // expcnt  — export / GDS
	WaitVS                   // vscnt   — vector stores (GFX10+)
)

// Waitcnt is a single counter target with a threshold to wait down to.
type Waitcnt struct {
	Kind  WaitKind
	Count int
}

func (w Waitcnt) String() string {
	switch w.Kind {
	case WaitVM:
		return fmt.Sprintf("vmcnt(%d)", w.Count)
	case WaitLGKM:
		return fmt.Sprintf("lgkmcnt(%d)", w.Count)
	case WaitEXP:
		return fmt.Sprintf("expcnt(%d)", w.Count)
	case WaitVS:
		return fmt.Sprintf("vscnt(%d)", w.Count)
	}
	return "waitcnt(?)"
}

// VMcnt waits for outstanding vector-memory loads to drop to n.
func VMcnt(n int) Waitcnt { return Waitcnt{Kind: WaitVM, Count: n} }

// LGKMcnt waits for scalar/LDS/message traffic to drop to n.
func LGKMcnt(n int) Waitcnt { return Waitcnt{Kind: WaitLGKM, Count: n} }

// EXPcnt waits for exports to drop to n.
func EXPcnt(n int) Waitcnt { return Waitcnt{Kind: WaitEXP, Count: n} }

// VScnt waits for vector stores to drop to n (GFX10+).
func VScnt(n int) Waitcnt { return Waitcnt{Kind: WaitVS, Count: n} }