// build.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// Build runs the full pipeline — decode, verify, lower, object,
// objectwriter, link — and returns the finished, linked binary. src may
// be .vbyte or .vir; either is fine, decodeModule sniffs it.
func Build(src []byte, t Target) ([]byte, error) {
	m, err := decodeModule(src)
	if err != nil {
		return nil, fmt.Errorf("vvm: decode: %w", err)
	}
	return BuildModule(m, t)
}

// BuildModule is Build for a dev who's already holding a *vir.Module
// (hand-built via vir's own FunctionBuilder, say) rather than serialized
// source. Verify runs here regardless of whether the caller already
// called it — Verify is idempotent and cheap relative to lowering, and
// this package can't assume the module it's handed has actually passed.
func BuildModule(m *vir.Module, t Target) ([]byte, error) {
	if err := vir.Verify(m); err != nil {
		return nil, fmt.Errorf("vvm: verify: %w", err)
	}

	// Entry-point resolution runs after the first Verify (so it can trust
	// m.EntryFunction()'s §9.4a invariants) but before lowering, since it
	// may mutate m by adding a synthesized "_start" wrapper (entrythunk.go).
	// Re-verify afterward — cheap, and idempotent when nothing changed.
	entryPoint, err := resolveEntryPoint(m, t)
	if err != nil {
		return nil, err
	}
	if err := vir.Verify(m); err != nil {
		return nil, fmt.Errorf("vvm: verify (post entry-thunk synthesis): %w", err)
	}

	f, err := t.objFormat()
	if err != nil {
		return nil, err
	}

	obj, err := toObjectBytes(m, t, f)
	if err != nil {
		return nil, err
	}

	// newLinker needs m, not just t: whether the module used an anonymous
	// extern group (§7.4) — and therefore needs the target's default
	// symbol namespace, e.g. libc on hosted OSes — is a property of the
	// module, never something inferable from the target triple alone.
	// A target "looking hosted" (x86_64-linux-gnu) is not the trigger;
	// os=none modules can and do use named extern groups without ever
	// touching this path, and the verifier already forbids anonymous
	// groups on os=none/uefi (§1.2 rule 9), so this can never misfire
	// there.
	l, err := newLinker(m, t, entryPoint)
	if err != nil {
		return nil, err
	}
	if err := l.AddObject("vvm_module.o", obj); err != nil {
		return nil, fmt.Errorf("vvm: add object: %w", err)
	}

	out, err := l.Link()
	if err != nil {
		return nil, fmt.Errorf("vvm: link: %w", err)
	}
	return out, nil
}