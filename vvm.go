// vvm.go
//
// Package vvm is the top-level, dev-facing entry point for the vvm
// toolchain: "give me .vbyte bytes, .vir text, or an already-built
// *vir.Module, and either build me a binary or run it." Everything
// below this package (ir/vir, importer, lower/<arch>, object/<arch>,
// objectwriter/<arch>, linker/<format>) stays independently importable
// and knows nothing about this package; this is the one place allowed
// to import all of them at once and pick the right combination for a
// given target.
package vvm

import (
	"bytes"

	"github.com/vertex-language/vvm/format/vbyte/binary"
	"github.com/vertex-language/vvm/format/vbyte/text"
	"github.com/vertex-language/vvm/ir/vir"
)

// decodeModule accepts either serialization vvm knows how to read and
// returns an unverified *vir.Module — verification is the caller's job,
// done centrally in build.go/graph.go.
//
// .vbyte is sniffed by its documented magic ("VBYT"); anything else is
// handed to the text decoder. There's no ambiguity case: a real .vir file
// can never start with those four bytes, since '.vir' text always opens
// with the "module" keyword.
func decodeModule(src []byte) (*vir.Module, error) {
	if bytes.HasPrefix(src, []byte("VBYT")) {
		return binary.Decode(src)
	}
	return text.Decode(src)
}

// ModuleTarget decodes src just far enough to report the target triple
// its own in-file target-decl states (§10.6), without running Verify and
// without requiring the caller to already know a Target to build for.
//
// ok is false for pure-compute modules — no link section, no asm block —
// which carry no target-decl at all and remain buildable for any triple
// via build flags alone. It is also false, along with a non-nil err, if
// src doesn't even decode; the caller should surface that error rather
// than treat it as "no target declared."
func ModuleTarget(src []byte) (t Target, ok bool, err error) {
	m, err := decodeModule(src)
	if err != nil {
		return Target{}, false, err
	}
	if m.Target == nil {
		return Target{}, false, nil
	}
	return Target{
		Arch: m.Target.Arch,
		OS:   m.Target.OS,
		ABI:  m.Target.ABI,
		Tier: append([]string(nil), m.Target.Tiers...),
	}, true, nil
}