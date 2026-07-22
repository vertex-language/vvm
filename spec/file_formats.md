# Vertex IR — File Formats (v1.9)

Companion to `README.md` (the language specification). That document defines *what the IR means*; this one defines *how it is stored on disk*. Section references in the form §N.M point into the language spec unless prefixed with `F` (e.g. §F4.2), which points into this document.

Three artifacts exist:

| Extension | Kind | Role |
| --- | --- | --- |
| `.vir` | UTF-8 text | Authoring / human-readable module source |
| `.vbyte` | Binary | Compact, one-pass-readable module, semantically identical to `.vir` |
| `.vmeta` | Binary | Export-shape summary consumed by Stage 0/A/B (§7.3) |

*Note: `.vir` gets a short section because there is genuinely little to say — it is plain text and the grammar in §2.3 is normative. The binary formats carry the weight here.*

---

## F1. Design Principles

* **Text and binary are the same language.** `.vbyte` is a serialization of the §2.3 grammar, not a lowered or optimized form. No construct exists in one and not the other.
* **Streaming, one-pass, forward-reference-free.** The fixed section order of §2.1 is reproduced in the binary section table, and every index in the file points *backwards*. A reader that consumes bytes in order never has to seek or patch. This is the same declare-before-use discipline as the text form (§2.2), enforced structurally rather than by convention.
* **Compact over zero-copy.** Varints are used throughout, which rules out mmap-and-cast access. That is a deliberate trade: modules are compile-time artifacts read once per build, so size and hash stability matter more than random access. `.vmeta` in particular is read constantly during dependency resolution and wants to be small.
* **Fixed encoding endianness.** All multi-byte scalars in `.vbyte`/`.vmeta` are little-endian, *regardless of the module's `target`*. A module for `s390x` or `mips64` (big-endian silicon) is still stored little-endian. Encoding endianness describes the file; target endianness describes the machine the code runs on, and conflating them makes cross-compilation artifacts non-portable.
* **Byte-deterministic emission.** Identical input produces byte-identical output, always. No timestamps, no absolute paths, no producer version strings, no hash-map iteration order. This is what makes `.vmeta` content-addressable (§7.5).
* **Structure carries the semantics; text does not.** Comments and indentation are not preserved through `.vbyte`. `loc` lines are, because they are grammar-level (§2.3), but they live in a separate section that is excluded from the semantic hash so stripping debug info does not perturb caching.
* **Binary input is untrusted.** Reading a malformed, truncated, or hostile `.vbyte` must produce a named diagnostic. It must never crash the toolchain, allocate unboundedly, or bypass verification. Decoding is not verification — a successfully decoded `.vbyte` still runs the full `vir.Verify` pass.

---

## F2. Common Binary Conventions

These apply to both `.vbyte` and `.vmeta`.

### F2.1 Primitive Encodings

| Name | Encoding |
| --- | --- |
| `u8`, `u16`, `u32`, `u64` | Fixed-width, little-endian |
| `uleb` | Unsigned LEB128, minimal-length required (non-canonical padding is an error) |
| `sleb` | Signed LEB128, minimal-length required |
| `str` | `uleb` index into the string table; index `0` means *absent*, never *empty* |
| `type` | `uleb` index into the type table; index `0` means *absent* (used for untyped opcodes) |
| `bytes` | `uleb` length, then that many raw bytes |

Minimal-length LEB128 is mandatory. Permitting redundant continuation bytes would give the same module two byte encodings, which breaks determinism and content addressing for free — so it is rejected rather than tolerated.

### F2.2 Container Layout

```
+-------------------------------+
| header            (32 bytes)  |
| section table     (24 * N)    |
| section payloads  (8-byte aligned, zero padding)
| trailer           (40 bytes)  |
+-------------------------------+
```

**Header:**

| Offset | Size | Field |
| --- | --- | --- |
| 0 | 8 | magic (§F2.3) |
| 8 | 2 | `format_major` (u16) |
| 10 | 2 | `format_minor` (u16) |
| 12 | 2 | `ir_major` (u16) — language spec version, `1` for v1.9 |
| 14 | 2 | `ir_minor` (u16) — `9` for v1.9 |
| 16 | 4 | `flags` (u32) |
| 20 | 4 | `section_count` (u32) |
| 24 | 8 | reserved, must be zero |

**Section table entry (24 bytes each):**

| Offset | Size | Field |
| --- | --- | --- |
| 0 | 4 | `tag` — four ASCII bytes |
| 4 | 4 | `flags` (u32) |
| 8 | 8 | `offset` from file start, 8-byte aligned |
| 16 | 4 | `stored_length` |
| 20 | 4 | `uncompressed_length` (equals `stored_length` when uncompressed) |

Section flags:

| Bit | Name | Meaning |
| --- | --- | --- |
| 0 | `REQUIRED` | An unrecognized section with this bit set is a hard error; without it, the reader skips the section |
| 1 | `ZSTD` | Payload is zstd-compressed. Never legal in `.vmeta` (§F5.1) |
| 2 | `NONSEMANTIC` | Excluded from the semantic hash (§F2.4) |
| 3–31 | — | Reserved, must be zero |

**Trailer:**

| Offset | Size | Field |
| --- | --- | --- |
| 0 | 32 | `content_hash` — SHA-256 over bytes `[0, trailer_start)` |
| 32 | 4 | `hash_algo` (u32) — `1` = SHA-256 |
| 36 | 4 | reserved, must be zero |

Reserved fields being *must-be-zero-and-rejected-if-not* (rather than must-ignore) is the strict-semantics stance of §1 applied to the container: forward compatibility happens by adding sections, not by squatting on reserved bits. The trailing hash makes truncation detectable without a separate length field.

`hash_algo` exists so this can be revised without a format-major bump, but the default and only currently defined value is SHA-256 from the standard library `crypto/sha256` package. There's no meaningful speed case for a specialized hash here — these are small, compile-time artifacts hashed occasionally, not a hot path — so the stdlib primitive is preferred over adding a third-party dependency for marginal gains.

### F2.3 Magic Numbers

| File | Magic (hex) | ASCII |
| --- | --- | --- |
| `.vbyte` | `00 56 42 59 0D 0A 1A 0A` | `\0VBY\r\n\x1a\n` |
| `.vmeta` | `00 56 4D 54 0D 0A 1A 0A` | `\0VMT\r\n\x1a\n` |

The leading `\0` prevents the file being mistaken for text. The `\r\n` … `\n` pattern (borrowed from PNG) detects a file that has been through a CRLF-translating transfer — `\r\n` surviving as `\n`, or the lone `\n` becoming `\r\n`, both corrupt the magic loudly instead of producing a mysterious decode failure 40 KB later. The `\x1a` truncates display under legacy `type`/`cat` on DOS-lineage systems.

`.vir` has no magic; see §F3.

### F2.4 Hashes

Two digests, computed differently and used for different things, both SHA-256 (`crypto/sha256`, standard library).

**Content hash** (trailer): SHA-256 over the literal file bytes preceding the trailer. Integrity only. Changing a `loc` line changes it.

**Semantic hash**: SHA-256 over the following byte stream, built in canonical section order:

```
ir_major (u16 LE) || ir_minor (u16 LE)
for each section, in section-table order, with NONSEMANTIC clear:
    tag (4 bytes) || uncompressed_length (u32 LE) || uncompressed payload
```

Compression is invisible to the semantic hash by construction, so a `.vbyte` and its zstd-compressed twin have the same semantic identity. Debug sections are excluded, so stripping them is a no-op for build caching. In `.vbyte` the semantic hash is stored in the `HASH` section; in `.vmeta` it is not stored at all, because there the *content* hash already serves as identity (nothing non-semantic is present to exclude).

### F2.5 String Table (`STRT`)

```
count : uleb
entry[1 .. count] : bytes      // UTF-8, no NUL terminator
```

Index `0` is reserved and never stored. Strings are interned — no two entries may be byte-identical — and ordered by first reference in canonical emission order (§F4.6). Interning plus first-use ordering means the table is a pure function of the module, which determinism requires.

Strings hold identifiers, namespace/import strings, library names, `asm` mnemonics, `loc` filenames, and string literals.

### F2.6 Type Table (`TYPE`)

```
count : uleb
entry[1 .. count] :
    kind : u8
    payload per kind
```

| Kind | Name | Payload | Present in |
| --- | --- | --- | --- |
| `0x01` | `VOID` | — | both |
| `0x02` | `INT` | `uleb` bit width | both |
| `0x03` | `FLOAT` | `u8` ∈ {16, 32, 64} | both |
| `0x04` | `PTR` | — | both |
| `0x05` | `VALIST` | — | both |
| `0x06` | `VEC` | `type` element, `uleb` lanes | both |
| `0x07` | `ARRAY` | `type` element, `uleb` count | both |
| `0x08` | `STRUCT_N` | `uleb` struct-decl index (nominal) | `.vbyte` only |
| `0x09` | `STRUCT_S` | `uleb` field count, then that many `type` | `.vmeta` only |

Rules:

* Index `0` is reserved as *absent*.
* Every `type` reference inside an entry must be **strictly less** than that entry's own index. Types are therefore acyclic and readable in one forward pass — the binary mirror of §2.2's no-forward-references rule. It also means structural expansion always terminates, since a struct can never (even indirectly) contain itself by value.
* The table is **hash-consed**: no two entries may be structurally identical. Consequently *type equality is index equality* within a file, which is exactly the primitive §7.4 needs.
* `INT` widths are restricted to the §3 set {1, 8, 16, 32, 64, 128}. The §2.3 grammar admits `i[1-9][0-9]*` generally; the encoder rejects anything outside §3 rather than encoding a type the rest of the spec does not define. (Worth reconciling in the grammar itself.)
* `VALIST` may appear only as an `alloca` result type or a `va_start`/`va_arg`/`va_end` operand type. It is illegal as a `VEC`/`ARRAY` element, as a `STRUCT_N`/`STRUCT_S` field, and as a `const`/`global` type — the encoding-level restatement of §3, §6.1, and §6.2.

The nominal/structural split is the whole reason there are two struct kinds. Inside a module, `struct T` resolves by name and there is a declaration to point at. Across modules, §7.4 compares `byval[S]`/`sret[S]` **structurally, never by name**, so `.vmeta` cannot store a name-keyed reference without reintroducing the identity it explicitly disclaims.

### F2.7 Literals

```
literal :=
    0x01 INT     : sleb                       // also i1 / bool
    0x02 FLOAT   : u8 width, then raw IEEE-754 bit pattern, LE (2/4/8 bytes)
    0x03 STRING  : str
    0x04 NULL    : —                          // ptr
```

Floats are stored as bit patterns, not as decimal text, because §4.1 mandates strict IEEE-754-2019 with no reformatting latitude. `NaN` in text (which carries no payload in the §2.3 grammar) encodes as the canonical quiet NaN for the width; `Inf`/`-Inf` as the canonical infinities. `i128` literals occupy up to 19 SLEB128 bytes.

### F2.8 Limits

Exceeding any of these is a named encoder or decoder error, never silent truncation and never UB.

| Quantity | Limit |
| --- | --- |
| File size | 2 GiB |
| Section payload | 2 GiB |
| Section count | 64 |
| String table entries | 2²⁴ |
| Single string | 64 KiB |
| Type table entries | 2²⁴ |
| Functions per module | 2²⁴ |
| Blocks per function | 2²⁴ |
| Locals per function | 2²⁴ |
| Operands per instruction | 255 |

---

## F3. `.vir` — Text Format

`.vir` is plain text. The §2.3 grammar is normative and this section adds only encoding-level rules.

* **Encoding:** UTF-8. A byte-order mark is rejected, not stripped — accepting it would mean the first line is sometimes `module` and sometimes `\uFEFFmodule`.
* **Line endings:** LF (`0x0A`) only. A bare `CR` or `CRLF` is a lexical error. Since §2 has no separators and no continuations, the line is the unit of syntax, and tolerating two line-ending conventions means two byte encodings of the same module — the same determinism problem as non-canonical LEB128.
* **Final newline:** The file should end with LF. A missing final newline is accepted.
* **No magic.** Files are identified by extension. A `.vir` file always begins with `module` (after optional blank/comment lines), which serves as a de facto sniff signature.
* **Identifiers** are ASCII per §3. **Comments** (`//` to end of line, §3) may contain any valid UTF-8.
* **Indentation** is whitespace-insignificant (§2). Space and tab are both accepted; the canonical printer emits two spaces per nesting level.
* **String literals** contain no escape sequences — the §2.3 production is `"\"" [^"]* "\""`, so a literal can hold neither `"` nor a newline. Byte data in `global` initializers is therefore written as an aggregate list of `i8` literals rather than as an escaped string. *(This is a real limitation, not an oversight in this document; adding escapes requires a grammar change in §2.3.)*

### F3.1 Canonical Text Form

A conforming formatter emits: one instruction per line; two-space indent inside `fn` bodies and `asm` blocks, four inside `code:`; block labels at the enclosing `fn`'s indentation; a single space after each comma; no trailing whitespace; sections in §2.1 order with one blank line between top-level groups. Canonical form exists so that `text → binary → text` is a fixed point, which makes diffing a decompiled `.vbyte` against its source meaningful.

---

## F4. `.vbyte` — Binary Module

### F4.1 Sections

Section table entries **must** appear in this order. Deviation, duplication, or an unrecognized `REQUIRED` tag is a decode error. The order deliberately reproduces §2.1, so a streaming decoder and a streaming verifier can run in lockstep.

| # | Tag | Req | Flags | Contents |
| --- | --- | --- | --- | --- |
| 1 | `STRT` | ✓ | | String table (§F2.5) |
| 2 | `TYPE` | ✓ | | Type table (§F2.6) |
| 3 | `MODU` | ✓ | | `str` module name, `str` namespace (0 = absent) |
| 4 | `TARG` | | | Target triple + feature tiers |
| 5 | `ASMD` | | | `u8` dialect enum |
| 6 | `STRU` | | | `struct` declarations |
| 7 | `FSIG` | | | `fnsig` declarations |
| 8 | `CNST` | | | `const` declarations |
| 9 | `GLOB` | | | `global` declarations |
| 10 | `LINK` | | | `link` declarations |
| 11 | `EXTN` | | | `extern` groups |
| 12 | `IMPT` | | | `import` declarations |
| 13 | `ASMB` | | | Inline-asm block pool |
| 14 | `FUNC` | | | `fn` definitions |
| 15 | `LOCS` | | `NONSEMANTIC` | `loc` line table |
| 16 | `HASH` | | `NONSEMANTIC` | 32-byte semantic hash |

`STRT` and `TYPE` precede `MODU` because everything else references them; this is the one place the binary order differs from the text order, and it is a pure lookup-table hoist with no semantic content.

`TARG` is required if `LINK` or `ASMB` is present, and `ASMD` is required if `ASMB` is present — the encoding-level restatement of §2.1's conditional requirements.

### F4.2 Declaration Sections

```
TARG := str arch, str os, str abi (0 = absent), uleb tier_count, str tier[]
ASMD := u8   // 0 intel, 1 att, 2 a32, 3 t32, 4 native

STRU := uleb count, each:
          str name, u8 flags, uleb field_count, (str name, type)[]
FSIG := uleb count, each:
          str name, u8 flags, uleb param_count, type param[], type ret
CNST := uleb count, each:
          str name, u8 flags, type, literal
GLOB := uleb count, each:
          str name, u8 flags, type, uleb align (0 = unspecified), const_init
LINK := uleb count, each: u8 kind (0 static, 1 shared, 2 framework), str name
EXTN := uleb count, each:
          str lib, uleb fn_count, extern_fn[]
IMPT := uleb count, each: str import_string
```

Declaration flags (`u8`): bit 0 `export`, bit 1 `tls` (`GLOB` only), bit 2 `variadic` (`FSIG` only). Remaining bits reserved, must be zero.

`const_init` mirrors §2.3:

```
const_init :=
    0x00 ZERO
    0x01 LITERAL   : literal
    0x02 ADDR      : uleb decl reference
    0x03 AGGREGATE : uleb count, const_init[]
```

`ADDR` references resolve only to already-emitted `fn`/`global` declarations, which is how the encoding enforces §6.2's rule that `addr` cannot name an `extern fn` — an `extern` group is section 11 and `GLOB` is section 9, so the reference has nowhere to point.

Arch, os, abi, and tier names are stored as canonical §7.1 spellings only. Aliases resolve at the build-system boundary and never reach a file; an encoder that writes `amd64` is producing an invalid `.vbyte`, not a lenient one.

### F4.3 Functions (`FUNC`)

```
FUNC := uleb count, function[]

function :=
    uleb byte_length          // covers everything after this field
    str  name
    uleb flags
    uleb param_count
    param[]
    type ret
    uleb local_count
    local[]                   // (str name, type)
    uleb block_count
    block[]

param := str name, type, u8 attr, uleb attr_struct
         // attr: 0 none, 1 byval, 2 sret; attr_struct = STRU index, 0 if attr = 0

block := str label            // 0 for the entry block, which must be block 0
         uleb inst_count
         inst[]
         terminator
```

Function flags: bit 0 `export`, 1 `noreturn`, 2 `readonly`, 3 `inline`, 4 `noinline`, 5 `cold`, 6 `entry`, 7 `extern_c`, 8 `variadic`.

The `byte_length` prefix lets a reader skip bodies wholesale — which is precisely what Stage 0 extraction wants when deriving a `.vmeta` from an already-compiled `.vbyte`.

**The local table is the Join Convention made explicit.** §4.3 fixes a name's type at its first assignment; the binary hoists that into a per-function table so the fixation is a decoded fact rather than something the verifier must infer while walking blocks. Every result binding and every local operand is a `uleb` index into it. Parameters occupy local indices `0 .. param_count-1`, matching their status as entry-block assignments.

```
inst :=
    uleb opcode
    type result_type          // 0 for untyped opcodes
    uleb result_local         // 0 = no result, else local_index + 1
    uleb operand_count
    operand[]
    uleb align                // 0 = absent, else the byte alignment (a power of two)

operand :=
    0x00 LOCAL     : uleb local index
    0x01 DECL      : u8 kind, uleb index        // struct/fnsig/const/global/fn/extern
    0x02 IMPORT    : uleb IMPT index, str name  // the §2.3 qualified-ident form
    0x03 LITERAL   : literal
    0x04 TYPE      : type
    0x05 ORDERING  : u8   // 0 relaxed, 1 acquire, 2 release, 3 acqrel, 4 seqcst
    0x06 LABEL     : uleb block index
    0x07 ASM       : uleb ASMB index
```

Terminators use the same `inst` encoding, drawn from the reserved terminator opcode range (§F7.2). Every block ends with exactly one, per §4.3.

A `qualified-ident` is never a string lookup at decode time: it is an `IMPT` index plus a name, which is exactly the `(import-string, name, kind, operands)` tuple Stage A carries for unresolved `fn`/`global` references (§7.3).

### F4.4 Inline Assembly (`ASMB`)

```
ASMB := uleb count, asm_block[]

asm_block :=
    uleb binding_count
    binding[]                 // u8 kind (0 in, 1 out, 2 clobber), str reg, uleb local (0 for clobber)
    bytes code                // UTF-8, LF-separated, comments stripped, no trailing newline
```

Bindings are structured; the `code:` body is stored as text. This is a deliberate asymmetry. The bindings are the semantically load-bearing part — they are what §4.4 checks for bit-width match, declare-before-use, and `out`/`clobber` exclusivity, and they are what ties physical registers to IR values. The instruction lines themselves have a dialect-specific grammar (three distinct memory-operand syntaxes in §2.3 alone) that the backend assembler re-parses regardless, so encoding them structurally would buy a larger, more fragile format and no verification that isn't re-done downstream.

The cost is explicit: a `.vbyte` reader must re-run the §4.4 structural verifier over the stored text. Comments are stripped on encode so that two source files differing only in asm comments produce the same semantic hash.

### F4.5 Debug Lines (`LOCS`)

```
LOCS := uleb count, entry[]

entry := uleb d_func     // delta from previous entry's function index
         uleb d_block
         uleb d_inst
         str  file
         uleb line
         uleb col         // 0 = absent
```

Entries are sorted by `(func, block, inst)` and delta-encoded. The section carries `NONSEMANTIC`, so stripping it leaves the semantic hash untouched — a stripped and unstripped build of the same module remain interchangeable for caching.

### F4.6 Canonical Emission Order

For determinism, an encoder must:

1. Emit declarations in source order (which §2.1 already constrains).
2. Build `STRT` by first reference in that order — module name, namespace, then each declaration's strings, then function bodies in order.
3. Build `TYPE` by first reference in the same order, with each entry's operand types emitted before it.
4. Emit sections in §F4.1 order, each 8-byte aligned with zero padding.
5. Use no compression unless explicitly requested; when requested, apply zstd at default level with no dictionary.

---

## F5. `.vmeta` — Cross-Module Shape Artifact

`.vmeta` is the Stage 0 output of `format/vmeta` (§7.3): the externally visible shape of a module's exports, and nothing else.

### F5.1 What Is and Is Not Present

**Present:** producing module identity; the target record; the deep structural type closure; one entry per `export`-tagged declaration, carrying its shape and its emitted symbol name.

**Absent, by design:**

* Function bodies, `global` initializers, `const`… no — `const` *values* are present, because §7.4 compares them.
* Non-exported declarations of any kind.
* `link` lines, `asm` blocks, `loc` information.
* `inline` / `noinline` / `cold`. §7.4 requires exact matching on `noreturn` and `readonly` only, and names the reason: those are *caller-visible UB contracts* (§5.4 #7). The optimization hints are not, so admitting them into `.vmeta` would create build failures on changes that cannot affect a caller.
* Parameter *names*. Structural equality does not consider them.
* Struct *names* in any load-bearing position (§F5.3).
* Timestamps, paths, producer versions, or anything else that would make two builds of identical source differ.

`.vmeta` **must not** use section compression. Its content hash is its identity (§7.5), and a compressible-two-ways artifact would give one shape two addresses.

### F5.2 Sections

| # | Tag | Req | Flags | Contents |
| --- | --- | --- | --- | --- |
| 1 | `STRT` | ✓ | | String table |
| 2 | `TYPE` | ✓ | | Structural type closure (`STRUCT_S` only) |
| 3 | `MODU` | ✓ | | `str` module name, `str` namespace (0 = absent) |
| 4 | `TARG` | | | Target triple + tiers |
| 5 | `XPRT` | ✓ | | Export table |
| 6 | `IMPD` | | `NONSEMANTIC` | Direct import strings (build-graph hint) |

`XPRT` is required but may be empty. A module with no exports still emits a `.vmeta`, so the build graph stays uniform and `resolveImportGraph` never has to special-case a missing file against a module that simply exports nothing.

`IMPD` lists the producing module's own direct `import` strings. It is a scheduling convenience for the orchestrator — it can order compilation without parsing bodies — and carries `NONSEMANTIC` because the deep closure means shape checking never needs it. It is a hint, and a reader that ignores it is still correct.

`TARG` is recorded for compatibility checking, not for shape equality: structural equality (§7.4) compares types, not layouts, so it is target-independent, but linking modules built for different targets is a build error and the check needs the data.

### F5.3 Depth and Structural Encoding

§7.3 specifies that `.vmeta` is **deep**: Stage 0 needs a direct import's `.vmeta` when an exported declaration structurally embeds another module's type, but never a transitive chain. The encoding is what delivers that.

When an export's shape reaches a foreign type, that type is **expanded structurally and inlined** into this file's `TYPE` closure. No entry ever names another `.vmeta`. Consequently:

* Stage A (§7.3) needs exactly the direct imports' `.vmeta` files. There is no chain to walk and no chain to invalidate.
* Struct names never enter the comparison. `STRUCT_S` is the only struct kind available here, and it stores field types positionally. This is §7.4's "compared structurally, field types, order, count, **never by struct name**, since `S` has no shared cross-module identity" made unrepresentable-otherwise, rather than merely required.
* Struct names *are* still recorded, in the export table entry for an exported `struct`, because `import` resolves `module.Name` by name. The name is a lookup key. It is not part of the equality relation and not part of the shape hash.

Depth costs some redundancy — a widely embedded struct is expanded into every dependent's `.vmeta`. Given that a `.vmeta` is a few kilobytes and the alternative is transitive fan-out on every rebuild, the trade is worth taking.

### F5.4 Export Table (`XPRT`)

```
XPRT := uleb count, export[]

export :=
    u8   kind       // 0 fn, 1 global, 2 struct, 3 const, 4 fnsig
    str  name       // the unmangled source identifier
    str  symbol     // emitted symbol per §6.3; 0 for struct/const/fnsig
    32   shape_hash
    payload per kind

fn      := uleb flags, uleb param_count, param_shape[], type ret
           // flags: bit 0 variadic, 1 noreturn, 2 readonly, 3 entry, 4 extern_c
global  := type, u8 tls, uleb align
struct  := type            // a STRUCT_S entry
const   := type, literal
fnsig   := uleb flags, uleb param_count, type param[], type ret

param_shape := type, u8 attr, type attr_shape
               // attr: 0 none, 1 byval, 2 sret
               // attr_shape: the STRUCT_S expansion of S; 0 when attr = 0
```

Entries are sorted by `(kind, name)` with byte-wise name comparison. Sorting rather than preserving declaration order means reordering declarations in a source file — which is semantically inert for exported shape — does not change the artifact, and every downstream cache entry survives the edit.

The `symbol` field records the name §6.3 will actually emit: mangled Itanium-style if a `namespace` is declared, bare if not, bare regardless for `entry`/`extern_c`. All of those inputs are module-local, which is the point §7.5 makes — mangling is gated on `namespace` presence and never on the link graph, so `.vmeta` can state the final symbol at Stage 0 without knowing who imports it. Stage B rewrites unresolved references straight against this field.

`entry` and `extern_c` are recorded because they determine `symbol`, not because §7.4 compares them; they are excluded from `shape_hash`.

### F5.5 Shape Hashes

Each export carries a 32-byte SHA-256 digest over a canonical encoding of **exactly the fields §7.4 compares**, and nothing else:

| Kind | Hashed |
| --- | --- |
| `fn` / `fnsig` | param count; variadic flag; each param type, with `byval[S]`/`sret[S]` replaced by `S`'s full structural expansion; return type; `noreturn`; `readonly` |
| `global` | type; `tls` flag |
| `struct` | field types, in order, structurally expanded |
| `const` | type; literal value |

Excluded everywhere: the export's own name, parameter names, struct names at any depth, `align`, `inline`/`noinline`/`cold`, `entry`/`extern_c`, and the target record.

`global`'s `align` being excluded is worth stating outright, since it is stored two fields away from the hashed ones: §7.4 requires "identical type **and** identical `tls`-ness" for globals and stops there. Alignment is recorded for codegen and is not part of the contract.

Stage B compares an importer's assumption against the exporter's real compiled declaration by looking up `name`, comparing `shape_hash`, and — only on mismatch — walking the structures to produce the specific diagnostic §7.4 requires (`"signature mismatch: acme/net/http.get expects (ptr) i32, main.vir assumes (ptr, i32) i32"`). The hash makes the common case a 32-byte compare; the structures make the failure case explainable.

### F5.6 Content Addressing

A `.vmeta`'s trailer `content_hash` is its identity. Because emission is byte-deterministic and carries no timestamp, path, or producer version, two builds of identical source produce the same address — which is what §7.5 requires when it says `.vmeta` output must stay "reproducible and content-addressed regardless of the link graph."

The practical consequence: an exporter can change function bodies, private declarations, `inline` hints, or comments freely, and every dependent's Stage A result stays valid, because the `.vmeta` address does not move.

---

## F6. Equivalence and Round-Tripping

For any module `M`:

```
verify(parse_text(M.vir))  ≡  verify(decode(encode(parse_text(M.vir))))
```

Decoding and parsing produce the same in-memory module, so the verifier cannot distinguish them and every diagnostic in the language spec applies identically to both inputs.

**`text → binary → text`** is lossless with respect to semantics and lossy with respect to presentation. Preserved: everything in §2.3, including `loc` lines and `asm` code text. Not preserved: comments, indentation, blank lines, the specific spelling of numeric literals. Emitting canonical text (§F3.1) on the way back makes the round trip a fixed point, so repeating it changes nothing further.

**`binary → text → binary`** is byte-identical if and only if the original was canonically emitted (§F4.6) and uncompressed. A non-canonical `.vbyte` — one from a third-party encoder that ordered its string table differently — decodes correctly but re-encodes to canonical form.

**`.vmeta` is derivable from either.** Stage 0 extraction from `.vir` and from the corresponding `.vbyte` produce byte-identical `.vmeta`. If they did not, the content address would depend on which representation a build happened to have on hand.

---

## F7. Versioning and Compatibility

### F7.1 Version Fields

`format_major` / `format_minor` version the container and encoding; `ir_major` / `ir_minor` record the language spec version the module was written against.

| Change | Reader behavior |
| --- | --- |
| `format_major` differs | Reject. Named error, no attempt to decode. |
| `format_minor` newer than reader | Decode. Unknown sections without `REQUIRED` are skipped; with `REQUIRED`, reject. |
| `format_minor` older | Decode normally. |
| `ir_major` differs | Reject. |
| `ir_minor` differs | Decode; the verifier decides whether the module's constructs are acceptable. |

Additive changes — a new optional section, a new opcode, a new type kind — bump `format_minor`. Anything that changes the meaning of existing bytes bumps `format_major`. Reserved fields are must-be-zero precisely so they cannot be used as a third, informal channel.

### F7.2 Opcode Registry

Opcodes are `uleb`-encoded with a stable numbering. Ranges are reserved by category so a new opcode lands near its relatives without renumbering:

| Range | Category |
| --- | --- |
| `0x0000`–`0x00FF` | Terminators (`br`, `br_if`, `switch`, `return`, `tailcall`, `trap`, `unreachable`) |
| `0x0100`–`0x02FF` | Arithmetic, bitwise, shifts, comparisons, overflow/saturating/widening |
| `0x0300`–`0x03FF` | Conversions |
| `0x0400`–`0x04FF` | Memory: `alloca`, `load`/`store`, volatile, bulk ops, `field.ptr`/`index.ptr` |
| `0x0500`–`0x05FF` | Atomics and `fence` |
| `0x0600`–`0x06FF` | Calls, `syscall`, `va_start`/`va_arg`/`va_end`, `asm` |
| `0x0700`+ | Reserved |

A retired opcode number is never reused. The full name↔number table is a normative appendix to this document and is versioned with `format_minor`.

---

## F8. Trust Model and Error Taxonomy

### F8.1 Trust

| Artifact | Trust |
| --- | --- |
| `.vir` | Untrusted input. Parse errors are diagnostics. |
| `.vbyte` | Untrusted input. Decode errors are diagnostics. Successful decode grants nothing — the module still goes through `vir.Verify` in full. |
| `.vmeta` | Trusted *provisionally* at Stage A, exactly as §7.3 describes: real checking against the summary, without re-deriving it from the exporter's output. Confirmed at Stage B against the exporter's real compiled declaration. |

`.vmeta` is the only artifact granted any trust at all, and that trust is bounded in scope (shape only), in time (until Stage B), and in consequence (a mismatch is a named build error, never a silent link). Stage B is what converts Stage A's provisional trust into a checked fact — the same relationship §7.4 describes, restated here because it is the one place the file formats participate in a security-relevant decision.

`link`-derived `extern` groups remain exempt, on the same trust model as `extern "c": fn printf` (§7.3): there is no `.vmeta` for a C library and the declaration is taken at its word.

### F8.2 Named Errors

Every failure is a named, catalogued error. The categories:

| Prefix | Raised by | Examples |
| --- | --- | --- |
| `E-TEXT-` | Text lexer/parser | BOM present; CR in input; unterminated string; unknown section order |
| `E-BIN-` | Binary decoder | Bad magic; truncated file; content hash mismatch; non-canonical LEB128; nonzero reserved field; section out of order; duplicate section tag; unknown `REQUIRED` section; forward type reference; limit exceeded |
| `E-VER-` | `vir.Verify` | All language-spec verification failures (§2–§6) |
| `E-META-` | Stage 0 / Stage A | Malformed `.vmeta`; missing direct import; kind/arity/type mismatch against `.vmeta` |
| `E-LINK-` | Stage B | Structural equality failure (§7.4); target incompatibility; symbol collision |

`E-BIN-` errors never escalate into memory unsafety, unbounded allocation, or nontermination in the toolchain. Every length prefix is bounds-checked against the enclosing section before allocation, and every index is bounds-checked against its table at the point of use.

---

## F9. Appendix: Media Types and Identification

| Extension | Media type | Sniff |
| --- | --- | --- |
| `.vir` | `text/vnd.vertex-ir` (`charset=utf-8`) | Leading `module ` after optional comments/blank lines |
| `.vbyte` | `application/vnd.vertex-ir.module` | 8-byte magic `\0VBY\r\n\x1a\n` |
| `.vmeta` | `application/vnd.vertex-ir.meta` | 8-byte magic `\0VMT\r\n\x1a\n` |

Extension is authoritative for tooling; magic is authoritative for correctness. A file with a `.vbyte` extension and `\0VMT` magic is an `E-BIN-` error, not a `.vmeta`.

---

## F10. Open Points

Flagged rather than silently decided, since each needs a change in `README.md` to settle:

1. **String escapes.** §2.3's `string-literal` admits no escapes, so no string literal can contain `"` or a newline, and byte-string `global` initializers must be written as `i8` aggregate lists. If escapes are wanted, the grammar production is the place to add them.
2. **Integer widths.** §2.3 admits `i[1-9][0-9]*`; §3 enumerates {1, 8, 16, 32, 64, 128}. This document encodes only the §3 set. The grammar should be narrowed to match, or §3 broadened.
3. **`asm` code text.** §F4.4 stores `code:` bodies as text, requiring the binary reader to re-run the §4.4 structural verifier. A fully structured encoding is possible — §2.3 does specify the operand grammars — at meaningful cost in format size and per-dialect maintenance. The current choice assumes re-verification is cheap relative to that cost.