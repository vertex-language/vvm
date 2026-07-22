// target.go
package verify

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// checkTarget enforces §7.1: canonical arch/os/abi only. Aliases resolve
// exclusively at the build-system boundary — the IR's own grammar never
// accepts one, so seeing an alias here is always an error, never a thing
// to silently canonicalize.
func checkTarget(m *vir.Module) error {
	if m.Target == nil {
		return nil // optional unless a `link` is present — checked in checkLinks
	}
	t := m.Target
	if !vir.CanonicalArch[t.Arch] {
		if canon, ok := vir.ArchAliases[t.Arch]; ok {
			return fmt.Errorf("target: arch %q is a rejected alias for %q (§7.1) — use the canonical spelling", t.Arch, canon)
		}
		return fmt.Errorf("target: unknown arch %q (§7.1)", t.Arch)
	}
	if !vir.CanonicalOS[t.OS] {
		if canon, ok := vir.OSAliases[t.OS]; ok {
			return fmt.Errorf("target: os %q is a rejected alias for %q (§7.1) — use the canonical spelling", t.OS, canon)
		}
		return fmt.Errorf("target: unknown os %q (§7.1)", t.OS)
	}
	if t.ABI != "" && !vir.CanonicalABI[t.ABI] {
		return fmt.Errorf("target: unknown abi %q (§7.1)", t.ABI)
	}
	return nil
}