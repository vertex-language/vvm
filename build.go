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

	f, err := t.objFormat()
	if err != nil {
		return nil, err
	}

	obj, err := toObjectBytes(m, t, f)
	if err != nil {
		return nil, err
	}

	l, err := newLinker(t)
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