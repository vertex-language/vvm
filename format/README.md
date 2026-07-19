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
│   └── text/         .vir   — lex.go, text.go, decl.go, func.go, operand.go,
│                                types.go, asm.go, asm_dialect.go,
│                                asm_intel.go, asm_att.go, asm_arm.go
└── asm/
    ├── x86/text/       encode.go — reads lower/x86.Program
    ├── x86_64/text/    encode.go — reads lower/x86_64.Program
    ├── arm/text/       encode.go — reads lower/arm.Program
    └── aarch64/text/   encode.go — reads lower/aarch64.Program
```

Two genuinely different shapes live under this tree, and the layout keeps them apart: `vbyte/` round-trips a `vir.Module`; `asm/` only ever prints, never parses.

**Two unrelated things are both called "asm" in this tree.** `vbyte/text`'s `asm.go`/`asm_*.go` files parse and print `vir.AsmBlock` — the inline-assembly *body-line* that can appear inside a `.vir` function, as data the caller is free to keep unlowered. `asm/<arch>/text` is a completely separate debug listing for an already-*lowered* `<arch>.Program`'s machine code. Neither imports the other; don't confuse an inline-asm dialect (`intel`/`att`/`a32`/`t32`/`native`, module-scoped, round-trips) with the disassembly listings under `asm/` (encode-only, one per architecture, not module-scoped).

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

This is why `text.Decode → binary.Encode → binary.Decode → text.Encode` is meant to land back on the same canonical `.vir` text it started from — both codecs traverse the module in the same field order and neither silently mutates it. This round-trip now also covers inline-asm body lines and the module-scoped `AsmDialect` field.

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

`Encode` assumes the module is already verified: tag bytes for each `Type`/`Operand`/`ConstInit`/`Terminator`/body-line variant, uvarint-prefixed strings and repetition counts, 8-byte floats, varint-encoded ints.

**Format version is 3.** History: v2 added inline-asm body lines (`BodyLine.Asm`, tagged `tagBodyInstruction`/`tagBodyAsm`) alongside the `ir/vir` Fn/Const/Inst-style rename. v3 moved `AsmDialect` from a per-asm-block field to a single module-scoped header field — read/written right after the target declaration, before structs — matching the current `module.go`/verifier shape (one dialect per module, not per block).

```go
if r.b() == 1 {
    d := vir.AsmDialect(r.str())
    m.AsmDialect = &d
}
```

A `BodyLine` decodes/encodes as one of two tagged variants — an ordinary `Instruction`, or a whole `AsmBlock` (bindings, then code lines, each either a mnemonic instruction or a bare label declaration). Nothing in the asm block's own encoding carries a dialect anymore; the block is interpreted using whatever `m.AsmDialect` says.

---

## `vbyte/text` — `.vir`, the human-readable form

```go
func Decode(src []byte) (*vir.Module, error)
func Encode(m *vir.Module) ([]byte, error)
```

`Decode` is a two-stage parser: `lexAll` splits source into one token stream per non-blank logical line (line breaks are significant; `//` starts a comment to end of line), then `parseModule` consumes those lines in the mandatory section order:

```
module → target? → asmdialect? → struct* → fnsig* → const* → global* → link* → extern* → fn*
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

Within a function body, labels (`ident:`), instructions, asm blocks, and terminators are recognized by shape; code appearing after a block's terminator is rejected on sight rather than left for `Verify` to catch. An `asm block after terminator` or an `instruction after terminator` are both parse-time errors from `func.go`, same as any other misplaced body line.

`Encode` is the canonical printer — fixed section order, one instruction per line at 4-space indent, `<op>.<suffix>` reassembled from `Inst.Op` + `Inst.Suffix`/`Inst.Sig`, byte strings re-quoted with the same escape set the lexer accepts (`\0 \n \r \t \\ \" \xHH`).

### Inline assembly (§4)

A function body line may be an `asm` block instead of an ordinary instruction:

```text
asm :
    in eax = value
    out ebx = result
    clobber ecx, edx
code:
    mov ebx, eax
    add ebx, 1
loop:
    dec ecx
    jnz loop
end
```

* `parseAsm` (in `asm.go`) reads the `in`/`out`/`clobber` binding lines, then a `code:` section of mnemonic lines and bare `label:` declarations, until `end`. This layer only enforces *shape* — legality of a given mnemonic/operand combination for the target dialect (§9.38) is not checked here.
* The dialect governing `code:` syntax is **module-wide**, not per-block: it comes from the module's `asmdialect` declaration (parsed by `parseAsmDialect` in `decl.go`) and is threaded into `parseAsm`/`parseAsmCodeLine` by the caller in `func.go`. A function that emits an `asm` block without the module having declared `asmdialect` is a parse error.
* Per-dialect operand grammar lives in one file per dialect, all implementing the same small `dialectSyntax` interface (`asm_dialect.go`):

  | Dialect | File | Registers | Immediates | Memory |
  |---|---|---|---|---|
  | `intel` | `asm_intel.go` | bare (`eax`) | bare literal (`ptr`-sized prefix + `[...]` recognized) | `[...]`, optional `byte/word/dword/qword/xmmword/ymmword/zmmword ptr` prefix |
  | `att` | `asm_att.go` | `%`-prefixed (`%eax`) | `$`-prefixed | `disp(base,index,scale)` |
  | `a32` / `t32` / `native` | `asm_arm.go` (shared `armSyntax`) | bare | `#`-prefixed | `[...]`, optional trailing `!` writeback |

  `code`/`in`/`out`/`clobber` bindings themselves are dialect-agnostic (`asm.go`); only mnemonic operand parsing/printing (`parseOperand`/`encodeOperand`) and the `%`-vs-bare register spelling (`regIdent`) vary by dialect.
* `readAsmMemory` (`asm_dialect.go`) is shared token/bracket-matching used by all dialects to capture a memory operand as raw text — `AsmOperand.Memory` is documented as verbatim dialect-specific addressing text, so no further structure is imposed on it.
* Encoding mirrors parsing: `encodeAsmBlock` prints `asm :` / bindings / `code:` / instruction-or-label lines / `end`, using the same per-dialect `encodeOperand`/`regIdent` to invert whatever `parseOperand` produced.

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

**Inline asm is data, not a lowering.** A `vir.AsmBlock` inside a `.vir`/`.vbyte` module is parsed/printed structurally by `vbyte/text` and `vbyte/binary` — mnemonic, operands, bindings — under whatever dialect the module declares. It is never lowered, never validated against a real instruction set here (§9.38 mnemonic/operand-shape legality is explicitly future work), and has nothing to do with the `asm/<arch>/text` listings, which describe the *output* of `lower/<arch>` instead.

**Dialect is module-scoped, not block-scoped.** A module declares at most one `asmdialect`; every asm block in every function is parsed and printed under that same dialect. This is enforced at three layers: the text parser threads the module's dialect into every `parseAsm` call, the binary format's v3 header carries a single `AsmDialect` field instead of per-block ones, and `vir.Verify` checks the declared dialect is legal for the module's target architecture.

**Neither codec re-validates.** `vbyte/binary` and `vbyte/text` decoders check framing/syntax and stop; their encoders assume `vir.Verify` already ran. Nothing in this package calls `Verify` on your behalf.

**Degrade, don't fail, in debug output.** The `asm/` decoders are deliberately lenient about unrecognized bytes — a listing with a few `db`/`.word` lines is still useful; refusing to print anything at all is not.

**Nothing here understands object-file layout or machine-code generation.** Verification lives in `ir/vir`. Instruction selection and encoding live in `lower/<arch>`. This package only converts what already exists into another shape — bytes into a `Module`, a `Module` into bytes, or a `Program` into a listing.