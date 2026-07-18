# format

`github.com/vertex-language/vvm/format`

Converts between `vir.Module` and a byte or text representation of it, plus one debug-only listing format for already-lowered machine code. There is no top-level `format` package to import — each sub-package is fully independent, the same way `objectfile`'s formats are.

---

## Import paths

```go
import "github.com/vertex-language/vvm/format/vbyte/binary" // .vbyte — round-trip, the frontend boundary
import "github.com/vertex-language/vvm/format/vbyte/text"    // .vir   — round-trip, human-readable
import "github.com/vertex-language/vvm/format/asm/x86/text"     // IA-32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/x86_64/text"  // x86-64 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/arm/text"     // A32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/aarch64/text" // A64 debug listing, encode-only
```

---

## Package layout

```
format/
├── vbyte/
│   ├── binary/       .vbyte — decode.go, encode.go
│   └── text/         .vir   — decode.go, encode.go
└── asm/
    ├── x86/text/       encode.go — reads lower/x86.Program
    ├── x86_64/text/    encode.go — reads lower/x86_64.Program
    ├── arm/text/       encode.go — reads lower/arm.Program
    └── aarch64/text/   encode.go — reads lower/aarch64.Program
```

Two genuinely different shapes live under this tree, and the layout keeps them apart: `vbyte/` round-trips a `vir.Module`; `asm/` only ever prints, never parses.

---

## Design: two directions, never mixed

### `vbyte` — round-trip

Both `.vbyte` and `.vir` decode into an **unverified** `*vir.Module` and encode from an **assumed-verified** one. Neither package calls `vir.Verify` itself:

```go
m, err := text.Decode(src)   // structure/syntax checked; semantics not
if err != nil {
    return err
}
if err := vir.Verify(m); err != nil { // caller's job, always
    return err
}
b, err := binary.Encode(m)   // assumes m already passed Verify
```

This is why `text.Decode → binary.Encode → binary.Decode → text.Encode` is meant to land back on the same canonical `.vir` text it started from — both codecs traverse the module in the same field order and neither silently mutates it.

### `asm` — encode-only, never an input format

`asm/<arch>/text.Encode` takes a lowered `<arch>.Program` — bytes that already exist — and renders a disassembly listing for humans. There is no matching `Decode` anywhere in this tree:

```go
p, err := x86_64.Lower(m)
if err != nil {
    return err
}
listing, err := text.Encode(p) // format/asm/x86_64/text
os.Stdout.Write(listing)
```

---

## `vbyte/binary` — `.vbyte`, the frontend boundary

The one binary format vvm treats as input it may need to reload, so it's the only binary format with a decoder.

```go
func Decode(data []byte) (*vir.Module, error)
func Encode(m *vir.Module) ([]byte, error)
```

`Decode` checks **framing only** — magic bytes (`VBYT`), format version, varint bounds, string lengths. Internally the reader panics with an unexported `decodeErr` on any framing problem, and `Decode` recovers it into a normal `error`, so the body reads linearly without threading `error` through every call:

```go
data, _ := os.ReadFile("add.vbyte")
m, err := binary.Decode(data)
if err != nil {
    // framing problem — "vbyte: offset 14: string length 900 exceeds input"
}
```

`Encode` assumes the module is already verified: tag bytes for each `Type`/`Operand`/`ConstInit`/`Terminator` variant, uvarint-prefixed strings and repetition counts, 8-byte floats, varint-encoded ints.

---

## `vbyte/text` — `.vir`, the human-readable form

```go
func Decode(src []byte) (*vir.Module, error)
func Encode(m *vir.Module) ([]byte, error)
```

`Decode` is a two-stage parser: `lexAll` splits source into one token stream per non-blank logical line (line breaks are significant; `//` starts a comment to end of line), then `parseModule` consumes those lines in the mandatory section order:

```
module → target? → struct* → fnsig* → const* → global* → link* → extern* → fn*
```

Anything out of order, or interleaved, is rejected immediately — this is structural enforcement, distinct from anything `vir.Verify` checks later:

```go
src := []byte(`module add_example
target x86_64 linux gnu
global fmt array[i8, 14] = "%d + %d = %d\n\0"
extern :
    fn printf(f ptr, ...) i32
end
export fn main() i32:
    a = mov.i32 7
    return 0
end
`)
m, err := text.Decode(src)
```

Within a function body, labels (`ident:`), instructions, and terminators are recognized by shape; code appearing after a block's terminator is rejected on sight rather than left for `Verify` to catch.

`Encode` is the canonical printer — fixed section order, one instruction per line at 4-space indent, `<op>.<suffix>` reassembled from `Inst.Op` + `Inst.Suffix`/`Inst.Sig`, byte strings re-quoted with the same escape set the lexer accepts (`\0 \n \r \t \\ \" \xHH`).

---

## `asm/<arch>/text` — debug listings

Four packages, one per arch, all named `text` (import with an arch alias), all reading a lowered `Program` and producing an Intel/UAL-syntax listing:

```go
func Encode(p *x86.Program) ([]byte, error)      // format/asm/x86/text
func Encode(p *x86_64.Program) ([]byte, error)   // format/asm/x86_64/text
func Encode(p *arm.Program) ([]byte, error)      // format/asm/arm/text
func Encode(p *aarch64.Program) ([]byte, error)  // format/asm/aarch64/text
```

None of these is a general-purpose disassembler — each is a small decoder scoped to *exactly* the encoding subset its matching `lower/<arch>` package emits:

| Arch | Scope |
|---|---|
| `x86` | One-byte opcodes + `0F` map; ModRM/SIB forms the encoder produces (`[disp32]` absolute, ESP/`0x24` SIB escape); legacy prefixes `66`/`F0`/`F3` |
| `x86_64` | Same as x86, plus REX prefixes, `[RIP+disp32]`, SIB absolute/base-only forms |
| `arm` | Fixed 4-byte words in the `Program`'s own byte order; condition-coded data processing; `movw`/`movt` pairs; halfword/word/byte load-store; `ldrex`/`strex`; fixed misc encodings (`push`/`pop`, `bx`/`blx`, `dmb`, `clrex`, `udf`) |
| `aarch64` | Instruction words always little-endian (A64 code is never big-endian, even when `Program.Arch` is a `_be` variant used for data); move-wide, add/sub, logical, `madd`/`msub`, mul-high, DP-1/DP-2-source, bitfield-extend aliases, `csel`/`csinc`, scaled/unscaled load-store, `ldar`/`stlr`/`ldaxr`/`stlxr`, `stp`/`ldp` frame pairs, branches |

Fixup sites are read from `Program.Fixups` and annotated inline with symbol, kind, and addend instead of a raw displacement:

```go
p, _ := x86_64.Lower(m)
listing, _ := text.Encode(p)
```

```text
// vvm debug listing — x86-64 (lower/x86_64 subset), Intel syntax, not assemblable input

fn main: export  // size=41 align=16 fixups=2
  00000000  55                       push rbp
  00000001  48 89 e5                 mov rbp, rsp
  ...
  0000000a  48 8d 05 00 00 00 00     lea rax, fmt<pcrel32-4>
  ...
```

An unrecognized instruction word or opcode byte degrades to a raw `.word`/`db` line instead of failing the whole `Encode` call, so the listing stays usable even if `lower/<arch>` grows past what the printer currently recognizes. Global data prints alongside, with fixups rendered as `.long`/`.quad`/`.word` directives and long zero runs compressed to `.zero N`.

---

## Design notes

**Round-trip vs. one-way is the whole organizing idea.** `vbyte/` exists because `.vbyte` and `.vir` are two serializations of the same `vir.Module`, and vvm accepts either as input — both directions have to exist. `asm/` exists only to describe bytes that already exist, for humans; adding a matching `Decode` would misrepresent what the format is for, since `lower/<arch>` — not a hand-written listing — is the only legitimate producer of a `Program`.

**Neither codec re-validates.** `vbyte/binary` and `vbyte/text` decoders check framing/syntax and stop; their encoders assume `vir.Verify` already ran. Nothing in this package calls `Verify` on your behalf.

**Degrade, don't fail, in debug output.** The `asm/` decoders are deliberately lenient about unrecognized bytes — a listing with a few `db`/`.word` lines is still useful; refusing to print anything at all is not.

**Nothing here understands object-file layout or machine-code generation.** Verification lives in `ir/vir`. Instruction selection and encoding live in `lower/<arch>`. This package only converts what already exists into another shape — bytes into a `Module`, a `Module` into bytes, or a `Program` into a listing.