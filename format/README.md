# format

`github.com/vertex-language/vvm/format`

Converts between `vir.Module` (or `vir.ModuleShape`) and a byte or text representation of it, plus one debug-only listing format for already-lowered machine code. There is no top-level `format` package to import — each sub-package is fully independent, the same way `objectfile`'s formats are.

---

## Import paths

```go
import "github.com/vertex-language/vvm/format/vbyte/binary"     // .vbyte  — round-trip, the frontend boundary
import "github.com/vertex-language/vvm/format/vbyte/text"       // .vir    — round-trip, human-readable
import "github.com/vertex-language/vvm/format/vmeta/binary"     // .vmeta  — Stage 0 module-shape container, lossy by design
import "github.com/vertex-language/vvm/format/asm/x86/text"     // IA-32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/x86_64/text"  // x86-64 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/arm/text"     // A32 debug listing, encode-only
import "github.com/vertex-language/vvm/format/asm/aarch64/text" // A64 debug listing, encode-only

```

---

## Package layout

```text
format/
├── vbyte/
│   ├── binary/       .vbyte — decode.go, encode.go, format.go
│   └── text/         .vir   — lex.go, text.go, decl.go, func.go, operand.go,
│                                types.go, asm.go, asm_dialect.go,
│                                asm_intel.go, asm_att.go, asm_a32.go
├── vmeta/
│   └── binary/        .vmeta — format.go, leb128.go, strtable.go, typetable.go,
│                                literal.go, shapehash.go, sections.go
└── asm/
    ├── x86/text/       encode.go — reads lower/x86.Program
    ├── x86_64/text/    encode.go — reads lower/x86_64.Program
    ├── arm/text/       encode.go — reads lower/arm.Program
    └── aarch64/text/   encode.go — reads lower/aarch64.Program

```

Three genuinely different shapes live under this tree, and the layout keeps them apart: `vbyte/` round-trips a full `vir.Module`; `vmeta/` round-trips only the smaller, structurally-lossy `vir.ModuleShape` used as a build-graph/interface artifact; `asm/` only ever prints, never parses.

**Two unrelated things are both called "asm" in this tree.** `vbyte/text`'s `asm.go`/`asm_*.go` files parse and print `vir.AsmBlock` — the inline-assembly *body-line* that can appear inside a `.vir` function, as data the caller is free to keep unlowered. `asm/<arch>/text` is a completely separate debug listing for an already-*lowered* `<arch>.Program`'s machine code. Neither imports the other; don't confuse an inline-asm dialect (`intel`/`att`/`a32`/`t32`/`native`, module-scoped, round-trips) with the disassembly listings under `asm/` (encode-only, one per architecture, not module-scoped).

---

## Design: three directions, never mixed

### `vbyte` — round-trip, full module

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

### `vmeta` — round-trip, but structurally lossy by design

`.vmeta` doesn't carry a `vir.Module` — it carries a `vir.ModuleShape` (Stage 0 extraction, §7.3), a smaller structural summary meant for build-graph and interface purposes rather than full recompilation. Neither direction calls `vir.Verify`; `vir.ExtractShape` is expected to run against an already-verified module first, matching `vbyte`'s "neither codec re-validates" stance:

```go
shape, err := vir.ExtractShape(m)   // m already verified
if err != nil {
    return err
}
b, err := binary.Encode(binary.Input{
    Shape:   shape,
    Target:  m.Target,
    Imports: m.Imports,
})
...
res, err := binary.Decode(b)   // res.Shape, res.Target, res.Imports

```

Unlike `vbyte`, this round-trip is lossy on purpose: `.vmeta`'s structural-only design (§F5.3 — struct names never enter the comparison) means decoded struct types only recover a name by matching against a caller's own exports, decoded struct field names and `byval`/`sret` parameter names don't survive at all, and `vir.GlobalShape` has no `Align` field to round-trip through the `align` slot §F5.4 reserves for it. These are format-mandated gaps, not bugs in this package.

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

The one binary format vvm treats as input it may need to reload, so it's the only binary format with a decoder. This package is a from-scratch rewrite against `file_formats.md` §F2–§F4's container format; the previous version was a flat tagged-varint stream with none of the container/table machinery described below.

```go
func Decode(data []byte) (*vir.Module, error)
func Encode(m *vir.Module) ([]byte, error)

```

`Decode` checks **framing only** — magic bytes, header/section-table shape, section ordering, and the trailer's content hash. Internally the reader panics with an unexported `decodeErr` on any framing problem, and `Decode` recovers it into a normal `error`, so the body reads linearly without threading `error` through every call.

**Container shape (§F2.2/§F2.3):**

```text
magic(8) header(24) section-table(24 × n) [pad] payloads [pad] trailer(40)

```

* Magic is `\0VBY\r\n\x1a\n`.
* Header (32 bytes total): magic, `format_major`/`format_minor` (currently 1.0), `ir_major`/`ir_minor` (currently 1.9), a reserved flags word, the section count, and 8 reserved bytes. `Decode` rejects a mismatched `format_major` or `ir_major` outright; `ir_minor` is decode-permissive (§F7.1).
* Each section-table entry is 24 bytes: a 4-byte tag, a `flags` word (`required` / `zstd` / `non_semantic` bits — this reader rejects `zstd`-flagged sections outright, since compression isn't implemented), a `uint64` offset, and stored/uncompressed lengths (equal here, since nothing is compressed). At most 64 sections; header+table and every payload are 8-byte aligned with zero padding.
* The trailer is 40 bytes: a SHA-256 of everything before it (checked on decode as a corruption/truncation guard), a `hash_algo` byte (2 = SHA-256), and 4 reserved bytes.

**Section order is fixed (§F4.1)** — `STRT TYPE MODU TARG ASMD STRU FSIG CNST GLOB LINK EXTN IMPT ASMB FUNC LOCS HASH`. Known sections, when present, must appear as a subsequence of that order with no duplicates; `STRT`/`TYPE`/`MODU` are the only ones actually required. `STRT` and `TYPE` are hash-consed string/type tables built while every other section is being built, so they're written first but resolved last on the encode side.

`LOCS` and `HASH` carry the `non_semantic` flag: `HASH` is a whole-file semantic-content hash (SHA-256 over `ir_major`/`ir_minor` plus each non-`non_semantic` section's tag+length+payload) used for content addressing, and `LOCS` carries `loc`-line debug info extracted out of `FUNC` bodies — neither participates in that hash, so `.vbyte` files differing only in debug info hash identically.

**Deviations/extensions where `file_formats.md` is silent or in tension with the actual `vir` data model** (see `format.go`'s header comment for the full rationale on each):

1. `STRUCT_N` (type-table kind `0x08`) gets a leading origin byte (`0` local / `1` imported) so cross-module struct references (`byval[S]`/`sret[S]`, `field.ptr`, §7.3/§7.4) aren't ambiguous.
2. The literal/value encoding adds `0x05 BOOL` and `0x06 VECTOR` tags beyond §F2.7's INT/FLOAT/STRING/NULL, since `operand.go`/`module.go` need both.
3. `const_init` gets a `0x04 BYTE_STRING` tag for `vir.InitByteString` (quoted-string global initializers), which §F4.2's grammar omits.
4. Inline-asm `code:` bodies are kept **structurally** encoded (mnemonic + typed operands) rather than re-parsed dialect text, contradicting §F4.4 but matching this package's own original no-relex design goal and `vir.AsmCodeLine`'s already-structured shape.
5. `LOCS` deltas use uleb for `d_func` (monotonic) but sleb for `d_block`/`d_inst`, since those indices reset to 0 at each new function/block.
6. `OpLoc` body lines are extracted out of `FUNC`'s instruction stream into `LOCS` at encode time (keyed by the real instruction they precede) and re-spliced back in at decode time — `file_formats.md` keys `LOCS` as if `loc` lines were never part of the instruction stream, but `vir`'s `BodyLine` has no separate slot for them.
7. A reserved pseudo-opcode (`0x06FE`, in the §F7.2 `0x0600`–`0x06FF` "asm" range) carries the single `ASM` operand referencing an `ASMB` pool entry — the natural place to hang the operand kind §F4.3 defines but never assigns to an instruction.
8. Multiple `clobber` bindings in one asm block coalesce into a single binding with a register list on decode, matching how `AsmBuilder.Clobber` actually builds them.
9. f16 conversion (`f16Bytes`/`f16ToFloat64`) is a simplified truncating conversion, not correctly-rounded float32→float16 (TODO: round-to-nearest-even).

`Encode` assumes the module is already verified.

---

## `vbyte/text` — `.vir`, the human-readable form

```go
func Decode(src []byte) (*vir.Module, error)
func Encode(m *vir.Module) ([]byte, error)

```

`Decode` is a two-stage parser: `lexAll` splits source into one token stream per non-blank logical line (line breaks are significant; `//` starts a comment to end of line), then `parseModule` consumes those lines in the mandatory section order:

```text
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
export fn main() i32 entry:
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
| --- | --- | --- | --- | --- |
| `intel` | `asm_intel.go` | bare (`eax`) | bare literal (`ptr`-sized prefix + `[...]` recognized) | `[...]`, optional `byte/word/dword/qword/xmmword/ymmword/zmmword ptr` prefix |
| `att` | `asm_att.go` | `%`-prefixed (`%eax`) | `$`-prefixed | `disp(base,index,scale)` |
| `a32` / `t32` / `native` | `asm_a32.go` (shared `armSyntax`) | bare | `#`-prefixed | `[...]`, optional trailing `!` writeback |

* `code`/`in`/`out`/`clobber` bindings themselves are dialect-agnostic (`asm.go`); only mnemonic operand parsing/printing (`parseOperand`/`encodeOperand`) and the `%`-vs-bare register spelling (`regIdent`) vary by dialect.
* `readAsmMemory` (`asm_dialect.go`) is shared token/bracket-matching used by all dialects to capture a memory operand as raw text — `AsmOperand.Memory` is documented as verbatim dialect-specific addressing text, so no further structure is imposed on it.
* Encoding mirrors parsing: `encodeAsmBlock` prints `asm :` / bindings / `code:` / instruction-or-label lines / `end`, using the same per-dialect `encodeOperand`/`regIdent` to invert whatever `parseOperand` produced.

---

## `vmeta/binary` — `.vmeta`, the Stage 0 shape

`github.com/vertex-language/vvm/format/vmeta/binary` converts between a `vir.ModuleShape` (`ir/vir`'s Stage 0 extraction, §7.3) and the on-disk `.vmeta` container (`file_formats.md` §F5). It mirrors `vbyte/binary`'s low-level container conventions (§F2) but carries a different, smaller section set, and never compresses (§F5.1).

```go
func Encode(in Input) ([]byte, error)
func Decode(data []byte) (*Result, error)

```

`Input`/`Result` bundle `*vir.ModuleShape` with the two things a `ModuleShape` doesn't itself carry: the producing module's `Target` (§F5.2, compatibility-only) and its direct `import` strings (§F5.2 `IMPD`, a `NONSEMANTIC` build-graph hint).

Neither direction calls `vir.Verify` — Stage 0 extraction (`vir.ExtractShape`) is expected to run against an already-verified module, matching `vbyte`'s "neither codec re-validates" stance.

**Known limitations.** `.vmeta`'s structural-only design (§F5.3: "struct names never enter the comparison") means round-tripping is lossy in ways the *format* mandates, not accidents of this code — see the `binary` package doc comment (`format.go`) for the itemized list: decoded struct types recover a name only by matching this file's own exports; decoded struct field names and `byval`/`sret` parameter names don't survive at all; `vir.GlobalShape` has no `Align` field to round-trip through the `align` slot §F5.4 reserves for it.

**Package layout:**

* **`format.go`** — container assembly: header, section table, trailer, `Encode`/`Decode` entry points, section ordering/duplication checks.
* **`leb128.go`** — `uleb`/`sleb` read/write (§F2.1), minimal-length enforced both ways.
* **`strtable.go`** — `STRT` string table: intern-on-first-reference build side, dedup-checked parse side (§F2.5).
* **`typetable.go`** — `TYPE` table: hash-consed, `STRUCT_S`-only (§F2.6, §F5.3) build side; raw-entry parse side plus the dependency-ordered materialization pass that resolves struct entries to `vir.StructType` once names are known.
* **`literal.go`** — `§F2.7` literal codec, extended with `0x05 BOOL` / `0x06 VECTOR` (mirroring `vbyte/binary`'s own documented deviation, since §6.2 scalar consts include `i1` and `vec[T,N]`).
* **`shapehash.go`** — `§F5.5` `shape_hash`: a canonical, table-index-free structural encoding, so two independently-built `.vmeta` files for equal shapes hash identically regardless of each file's own declaration order.
* **`sections.go`** — `MODU`/`TARG`/`IMPD`/`XPRT` payload codecs, including symbol mangling (via `vir.MangledSymbol`) and the `(kind, name)`-sorted export table (§F5.4).

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
| --- | --- |
| `x86` | One-byte opcodes + `0F` map; `nop`; full `FF`/group-5 form (`inc`/`dec`/`call`/`jmp`/`push`, register or memory r/m — not just register-indirect `call`/`jmp`); ModRM/SIB forms the encoder produces (`[disp32]` absolute, ESP/`0x24` SIB escape); legacy prefixes `66`/`F0`/`F3` |
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

**Round-trip vs. one-way is the whole organizing idea.** `vbyte/` exists because `.vbyte` and `.vir` are two serializations of the same `vir.Module`, and vvm accepts either as input — both directions have to exist. `vmeta/` also round-trips, but a smaller, structurally-lossy `vir.ModuleShape` rather than a full `Module` — it exists for build-graph/interface consumers that need to know a module's shape without needing to recompile it. `asm/` exists only to describe bytes that already exist, for humans; adding a matching `Decode` would misrepresent what the format is for, since `lower/<arch>` — not a hand-written listing — is the only legitimate producer of a `Program`.

**Inline asm is data, not a lowering.** A `vir.AsmBlock` inside a `.vir`/`.vbyte` module is parsed/printed structurally by `vbyte/text` and `vbyte/binary` — mnemonic, operands, bindings — under whatever dialect the module declares. It is never lowered, never validated against a real instruction set here (§9.38 mnemonic/operand-shape legality is explicitly future work), and has nothing to do with the `asm/<arch>/text` listings, which describe the *output* of `lower/<arch>` instead.

**Dialect is module-scoped, not block-scoped.** A module declares at most one `asmdialect`; every asm block in every function is parsed and printed under that same dialect. This is enforced at three layers: the text parser threads the module's dialect into every `parseAsm` call, the binary format's `ASMD` section carries a single dialect byte instead of per-block ones, and `vir.Verify` checks the declared dialect is legal for the module's target architecture.

**Neither codec re-validates.** `vbyte/binary`, `vbyte/text`, and `vmeta/binary` decoders check framing/syntax and stop; their encoders assume `vir.Verify` (or, for `vmeta`, an already-verified module's `vir.ExtractShape` output) already ran. Nothing in this package calls `Verify` on your behalf.

**`vmeta` trades completeness for stability.** Its lossiness — dropped field/parameter names, structural-only struct identity, no `Align` slot — isn't a bug to fix; it's what makes two independently-built `.vmeta` files for equivalent shapes hash identically (§F5.5) regardless of surface-level differences the full `vir.Module` would still distinguish.

**Degrade, don't fail, in debug output.** The `asm/` decoders are deliberately lenient about unrecognized bytes — a listing with a few `db`/`.word` lines is still useful; refusing to print anything at all is not.

**Nothing here understands object-file layout or machine-code generation.** Verification lives in `ir/vir`. Instruction selection and encoding live in `lower/<arch>`. This package only converts what already exists into another shape — bytes into a `Module` or `ModuleShape`, a `Module`/`ModuleShape` into bytes, or a `Program` into a listing.