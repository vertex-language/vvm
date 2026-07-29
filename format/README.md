# format

`github.com/vertex-language/vvm/format`

Converts between an in-memory IR module — `vir.Module` (host) or `gvir.Module`
(device) — and a byte or text representation of it, plus one debug-only listing
format for already-lowered machine code. There is no top-level `format` package to
import — each sub-package is independent.

---

## Import paths

```go
import "github.com/vertex-language/vvm/format/vbyte/binary"     // .vbyte — round-trip, the frontend boundary
import "github.com/vertex-language/vvm/format/vbyte/text"       // .vir   — round-trip, human-readable
import "github.com/vertex-language/vvm/format/gvbyte/text"      // .gvir  — round-trip, human-readable, device-only
import "github.com/vertex-language/vvm/format/asm/x86/text"     // IA-32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/x86_64/text"  // x86-64 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/arm/text"     // A32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/aarch64/text" // A64 debug listing, encode-only
```

`vbyte/*` and `gvbyte/*` are two independent trees over two independent IRs.
`gvir` is the device-only sibling of `vir`, **not a subset of it** (see
`ir/gvir/README.md`), so nothing is shared between the two codecs — not the
lexer, not the grammar, not a common "module" abstraction. A change to one is
not a change to the other.

---

## Design: two directions, never mixed

### `vbyte` — round-trip, full module

Both `.vbyte` and `.vir` decode into an **unverified** `*vir.Module` and encode from an
**assumed-verified** one. Neither package calls `verify.Verify` itself:

```go
import (
    "github.com/vertex-language/vvm/format/vbyte/binary"
    "github.com/vertex-language/vvm/format/vbyte/text"
    "github.com/vertex-language/vvm/ir/verify"
)

m, err := text.Decode(src)   // structure/syntax checked; semantics not
if err != nil {
    return err
}
if err := verify.Verify(m); err != nil { // caller's job, always
    return err
}
b, err := binary.Encode(m)   // assumes m already passed Verify
if err != nil {
    return err
}
```

The intent is that `text.Decode → binary.Encode → binary.Decode → text.Encode` lands back
on the same canonical `.vir` text it started from — both codecs are meant to traverse the
module in the same field order and neither should silently mutate it. Both packages are
being rewritten from scratch, so none of the current internal file layout, container
format, or implementation notes are documented here yet — this file will fill back in as
that lands. Note that inline/native assembly is not part of the `vir.Module` data model
(see `asm.md`), so neither codec has anything asm-related to round-trip.

```go
func Decode(data []byte) (*vir.Module, error) // .vbyte
func Encode(m *vir.Module) ([]byte, error)    // .vbyte

func Decode(src []byte) (*vir.Module, error)  // .vir
func Encode(m *vir.Module) ([]byte, error)    // .vir
```

### `gvbyte` — round-trip, device modules

`gvbyte/text` is the `.gvir` codec: the same contract as `vbyte/text`, over
`gvir.Module` instead of `vir.Module`. It decodes into an **unverified** module and
encodes from an **assumed-verified** one, and like everything else here it calls no
verifier of its own:

```go
import (
    "github.com/vertex-language/vvm/format/gvbyte/text"
    "github.com/vertex-language/vvm/ir/verify"
)

m, err := text.Decode(src)  // structure/syntax checked; semantics not
if err != nil {
    return err
}
// Name binding, types, merge annotations, the §7.4 uniformity analysis and
// §4.3 capability gating are all ir/verify's — caller's job, always.
```

```go
func Decode(src []byte) (*gvir.Module, error) // .gvir
func Encode(m *gvir.Module) ([]byte, error)   // .gvir
```

**There is currently no `gvbyte/binary`.** The Vertex GPU IR specification defines
`.gvir` as a text format and names no byte encoding, so `text` is the whole tree
today. The directory is nested rather than flat so a binary sibling can land beside
it later without moving the text package or breaking its import path — not because
one is planned.

`Decode → Encode` is expected to land back on the same canonical `.gvir` text.
The round trip is single-format here, so it is a weaker check than the `.vbyte`/`.vir`
crossing: it catches a printer that drops a field, but there is no second
serialization to disagree with.

---

### `asm` — encode-only, never an input format

`asm/<arch>/text.Encode` takes a lowered `<arch>.Program` — bytes that already exist — and
renders a disassembly listing for humans. There is no matching `Decode` anywhere in this
tree:

```go
func Encode(p *x86.Program) ([]byte, error)      // format/asm/x86/text
func Encode(p *x86_64.Program) ([]byte, error)   // format/asm/x86_64/text
func Encode(p *arm.Program) ([]byte, error)      // format/asm/arm/text
func Encode(p *aarch64.Program) ([]byte, error)  // format/asm/aarch64/text
```

```go
p, err := x86_64.Lower(m)
if err != nil {
    return err
}
listing, err := text.Encode(p) // format/asm/x86_64/text
os.Stdout.Write(listing)
```

None of these is a general-purpose disassembler — each is scoped to exactly the encoding
subset its matching `lower/<arch>` package emits. An unrecognized instruction word or
opcode byte degrades to a raw `.word`/`db` line instead of failing the whole `Encode`
call, so the listing stays usable even if `lower/<arch>` grows past what the printer
currently recognizes.

This is a completely different "asm" from anything in `vbyte` — it's a listing of
already-lowered machine code for a target architecture, not a representation of inline
assembly inside a `.vir` module (which no longer exists as a concept here; see `asm.md`).

The `asm/` tree is host-side only. A `.gvir` module lowers to PTX, amdgcn and MSL
artifacts, which are the vendor toolchains' formats and are not produced or printed
by anything in this package.

---

## Design notes

**Round-trip vs. one-way is the whole organizing idea.** `vbyte/` exists because
`.vbyte` and `.vir` are two serializations of the same `vir.Module`, and vvm accepts
either as input — both directions have to exist. `gvbyte/` exists for the same reason
in the device IR: `.gvir` is an input format, so it needs a decoder, and a decoder
without a matching encoder cannot be tested by round-tripping. `asm/` exists only to
describe bytes that already exist, for humans; adding a matching `Decode` would
misrepresent what the format is for, since `lower/<arch>` — not a hand-written
listing — is the only legitimate producer of a `Program`.

**No codec re-validates.** `vbyte/binary`, `vbyte/text` and `gvbyte/text` decoders
check framing/syntax and stop; their encoders assume `ir/verify` already ran. Nothing
in this package calls a verifier on your behalf. What counts as "syntax" is drawn at
the same place in both text codecs: a spelling the grammar cannot produce is a decode
error, and everything a §-numbered rule decides is not.

**`.vir` and `.gvir` are lexically different languages.** The `.vir` lexer treats all
whitespace uniformly, because every `.vir` construct is self-delimiting. `.gvir` is
line-oriented with no continuations, and several of its constructs are not
self-delimiting without that — a `§9` execution builtin takes no operands, so nothing
but the line break separates `i = thread_in_grid.x` from the instruction below it.
`gvbyte/text` therefore lexes newlines as tokens. Do not port one lexer to the other.

**Spelling can be load-bearing.** `.gvir` makes `hex-float` exact by construction and
the portable way to pin a bit pattern, so which spelling the source used is part of
its meaning; `gvbyte/text` preserves it rather than re-deriving a printer's
preference. The same reasoning is why neither text codec normalizes anything it was
not asked to.

**Reject spelling, not meaning.** `gvbyte/text` accepts the arch alias spellings §3
forbids (`metal3.2`, `sm90`, `gfx11`) and hands them to `ir/verify` verbatim, so the
"write `metal32`" diagnostic survives instead of being buried under a syntax error.
Rejecting them in the parser would be locally correct and worse.

**Nothing here understands object-file layout or machine-code generation.**
Verification lives in `ir/verify`. Instruction selection and encoding live in
`lower/<arch>`. This package only converts what already exists into another shape —
bytes into a `Module`, a `Module` into bytes, or a `Program` into a listing.