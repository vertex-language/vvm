// vvm.go
//
// Package vvm is the top-level, dev-facing entry point for the vvm
// toolchain: "give me .vbyte bytes, .vir text, .gvir text, or an
// already-built *vir.Module/*gvir.Module, and either build me a binary,
// run it, or emit device artifacts." Everything below this package
// (ir/vir, ir/gvir, importer, lower/<arch>, gpu/lower/<backend>,
// object/<arch>, objectwriter/<arch>, linker/<format>) stays
// independently importable and knows nothing about this package; this is
// the one place allowed to import all of them at once and pick the right
// combination for a given target.
//
// Host and device are two separate pipelines that never meet here. A
// .vir/.vbyte module goes through Build/BuildGraph/Run (build.go,
// graph.go, run.go) and ends in a native binary; a .gvir module goes
// through BuildDevice (gpu.go) and ends in one or more vendor-toolchain
// source artifacts. There is no host-side launcher, no device-side
// linker, and no build that consumes both — the decoders below enforce
// that split at the front door rather than letting a mismatched module
// fail somewhere deep in a lowering backend.
package vvm

import (
	"bytes"
	"fmt"

	gvirtext "github.com/vertex-language/vvm/format/gvbyte/text"
	"github.com/vertex-language/vvm/format/vbyte/binary"
	virtext "github.com/vertex-language/vvm/format/vbyte/text"
	"github.com/vertex-language/vvm/ir/gvir"
	"github.com/vertex-language/vvm/ir/vir"
)

// SourceKind distinguishes the two IRs vvm accepts. It's derived purely
// from the bytes — never from a file extension, never from a CLI flag —
// so a caller holding an unnamed []byte can always route correctly.
type SourceKind int

const (
	// SourceHost is .vbyte or .vir: decodes to a *vir.Module, builds to a
	// native binary.
	SourceHost SourceKind = iota
	// SourceDevice is .gvir: decodes to a *gvir.Module, builds to PTX /
	// amdtx / MSL artifacts.
	SourceDevice
)

func (k SourceKind) String() string {
	if k == SourceDevice {
		return "device (.gvir)"
	}
	return "host (.vir/.vbyte)"
}

// SourceKindOf sniffs which IR src holds, without fully decoding it.
//
// The three cases are mutually exclusive by construction:
//
//   - .vbyte opens with its documented magic, "VBYT".
//   - .vir text always opens with the "module" keyword (§ module decl is
//     the first mandatory section).
//   - .gvir text always opens with "gvir" — gvir's mandatory section
//     order is version, module, target, float profile, structs, consts,
//     funcs, kernels (see ir/gvir/README.md), and the version-decl
//     production itself is spelled `"gvir" int-literal "." int-literal`
//     (format/gvbyte/text/decode.go's parseModule calls
//     p.expectIdentVal("gvir") first, not "version") — so the literal
//     token "gvir" is the first thing in every well-formed device module.
//
// Anything else is rejected here rather than handed to a decoder that
// would produce a confusing syntax error deep in a grammar it was never
// given the right language for.
func SourceKindOf(src []byte) (SourceKind, error) {
	if bytes.HasPrefix(src, []byte("VBYT")) {
		return SourceHost, nil
	}
	switch firstWord(src) {
	case "module":
		return SourceHost, nil
	case "gvir":
		return SourceDevice, nil
	case "":
		return 0, fmt.Errorf("empty source: expected a .vbyte, .vir, or .gvir module")
	default:
		return 0, fmt.Errorf(
			"unrecognized source: expected the \"VBYT\" magic (.vbyte), a leading "+
				"\"module\" keyword (.vir), or a leading \"gvir\" keyword (.gvir), got %q",
			firstWord(src))
	}
}

// firstWord returns the first whitespace-delimited token, skipping any
// leading blank lines and // comments. Both text grammars allow a comment
// or blank line before the first real declaration, so the sniff has to
// look past them.
func firstWord(src []byte) string {
	i := 0
	for i < len(src) {
		// Skip whitespace.
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
			i++
		}
		// Skip a line comment, if that's what's here.
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		break
	}
	start := i
	for i < len(src) && src[i] != ' ' && src[i] != '\t' && src[i] != '\r' && src[i] != '\n' {
		i++
	}
	return string(src[start:i])
}

// decodeModule accepts either *host* serialization vvm knows how to read
// and returns an unverified *vir.Module — verification is the caller's
// job, done centrally in build.go/graph.go.
//
// A .gvir device module is refused here rather than silently mis-parsed.
// That single check is what keeps Build, BuildGraph, and Run from ever
// seeing a device module: none of them need their own guard.
func decodeModule(src []byte) (*vir.Module, error) {
	kind, err := SourceKindOf(src)
	if err != nil {
		return nil, err
	}
	if kind == SourceDevice {
		return nil, fmt.Errorf(
			"this is a .gvir device module — the host pipeline (Build/BuildGraph/Run) " +
				"has no lowering, linking, or entry-point story for device kernels; " +
				"use BuildDevice/BuildDeviceModule instead")
	}
	if bytes.HasPrefix(src, []byte("VBYT")) {
		return binary.Decode(src)
	}
	return virtext.Decode(src)
}

// decodeDeviceModule is decodeModule's device-side counterpart: it
// accepts .gvir text and returns an *unverified* *gvir.Module.
//
// "Unverified" is load-bearing and, for now, permanent: there is no
// device verifier wired up yet. ir/verify owns name binding, merge
// annotations, the uniformity analysis, and §4.3 capability gating for
// gvir, and until that lands BuildDevice hands the decoded module
// straight to gpu/lower. A malformed-but-parseable device module will
// therefore fail (or worse, not fail) inside a lowering backend rather
// than at a clean verification seam. This is a known gap, not a design
// stance — the call site in gpu.go is where the verify.Verify call goes
// when it exists.
//
// There is deliberately no gvbyte/binary counterpart to sniff for: the
// Vertex GPU IR spec defines .gvir as a text format and names no byte
// encoding (see format/README.md), so text is the whole tree today.
func decodeDeviceModule(src []byte) (*gvir.Module, error) {
	kind, err := SourceKindOf(src)
	if err != nil {
		return nil, err
	}
	if kind == SourceHost {
		return nil, fmt.Errorf(
			"this is a host .vir/.vbyte module — the device pipeline (BuildDevice) " +
				"only consumes .gvir kernels; use Build/BuildGraph/Run instead")
	}
	return gvirtext.Decode(src)
}

// ModuleTarget decodes src just far enough to report the target triple
// its own in-file target-decl states (§10.6), without running Verify and
// without requiring the caller to already know a Target to build for.
//
// This is the *host* accessor only; DeviceTargets (gpu.go) is the .gvir
// equivalent, and returns a list rather than a single triple, since a
// device module's target section is mandatory and multi-valued.
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