# format

`github.com/vertex-language/vvm/format`

Converts between `vir.Module` and a byte or text representation of it, plus one
debug-only listing format for already-lowered machine code. There is no top-level
`format` package to import — each sub-package is independent.

---

## Import paths

```go
import "github.com/vertex-language/vvm/format/vbyte/binary"     // .vbyte — round-trip, the frontend boundary
import "github.com/vertex-language/vvm/format/vbyte/text"       // .vir   — round-trip, human-readable
import "github.com/vertex-language/vvm/format/asm/x86/text"     // IA-32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/x86_64/text"  // x86-64 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/arm/text"     // A32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/aarch64/text" // A64 debug listing, encode-only
```

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

---

## Design notes

**Round-trip vs. one-way is the whole organizing idea.** `vbyte/` exists because
`.vbyte` and `.vir` are two serializations of the same `vir.Module`, and vvm accepts
either as input — both directions have to exist. `asm/` exists only to describe bytes
that already exist, for humans; adding a matching `Decode` would misrepresent what the
format is for, since `lower/<arch>` — not a hand-written listing — is the only
legitimate producer of a `Program`.

**Neither `vbyte` codec re-validates.** `vbyte/binary` and `vbyte/text` decoders check
framing/syntax and stop; their encoders assume `ir/verify.Verify` already ran. Nothing in
this package calls `Verify` on your behalf.

**Degrade, don't fail, in debug output.** The `asm/` decoders are deliberately lenient
about unrecognized bytes — a listing with a few `db`/`.word` lines is still useful;
refusing to print anything at all is not.

**Nothing here understands object-file layout or machine-code generation.**
Verification lives in `ir/verify`. Instruction selection and encoding live in
`lower/<arch>`. This package only converts what already exists into another shape —
bytes into a `Module`, a `Module` into bytes, or a `Program` into a listing.