package text

import (
	"fmt"
	"strings"

	"github.com/vertex-language/vvm/ir/vir"
)

// Decode parses .vir source into an unverified *vir.Module. Section order is
// enforced here structurally (§1.2), as is basic body shape (one terminator
// per block, nothing after it); everything else is Verify's job.
func Decode(src []byte) (*vir.Module, error) {
	p := &parser{}
	if err := p.lexAll(string(src)); err != nil {
		return nil, err
	}
	return p.parseModule()
}

// Encode prints a *vir.Module as .vir source in canonical section order.
// It assumes a verified module; it never re-checks (README invariant 3).
func Encode(m *vir.Module) ([]byte, error) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	encodeHeader(w, m)
	encodeStructsSection(w, m)
	encodeFnSigsSection(w, m)
	encodeConstsSection(w, m)
	encodeGlobalsSection(w, m)
	encodeLinksSection(w, m)
	encodeExternsSection(w, m)
	encodeFunctionsSection(w, m)

	return []byte(b.String()), nil
}