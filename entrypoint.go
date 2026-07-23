// entrypoint.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/crt"
	"github.com/vertex-language/vvm/ir/vir"
)

// resolveEntryPoint decides what symbol name the linker should be told is
// the process entry point, and — if conditions are met — returns a real
// object-file Stub to be linked in alongside the module's own object.
//
// Unlike the old entrythunk.go, m is never mutated. The raw process-entry
// sequence (reading the incoming stack pointer, staging argc/argv/envp,
// issuing a bare syscall or calling libc's exit) has no vir opcode to
// express it in — §4's instruction vocabulary is closed and spec-fixed,
// deliberately with nothing for "the raw value a register held before any
// parameter binding happened." So it's built directly as machine code
// (crt package), one layer below vir entirely, instead of synthesized as
// IR that would then need a second vir.Verify pass.
//
// The gate, in order:
//  1. No `entry`-attributed fn at all → nothing to do; the module defines
//     "_start" itself, by convention.
//  2. The entry fn is itself literally named "_start" → the author opted
//     into the raw contract explicitly; never second-guess that.
//  3. Output kind isn't an executable (e.g. a shared library) → no
//     process image, no _start convention; `entry` is only a
//     documentation marker here, wire the fn's own name unwrapped.
//  4. os is "none" or "uefi" → no hosted-process convention to
//     synthesize against; wire the fn's own name, unwrapped.
//  5. The fn's signature doesn't match a recognized main() shape →
//     assume the author is hand-managing the raw ABI themselves.
//  6. No registered crt stub for (arch, os) → fail loudly rather than
//     silently doing nothing or guessing.
func resolveEntryPoint(m *vir.Module, t Target) (symbol string, stub *crt.Stub, err error) {
	ef := m.EntryFunction()
	if ef == nil {
		return "_start", nil, nil
	}
	if ef.Name == "_start" {
		return "_start", nil, nil
	}
	if t.Kind != OutputExecutable {
		return ef.Name, nil, nil
	}
	if !t.isHostedProcessOS() {
		return ef.Name, nil, nil
	}
	sig := vir.RecognizedMainSignature(ef)
	if sig == vir.MainSignatureNone {
		return ef.Name, nil, nil
	}

	build, ok := crt.Lookup(t.baseArch(), t.OS)
	if !ok {
		return "", nil, fmt.Errorf(
			"vvm: %s: no crt stub registered for automatic main() wiring — "+
				"name your entry fn \"_start\" and write the process-entry "+
				"prologue yourself, or build for a target with one registered", t)
	}
	format, err := crtFormat(t)
	if err != nil {
		return "", nil, err
	}
	s, err := build(crt.BuildArgs{
		UserMain:  ef.Name,
		Signature: toCRTSignature(sig),
		Format:    format,
		NeedsLibC: linksLibC(m),
	})
	if err != nil {
		return "", nil, err
	}
	return s.Symbol, &s, nil
}

// linksLibC reports whether m declares the conventional libc dependency
// (§7.4's own worked example: `link shared "c"`) — this decides how the
// crt stub must terminate the process: a module that has routed output
// through libc's buffered stdio needs libc's own exit() to flush those
// buffers, where a bare SYS_exit would silently drop them.
func linksLibC(m *vir.Module) bool {
	for _, l := range m.Links {
		if l.Kind == vir.LinkShared && l.Name == "c" {
			return true
		}
	}
	return false
}

// crtFormat translates vvm's own objFormat into crt's — crt builds a
// real relocatable object via objectfile/<format> to hand the linker, so
// it needs to know which container format it's targeting, same as
// toObjectBytes does. Flat targets never reach here (resolveEntryPoint
// is still called for them, but build.go rejects a non-nil stub against
// Flat afterward) — objFormat() already errors for anything it can't
// classify, so the only new case is Flat itself, called out explicitly.
func crtFormat(t Target) (crt.Format, error) {
	f, err := t.objFormat()
	if err != nil {
		return 0, err
	}
	switch f {
	case formatELF:
		return crt.FormatELF, nil
	case formatMachO:
		return crt.FormatMachO, nil
	case formatPE:
		return crt.FormatCOFF, nil
	default:
		return 0, fmt.Errorf("vvm: %s: no crt stub format for flat targets (entry synthesis needs a real linker)", t)
	}
}

// toCRTSignature converts vir's own MainSignature into crt's — crt
// doesn't import ir/vir at all (it sits below it, see crt/README.md), so
// it needs its own copy of the three recognized shapes.
func toCRTSignature(sig vir.MainSignature) crt.MainSignature {
	switch sig {
	case vir.MainSignatureBare:
		return crt.SignatureBare
	case vir.MainSignatureArgcArgv:
		return crt.SignatureArgcArgv
	case vir.MainSignatureArgcArgvEnvp:
		return crt.SignatureArgcArgvEnvp
	default:
		// Unreachable: callers only reach here after checking
		// sig != vir.MainSignatureNone above.
		return crt.SignatureBare
	}
}