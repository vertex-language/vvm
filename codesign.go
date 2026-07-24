// codesign.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
	"github.com/vertex-language/vvm/linker/macho/codesign"
)

// signMachOExecutable re-signs a freshly linked Mach-O executable with a
// full ad-hoc signature via linker/macho/codesign, replacing the minimal
// inline signature linker/macho's own builder.go already embeds.
//
// builder.go's buildAdHocSig produces the same *shape* of signature the
// Darwin linker's automatic ad-hoc path does — but it's linker/macho's
// own from-scratch implementation, separate from (and never
// cross-verified against) the fuller codesign package this repo also
// ships. On Apple Silicon, exec() enforces a valid code signature
// unconditionally: a malformed CodeDirectory field (wrong page-hash
// count, wrong execSeg* value, a codeLimit that doesn't match what
// LC_CODE_SIGNATURE's dataoff actually patched) gets a bare SIGKILL with
// zero diagnostic output — indistinguishable, from vvm run's side, from
// a binary that ran fine and simply printed nothing. Routing every
// Mach-O executable through codesign.SignImage before returning it
// trades one inline signer for the one exercised, documented
// implementation of the format this repo maintains — cheap insurance
// against exactly that failure mode.
//
// A no-op for every non-Mach-O target, and for Mach-O shared libraries:
// builder.go only reserves LC_CODE_SIGNATURE space when isExec is true
// (see its buildLCs — appendCodeSig is in the isExec branch only,
// appendIDDylib otherwise), so a dylib has no reserved signature region
// to re-sign into without Force:true and a full __LINKEDIT rewrite.
// That's out of scope here; a dylib passes through with whatever
// builder.go already produced (unsigned, same as before this change).
func signMachOExecutable(m *vir.Module, t Target, out []byte) ([]byte, error) {
	f, err := t.objFormat()
	if err != nil {
		return nil, err
	}
	if f != formatMachO || t.Flat || t.Kind != OutputExecutable {
		return out, nil
	}

	signed, err := codesign.SignImage(out, codesign.Options{
		Identifier: m.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("vvm: codesign: %w", err)
	}
	return signed, nil
}