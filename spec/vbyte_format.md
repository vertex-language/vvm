# Vertex IR — Binary Encoding Specification (vbyte)

**File Extension:** `.vbyte`
**Version:** Major `2`, Minor `2`

---

## 1. Core Guarantees & Constraints

* **Structural Equivalence:** Decoding an encoded module must produce an AST
  with identical section contents, declarations, types, and instructions.
* **Semantic Equivalence:** The encoded binary must strictly preserve all
  verifier outcomes of the text grammar. Anything the text verifier rejects,
  the decoder (or its verifier pass) must also reject.
* **Trivia Discarded:** Whitespace, comment placement, and identifier spellings
  beyond what the StringTable stores do not survive a round-trip.
* **Streaming Validation:** The layout guarantees one-pass, streaming
  decodability with no backpatching. This is why the StringTable precedes all
  other sections (§3.2) and why section order mirrors the text grammar's
  declare-before-use order (README §2.1).
* **Strict Rejection:** Decoders must reject non-canonical variable-length
  integers, booleans other than `0`/`1`, out-of-bounds indices, set reserved
  bits, unknown major versions, unknown section IDs, and trailing bytes after
  the final section or within any section body.

---

## 2. Primitive Encodings

| Type | Meaning | Binary Format |
| --- | --- | --- |
| `u8` | Raw byte | 1 byte. |
| `uleb` | Unsigned integer | Variable-length, 7 payload bits/byte, canonical form only. |
| `sleb` | Signed integer | Two's-complement variable-length, 7 payload bits/byte, canonical form only. |
| `f32` / `f64` | IEEE-754 float | 4 / 8 raw bytes, little-endian. NaN payloads pass through bit-exact, never re-encoded. |
| `bytes(n)` | Raw byte string | `uleb` length `n`, then `n` raw bytes. |
| `vec<T>` | Array | `uleb` count, then that many `T` elements. |
| `bool` | Flag | One `u8`; must be exactly `0` or `1`. |
| `idx` | Table index | `uleb` referencing one specific lookup table (§4). Which table is fixed by position in the grammar, never inferred. |

All multi-byte fixed-width values in this format are little-endian. There are
only two: `f32` and `f64`. Everything else is bytes or LEBs, so endianness
never otherwise arises.

---

## 3. Module Layout

```text
vbyte_module := magic version string_table? section*
magic        := 0x76 0x69 0x72 0x00              ; "vir\0"
version      := u8(major) u8(minor)
section      := u8(section_id) uleb(section_len) bytes-body
```

`section_len` is the exact byte length of the body. A body that ends early or
runs long relative to its declared length is a decode error — the length
exists for validation and coarse skipping by tooling, not for tolerating
unknown content (unknown IDs are still rejected, per §1).

### 3.1 Section IDs & Ordering

Sections `0x00`–`0x0A` must appear in strictly ascending ID order, each at
most once.

| ID | Section | Requirements | Body |
| --- | --- | --- | --- |
| `0x00` | Header | Required exactly once. | `idx(module_name)` |
| `0x01` | Namespace | Optional. | `idx(string)` |
| `0x02` | Target | Optional; required if Links (`0x07`) present. | `target_decl` |
| `0x03` | Structs | Optional. | `vec<struct_decl>` |
| `0x04` | FnSigs | Optional. | `vec<fnsig_decl>` |
| `0x05` | Consts | Optional. | `vec<const_decl>` |
| `0x06` | Globals | Optional. | `vec<global_decl>` |
| `0x07` | Links | Optional. | `vec<link_decl>` |
| `0x08` | Externs | Optional. | `vec<extern_group>` |
| `0x09` | Imports | Optional. | `vec<import_decl>` |
| `0x0A` | Functions | Optional. | `vec<fn_def>` |
| `0x0B` | StringTable | Required if any `idx` references a string. | `vec<bytes>` |

### 3.2 StringTable Placement (Carve-Out)

The StringTable keeps ID `0x0B` but is **exempt from the ascending-ID rule**:
when present, it must appear immediately after the version header, before
section `0x00`. Every other position is a decode error.

*Rationale:* one-pass decoding requires every name to exist before the first
`idx` that references it. Renumbering it to `0x00` would have broken every
existing ID assignment for a purely cosmetic gain; a single documented
carve-out is cheaper. This mirrors the text grammar's stance that `entry` /
`extern_c` are explicit overrides rather than silently resolved precedence
(README §2.2).

Empty strings are legal table entries. Duplicate byte strings in the table are
a decode error — the table is a deduplication, not a log.

---

## 4. Index Spaces

All names live in the StringTable as `vec<bytes>`. Structural references use
dense per-kind index spaces, assigned in declaration order:

| Namespace | Source | Usage |
| --- | --- | --- |
| `string_idx` | StringTable order | All name/string references. |
| `struct_idx` | Structs section order | Field types, `byval`/`sret`, `struct` types, `field.ptr`. |
| `fnsig_idx` | FnSigs section order | `call.<fnsig>`, `va_start.<fnsig>`, indirect `tailcall`. |
| `const_idx` | Consts section order | Validation only; const uses are inlined at each site. |
| `global_idx` | Globals section order | `addr` initializers, global operands. |
| `fn_idx` | Functions section order | Direct callees, function `addr` targets. |
| `extern_fn_idx` | Dense across all extern groups, in order | Extern callees. |
| `local_idx` | `fn_def.local_names` order | Values inside one function body. |
| `label_idx` | `fn_def.label_names` order | Block labels. Index `0` is the entry block and is unbranchable-to (README §4.3); a terminator targeting label `0` is a decode error. |

Because sections decode in ascending order and each index space is populated
before any later section can reference it, forward references are structurally
impossible — the binary format enforces declare-before-use by construction,
with the sole exception of direct self-recursion: inside function body `i`, a
callee `fn_idx` equal to `i` is legal, matching the text grammar's one
exemption (README §2.2).

---

## 5. Declarations

```text
target_decl  := idx(arch) idx(os)
                bool(has_abi)  idx(abi)?
                bool(has_tiers) vec<idx>(tiers)?

struct_decl  := bool(exported) idx(name) vec<field>
field        := idx(name) type

fnsig_decl   := bool(exported) idx(name) vec<type>(params) bool(variadic) type(ret)

const_decl   := bool(exported) idx(name) type literal

global_decl  := bool(exported) idx(name) bool(tls) type
                u8(align_log2_plus_1)          ; 0 = no align clause
                const_init

link_decl    := u8(lib_kind) idx(name)         ; 0=static, 1=shared, 2=framework

extern_group := idx(dependency) vec<extern_fn>
extern_fn    := idx(name) vec<extern_param> bool(variadic) type(ret) fn_attr_bits
extern_param := idx(name) type param_attr

param_attr   := u8(kind)                        ; 0=none, 1=byval, 2=sret
                idx(struct_idx)?                ; present iff kind != 0

import_decl  := idx(string)
```

* **Optionality is always flagged.** A `?` field in this grammar is present
  iff its governing `bool`/`u8` says so; nothing is inferred from remaining
  section length. (`bool(has_tiers)` is kept even though `vec` self-describes
  its count, because an empty tier list `[]` is ungrammatical in text form —
  absent and empty must stay distinguishable to preserve round-trips.
  `bool(has_tiers) = 1` with a zero-length vec is a decode error.)
* **Alignment bias:** `align_log2_plus_1 = 0` means no `align` clause;
  otherwise the alignment is `2^(value − 1)`. This removes the old collision
  between "absent" and `align 1`.
* **Attribute Mask:** `fn_attr_bits` is one `u8` with bit assignments
  `0=noreturn, 1=readonly, 2=inline, 3=noinline, 4=cold, 5=entry, 6=extern_c`.
  Bit 7 is reserved and **must be zero**; a set reserved bit is a decode
  error, not a warning. Bits 5 and 6 set together are a decode error
  (mutually exclusive per README §2.2), as is either without `exported`.

---

## 6. Type Encoding

```text
type := u8(type_tag) type_args?
```

| Tag | Type | Arguments |
| --- | --- | --- |
| `0x01` | `iN` | `uleb N` (N ≥ 1; `i0` rejected) |
| `0x02` | `f16` | None |
| `0x03` | `f32` | None |
| `0x04` | `f64` | None |
| `0x05` | `ptr` | None |
| `0x06` | `void` | None |
| `0x07` | `valist` | None. Rejected anywhere except an `alloca` suffix or a `va_start`/`va_arg`/`va_end` context — never a field, element, global, const, param, or return type, matching README §3/§6.2. |
| `0x08` | `vec[T,N]` | `type T`, `uleb N` |
| `0x09` | `struct` | `struct_type_ref` |
| `0x0A` | `array[T,N]` | `type T`, `uleb N` |

**Struct References:**

```text
struct_type_ref := bool(imported) payload
payload         := idx(struct_idx)                      ; imported = 0
                 | idx(import_path) idx(string name)    ; imported = 1
```

---

## 7. Literals & Global Initialization

One tag space, two legal ranges. Tags `0x01`–`0x08` are **literals** and may
appear both as instruction operands and inside initializers. Tags `0x0F`–`0x12`
are **init-only** and are a decode error in operand position; conversely, an
initializer may use any tag from either range.

```text
literal    := u8(tag ∈ 0x01..0x08) payload?
const_init := u8(tag ∈ 0x01..0x08 | 0x0F..0x12) payload?
```

| Tag | Variant | Payload |
| --- | --- | --- |
| `0x01` | Integer | `sleb` value. |
| `0x02` | Float | `f64` raw bytes (8, little-endian). |
| `0x03` | String | `idx` into StringTable. |
| `0x04` | Bool | `bool`. |
| `0x05` | `null` | None. |
| `0x06` | `NaN` | None. |
| `0x07` | `Inf` | None. |
| `0x08` | `-Inf` | None. |
| `0x0F` | `InitZero` | None. |
| `0x10` | `InitAddressOf` | `u8` (0=global, 1=fn) + `idx` (`global_idx` / `fn_idx`). A `tls` global as the *initialized* global with an `InitAddressOf` initializer is a decode error (README §6.2). |
| `0x11` | `InitAggregate` | `vec<const_init>`. |
| `0x12` | `InitByteString` | `bytes(n)` raw payload. Bypasses the StringTable — a byte-string initializer is data, not a name, so deduplicating it would be a size pessimization dressed as a feature. |

---

## 8. Functions & Bodies

```text
fn_def := bool(exported) idx(name)
          vec<param> bool(variadic) type(ret) fn_attr_bits
          vec<idx>(local_names)
          vec<idx>(label_names)      ; index 0 = entry block
          vec<block>

param  := idx(name) type param_attr  ; param_attr as in §5

block  := vec<body_line> terminator
```

`vec<block>` count must equal `label_names` count; block `i` carries label
`i`. A function must have at least one block (the entry).

### 8.1 Body Lines

```text
body_line := u8(kind) payload
kind      := 0 (inst) | 1 (loc)

loc_payload := idx(filename) uleb(line) bool(has_col) uleb(col)?
```

The kind byte exists because `inst` and `loc` were previously
indistinguishable at their first byte. One byte per line buys unambiguous
one-pass decoding; a cleverer packing was not worth the ambiguity.

### 8.2 Instructions

```text
inst_payload := bool(has_result) local_idx?
                u8(group) u8(ordinal)          ; opcode, see §8.3
                suffix
                vec<operand>
                u8(align_log2_plus_1)          ; 0 = no align clause

suffix := u8(kind) payload?
kind   := 0 (none) | 1 (type: `type` follows) | 2 (fnsig: `fnsig_idx` follows)
```

* The opcode is two `u8`s — group then ordinal — replacing the old `u16` whose
  byte order was never pinned down. Same information, no endianness question.
* `align_log2_plus_1` uses the §5 bias and is always physically present;
  a nonzero value on an opcode that takes no alignment is a decode error.
* **`va_start` Validation:** the `fnsig_idx` suffix of `va_start` must
  structurally match the enclosing `fn_def`'s params and return type, and the
  enclosing `fn_def` must be variadic (README §4.4). `has_result = 1` on a
  `void`-producing opcode, or `0` on a value-producing one, is a decode error.

### 8.3 Opcode Groups (`group`, then `ordinal` in listed order from 0)

| Group | Ordinal Sequence |
| --- | --- |
| `0x01` | Math: `add`, `sub`, `mul`, `neg`, `abs`, `sqrt`, `udiv`, `sdiv`, `urem`, `srem` |
| `0x02` | Overflow: `uaddo`, `saddo`, `usubo`, `ssubo`, `umulo`, `smulo`, `umulh`, `smulh`, `uadd_sat`, `sadd_sat`, `usub_sat`, `ssub_sat` |
| `0x03` | Bits: `and`, `or`, `xor`, `not`, `shl`, `lshr`, `ashr`, `rotl`, `rotr`, `ctlz`, `cttz`, `popcnt` |
| `0x04` | Min/Max/Intrinsics: `min`, `max`, `fma`, `copysign`, `floor`, `ceil`, `trunc_f`, `nearest`, `smin`, `smax`, `umin`, `umax`, `bswap`, `bitrev` |
| `0x05` | Compares: `eq`, `ne`, `slt`, `sgt`, `sle`, `sge`, `ult`, `ugt`, `ule`, `uge`, `lt`, `gt`, `le`, `ge` |
| `0x06` | Casts: `trunc`, `sext`, `zext`, `fdemote`, `fpromote`, `bitcast`, `sfromint`, `ufromint`, `stoint`, `utoint`, `stoint_sat`, `utoint_sat` |
| `0x07` | Memory: `alloca`, `load`, `store`, `load_vol`, `store_vol`, `memcopy`, `memmove`, `memset`, `field.ptr`, `index.ptr` |
| `0x08` | Atomics: `atomic_load`, `atomic_store`, `atomic_add`, `atomic_sub`, `atomic_and`, `atomic_or`, `atomic_xor`, `atomic_xchg`, `cmpxchg`, `fence` |
| `0x09` | Calls: `call`, `syscall` |
| `0x0A` | Varargs: `va_start`, `va_arg`, `va_end` |
| `0x0B` | Select: `select` |
| `0x0C` | Vectors: `splat`, `extract`, `insert`, `shuffle`, `masked_load`, `masked_store`, `gather`, `scatter` |
| `0x0D` | Reductions: `reduce_add`, `reduce_min`, `reduce_max`, `reduce_and`, `reduce_or`, `reduce_xor` |
| `0x0E` | Hints: `prefetch` |

Group `0x00` is permanently reserved (never a valid opcode), so a zeroed
opcode field can never silently decode as `add`.

### 8.4 Operands

```text
operand := u8(operand_tag) payload
```

| Tag | Kind | Payload |
| --- | --- | --- |
| `0x01` | Local | `local_idx` |
| `0x02` | Qualified | `idx`(module string), `idx`(member string) |
| `0x03` | Literal | `literal` (§7, tags `0x01`–`0x08` only) |
| `0x04` | Type | `type` (§6) |
| `0x05` | Ordering | `u8`: 0=relaxed, 1=acquire, 2=release, 3=acqrel, 4=seqcst. Values ≥ 5 rejected. |
| `0x06` | Vector Literal | `vec<sleb>`. Integer lanes only; float vector constants are built with `splat`/`insert`, matching the text grammar which has no float-vector literal form. |
| `0x07` | Struct Name | `idx(struct_idx)` — `field.ptr` operand 2. |
| `0x08` | Field Name | `idx(struct_idx)`, `uleb(field_ordinal)` — `field.ptr` operand 3. Ordinal must be in range for the struct. |
| `0x09` | Callable Ref | `u8` (0=fn, 1=extern_fn) + `idx`. |

### 8.5 Terminators

```text
terminator := u8(term_tag) payload
```

| Tag | Form | Payload |
| --- | --- | --- |
| `0x01` | `br` | `label_idx` |
| `0x02` | `br_if` | `operand`, `label_idx`(then), `label_idx`(else) |
| `0x03` | `switch` | `operand`, `label_idx`(default), `vec<(sleb case, label_idx)>`. Duplicate case values are a decode error. |
| `0x04` | `return` | `bool(has_operand)`, `operand?` |
| `0x05` | `tailcall` (direct) | `u8` (0=fn, 1=extern_fn) + `idx`, `vec<operand>` |
| `0x06` | `tailcall` (indirect) | `fnsig_idx`, `operand`(function pointer), `vec<operand>` |
| `0x07` | `trap` | None |
| `0x08` | `unreachable` | None |