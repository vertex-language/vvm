# AMDTX — Language Specification

**Version 1.0 · Target audience: implementers of the parser, verifier, printer, and lowering pipeline**

---

## 0. Front Matter

### 0.1 Document conventions

**MUST**, **MUST NOT**, **SHALL**, **SHOULD**, and **MAY** carry RFC 2119 meanings. Rules labelled **V***n* are normative verifier obligations; the complete index is §16. Rules labelled **W***n* are warnings — a conforming verifier emits them but does not reject the module.

Sections marked *(informative)* are non-normative.

### 0.2 The PTX baseline rule

AMDTX takes NVIDIA PTX as its **structural** baseline: lexical conventions, statement termination, directive placement, parameterised register declarations, bracketed address syntax, and section ordering all follow PTX unless this document states otherwise. In PTX a statement is either a directive or an instruction, statements begin with an optional label and end with a semicolon, and a large block of virtual registers sharing a name prefix can be declared with a count suffix rather than one name at a time. AMDTX keeps all of that.

AMDTX departs from PTX only where amdgcn semantics make the PTX form meaningless or unsound. Every such departure is called out inline as a **PTX deviation** with a justification. Adding syntax that has no amdgcn-specific reason to exist is a specification defect.

### 0.3 Normative references

*   LLVM *User Guide for the AMDGPU Backend* (`AMDGPUUsage`) — address spaces, target properties, kernel descriptor, code object metadata, memory model.
*   LLVM *Syntax of AMDGPU Instruction Operands* (`AMDGPUOperandSyntax`) — register tuples, inline constants, literals.
*   AMD CDNA2 / CDNA3 / RDNA2 / RDNA3 ISA reference guides.
*   NVIDIA *Parallel Thread Execution ISA* — structural baseline only.

### 0.4 File extensions

| Extension | Content | Direction |
|---|---|---|
| `.amdtx` | Canonical AMDTX text | in / out |
| `.s` | Lowered physical amdgcn assembly | out only |

---

## 1. Scope and Design Principles

AMDTX (AMD Thread eXecution) is a virtual, target-shaped intermediate representation for AMD GPU compute kernels. It occupies the same niche PTX does for NVIDIA — a stable, human-readable, verifiable layer above the machine ISA — but it owns its own lowering pipeline, because amdgcn has no virtual-register form and no vendor-supplied finaliser.

**P1 — AMD only.** One `gfx` target per module. There is no NVIDIA path and no portable-VM abstraction. AMDTX is not a cross-vendor IR and will not accept one.

**P2 — Virtual registers, structured control flow.** Bodies operate on virtual registers, symbolic labels, and (preferably) structured regions. Physical register numbers, allocation decisions, and explicit EXEC-mask save/restore sequences are the lowering pipeline's business and MUST NOT appear in `.amdtx` text.

**P3 — Target-shaped, not target-agnostic.** Mnemonics map one-to-one onto amdgcn instruction families. The declared `.target` fixes wave width, register-file topology, matrix-core availability, waitcnt counter set, and flat-scratch model. A module is only meaningful against its declared target.

**P4 — Generation-neutral mnemonic spelling.** AMDTX mnemonics use the **GFX11-style bit-width naming** (`s_load_b128`, `global_load_b64`, `v_add_f32`) for *all* targets. Lowering rewrites to the legacy GFX9 spelling (`s_load_dwordx4`, `global_load_dwordx2`) where the target requires it. This is deliberate: encoding the dword count in the IR mnemonic while typing the operand separately is how width/type mismatches get in.

**P5 — Canonical text.** In-memory IR and `.amdtx` text are two encodings of the same object and MUST round-trip exactly (**V32**).

**P6 — Explicit synchronisation.** There is no implicit memory ordering between an asynchronous memory operation and its consumer. A dependency is expressed by an explicit `waitcnt`, by an ordered `fence`, or by running the `autowait` pass — never by adjacency.

**P7 — Strict legalisation.** Illegal instructions are verification errors. The verifier never silently rewrites. An optional pass may legalise before verification; the verifier itself may only accept or reject.

**P8 — Opaque escape hatches are typed.** `raw` and `rawbytes` pass text or bytes through untouched, but they MUST declare their clobbers, defs, and uses. An escape hatch that lies to the register allocator is worse than no escape hatch.

---

## 2. Lexical Structure

Case-sensitive. Keywords and mnemonics are lowercase.

| Token class | Form |
|---|---|
| Identifier | `[A-Za-z_][A-Za-z0-9_$.]*` |
| Register | `%` identifier |
| Directive | `.` identifier |
| Label definition | identifier `:` |
| Decimal integer | `[+-]?[0-9]+` |
| Hex integer | `0x[0-9a-fA-F]+` |
| Float | C99 hex-float or decimal-float literal |
| String | `"` … `"`, C escapes |
| Line comment | `//` to end of line |
| Block comment | `/*` … `*/`, not nested |

Whitespace, indentation, and column alignment are insignificant to the parser and significant only to the canonical printer (§15). Statements terminate with `;`. Blocks use `{ }`.

Comments are whitespace. A conforming implementation is not required to preserve comments across a round trip; **V32** is defined over the parsed module, not the byte stream.

---

## 3. Module Structure and Grammar

### 3.1 Section order

Single-pass processing requires strict ordering. A module consists of, in this order:

1.  **Preamble** — `.amdtx`, `.target`, `.wave`. Each exactly once, in this order *(**V1**)*.
2.  **File table** — zero or more `.file` declarations. All `.file` entries precede every `.loc` that references them *(**V2**)*.
3.  **Module-scope objects** — `.global` and `.shared` variable declarations (with optional static initializers). Array initializer lengths MUST match declared array sizes *(**V41**)*. An omitted length inside the brackets `[]` takes the length of the provided `init-list`.
4.  **Bodies** — `.kernel` and `.func` definitions, in any order.

### 3.2 Global rules

*   **Declare before use.** Files, module objects, and registers MUST be declared before first reference. Forward branches to labels are the sole exception.
*   **Flat symbol namespace.** Kernels, functions, and module objects share one module-wide namespace. Registers and labels are body-scoped. Body-scoped labels shadow module symbols during name resolution.
*   **Linkage.** `.func` symbols are always module-local in AMDTX 1.0. `.visible` and `.local` apply to `.kernel` definitions only, producing an externally dispatchable/linkable symbol (default) or an internal one. External `.func` linkage is deferred to a future revision that defines a call ABI.
*   **Kernels are not callable.** A `.kernel` is an AQL dispatch entry point only. It MUST NOT be the target of a `call` *(**V3**)*.
*   **Functions are inlined.** `.func` bodies are inlined at every call site by the lowering pipeline. AMDTX 1.0 defines no calling convention, no stack frame, and no `call` ABI. Recursive or indirect calls are rejected *(**V4**)*.

### 3.3 Grammar

```text
module            := preamble file-decl* object-decl* body-def*

preamble          := ".amdtx"  version-literal ";"
                     ".target" gfx-ident       ";"
                     ".wave"   ("32" | "64")   ";"

version-literal   := int-literal "." int-literal
gfx-ident         := "gfx" [0-9a-f]+

file-decl         := ".file" int-literal string-literal ";"

object-decl       := linkage? space-qual align-qual? ".b" int-literal ident
                     "[" int-literal? "]" ("=" "{" init-list "}")? ";"
space-qual        := ".global" | ".shared"
align-qual        := ".align" int-literal
linkage           := ".visible" | ".local"
init-list         := imm ("," imm)*

body-def          := kernel-def | func-def
kernel-def        := linkage? ".kernel" ident "(" param-list? ")" kernel-body
func-def          := ".func" ident "(" fparam-list? ")" func-body

param-list        := param ("," param)*
param             := ".param" param-kind? space? access? ".b" int-literal
                     align-qual? ident
param-kind        := ".buffer" | ".dynshared"
space             := ".global" | ".shared" | ".private" | ".constant"
                   | ".generic"
access            := ".read_only" | ".write_only" | ".read_write"

fparam-list       := fparam ("," fparam)*
fparam            := reg-class ident

kernel-body       := "{" launch-dir* reg-decl* stmt* "}"
func-body         := "{" reg-decl* stmt* "}"

launch-dir        := ".kernarg_size" int-literal ";"
                   | ".kernarg_align" int-literal ";"
                   | ".group_segment_size" int-literal ";"
                   | ".dynamic_group_segment" ";"
                   | ".private_segment_size" int-literal ";"
                   | ".reqd_workgroup_size" int-literal "," int-literal "," int-literal ";"
                   | ".max_flat_workgroup_size" int-literal ";"
                   | ".waves_per_eu" int-literal "," int-literal ";"
                   | ".kernarg_preload" int-literal ";"

reg-decl          := ".reg" reg-class reg-names ";"
reg-class         := ".sgpr" width | ".vgpr" width | ".agpr" width
                   | ".lanemask"
width             := ".b" int-literal          ; multiple of 32
reg-names         := "%" ident "<" int-literal ">"
                   | "%" ident ("," "%" ident)*

stmt              := inst ";" | label-def | if-stmt | loop-stmt | loc-line
label-def         := ident ":"
loc-line          := ".loc" int-literal int-literal int-literal? ";"

inst              := mnemonic operand-list? modifier* enc-clause?
mnemonic          := ident
operand-list      := operand ("," operand)*
modifier          := "." ident | "." ident "(" int-literal ")"
enc-clause        := ".enc" "(" enc-name ")"

if-stmt           := "if" uniformity? reg block ("else" block)?
loop-stmt         := "loop" "{" (stmt | break-stmt | continue-stmt)* "}"
break-stmt        := "breakif" uniformity? reg ";"
continue-stmt     := "continueif" uniformity? reg ";"
uniformity        := ".uniform" | ".divergent"
block             := "{" stmt* "}"

operand           := reg | imm | float-imm | addr | name-ref
                   | counter-list
reg               := "%" ident ("[" int-literal (":" int-literal)? "]")?
                   | "%" special-name
special-name      := "exec" | "vcc" | "scc" | "m0" | "flat_scratch"
                   | "kernarg_ptr" | "dispatch_ptr" | "null"
                   | "wgid" "." ("x"|"y"|"z")
                   | "tid"  "." ("x"|"y"|"z")
imm               := int-literal | hex-literal
addr              := "[" reg ("+" | "-") int-literal "]" | "[" reg "]"
name-ref          := ident
counter-list      := counter ("," counter)*
counter           := ("vmcnt"|"lgkmcnt"|"vscnt") "(" int-literal ")"

enc-name          := "auto" | "SOP1" | "SOP2" | "SOPK" | "SOPP" | "SOPC"
                   | "SMEM" | "VOP1" | "VOP2" | "VOP3" | "VOP3P" | "VOPC"
                   | "VOPD" | "DS"   | "FLAT" | "GLOBAL" | "SCRATCH"
                   | "MUBUF"

```

---

## 4. Targets

`.target` selects exactly one processor. Everything below follows from it.

| `.target` | Family | Silicon | Default wave | `.wave 32` | AGPRs | Matrix | `vscnt` | Flat scratch | Packed tid | `EF_AMDGPU_MACH` |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `gfx900` | GFX9 / GCN5 | Vega | 64 | ✗ | ✗ | ✗ | ✗ | absolute | ✗ | `0x02c` |
| `gfx90a` | GFX9 / CDNA2 | MI200 | 64 | ✗ | unified | MFMA | ✗ | absolute | ✓ | `0x03f` |
| `gfx942` | GFX9 / CDNA3 | MI300 | 64 | ✗ | unified | MFMA | ✗ | architected | ✓ | `0x04c` |
| `gfx1030` | GFX10.3 / RDNA2 | Navi 21 | 32 | ✓ | ✗ | ✗ | ✓ | absolute | ✗ | `0x036` |
| `gfx1100` | GFX11 / RDNA3 | Navi 31 | 32 | ✓ | ✗ | WMMA | ✓ | architected | ✓ | `0x041` |

The `gfx942` machine code `0x4c` is taken from LLVM's `ELF.h`. Flat-scratch model and packed work-item IDs are the "Target Properties" recorded per processor in the AMDGPU backend guide; `gfx90a` and `gfx942` additionally support `tgsplit`, `sramecc`, `xnack`, and kernarg preload, while `gfx1030` and `gfx1100` support `cumode` and `wavefrontsize64`.

### 4.1 Wave width

`.wave 32` is legal **only** on GFX10 and later. `.wave 64` is legal on **every** target — RDNA parts support it through the `wavefrontsize64` feature *(**V5**)*.

Wave width is resolved before any width-dependent rule is checked, because it determines:

* the physical width of `.lanemask` (§7.4).
* the width of `%exec` and `%vcc` (on GFX10+ in wave32 mode only the low 32 bits are architecturally used).
* the legal lane-index range for cross-lane operations (**V22**).

### 4.2 Target extension *(informative)*

Adding a target requires: a row in the table above, a waitcnt counter profile (§12.2), an inline-constant profile (§8.1), a per-target encoding table (§9.4), and the `EF_AMDGPU_MACH` value. Vector encoding tables MUST be per-target; a single GFX11 table applied to all targets is a conformance defect. Adding a generation is a spec revision, not a lowering-pipeline concern.

---

## 5. Types

AMDTX carries **bit widths**, not semantic types, on registers. Interpretation lives in the mnemonic — `v_add_f32` and `v_add_u32` both take `.vgpr.b32` operands.

| Form | Meaning |
| --- | --- |
| `.b`*N* | *N*-bit opaque bit pattern; *N* MUST be a multiple of 32 |
| `.lanemask` | one bit per lane; physical width = `.wave` |

Bit widths appear in register declarations, `.param` declarations, and `.func` formal parameters; `.lanemask` is admissible only in the first and third. They never appear as a standalone instruction suffix.

Legal widths: `.b32`, `.b64`, `.b96`, `.b128`, `.b160`, `.b192`, `.b224`, `.b256`, `.b288`… up to `.b384` (12 dwords), plus `.b512` (16 dwords) and `.b1024` (32 dwords). These follow the AMDGPU assembler's supported register-tuple sizes: 1 through 12 registers, or exactly 16 or 32 *(**V13**)*.

---

## 6. Address Spaces

| AMDTX space | HSA segment | Hardware | Pointer width | Notes |
| --- | --- | --- | --- | --- |
| `.generic` | flat | flat | 64 | aperture-based dispatch to global/private/shared |
| `.global` | global | global | 64 |  |
| `.constant` | constant | global | 64 | scalar-loadable; host-immutable during dispatch |
| `.shared` | group | LDS | 32 | workgroup-scoped; un-sized arrays depend on `.dynamic_group_segment` |
| `.private` | private | scratch | 32 | lane-scoped, dword-interleaved |

Only 64-bit process address spaces are supported for the amdgcn target.

**PTX deviation:** AMDTX omits the `.address_size` directive entirely. Because the `amdgcn` target strictly uses a 64-bit process address space, a directive that can only ever accept `64` carries zero information. Adding syntax that has no amdgcn-specific reason to exist is a specification defect.

---

## 7. Registers

### 7.1 Declaration

```text
.reg .sgpr.b32   %s<16>;          // %s0 … %s15
.reg .sgpr.b64   %kbase, %kend;
.reg .vgpr.b32   %v<64>;
.reg .vgpr.b128  %tile<4>;
.reg .agpr.b512  %acc;
.reg .lanemask   %p<4>;

```

Numbering inside a `%name<N>` block is **0-based and dense**: `%name0` through `%name(N-1)`. `N` MUST be ≥ 1. Every operand register MUST have a matching `.reg` declaration *(**V16**)*.

### 7.2 Register files

| Class | File | Constraint |
| --- | --- | --- |
| `.sgpr.b`*N* | SGPR | uniform across the wave |
| `.vgpr.b`*N* | VGPR | one value per lane |
| `.agpr.b`*N* | AGPR | matrix accumulator; requires `has_agprs` |
| `.lanemask` | SGPR | width from `.wave` |

There are 256 architected accumulator registers. All tuple alignment constraints (SGPR pair/quad alignment universally; VGPR and AGPR pair even-alignment on `gfx90a` and `gfx942`) are allocator obligations, not verifier obligations, and are listed here so implementers do not omit them from the allocator. AMDTX does not expose physical numbers, so these are not verified; however, `.agpr` on a target without AGPRs is a verification error (**V14**).

On CDNA2 and CDNA3 the VGPR and AGPR files are **one budget, not two**. The file holds 512 entries per lane, split between regular VGPRs (≤256 per wave) and accumulation VGPRs (≤256 per wave), with both drawing on the same 512-entry allocation. The lowering pipeline MUST emit `.amdhsa_accum_offset` to record the split — it marks where AGPRs begin after the VGPRs and is rounded up to a multiple of 4. Occupancy analysis MUST count VGPR + AGPR against 512, never against 256 each.

### 7.3 Special registers

`%exec`, `%vcc`, `%scc`, `%m0`, `%flat_scratch`, `%kernarg_ptr`, `%dispatch_ptr`, `%null`, `%wgid.{x,y,z}`, `%tid.{x,y,z}` are referenceable without declaration.

* **Reads** of `%exec` and `%vcc` are permitted (ballot, readfirstlane, mask arithmetic).
* **Direct writes** to `%exec` are prohibited. Divergence is expressed structurally, or — if you truly need the raw sequence — through `raw` with an explicit `%exec` clobber *(**V15**)*.
* `%tid.{x,y,z}`: on targets with **packed work-item IDs** (`gfx90a`, `gfx942`, `gfx1100`) the three IDs arrive packed into a single VGPR, 10 bits each. AMDTX presents them as three independent values; lowering emits the unpack. Code MUST NOT assume three separate initial VGPRs.

### 7.4 `.lanemask` and the wave-width problem

A per-lane boolean on amdgcn is a wave-wide bit mask living in an SGPR (wave32) or SGPR pair (wave64). Writing `.sgpr.b64` for a predicate hard-codes wave64 into the IR and breaks the moment the module is retargeted.

`.lanemask` is the fix. It lowers to `.sgpr.b32` under `.wave 32` and `.sgpr.b64` under `.wave 64`. Vector comparisons write `.lanemask`; `if`/`breakif` on a `.lanemask` operand is divergent by definition.

### 7.5 Sub-Register Slicing

To interact with wide register tuples while adhering to strict memory widths, AMDTX implements a sub-register slice syntax. Users MAY slice wide registers down to smaller sizes for arithmetic operations:

```text
s_load_b128 %sd0, [%kernarg_ptr+0];
v_add_f32   %v0, %v1, %sd0[1];       // Extracts the 2nd dword of %sd0
v_add_f64   %vd0, %vd1, %sd0[2:3];   // Extracts the 3rd and 4th dwords

```

**PTX/LLVM deviation:** AMDTX borrows the bracket notation from LLVM but redefines the index domain as tuple-relative dwords, rather than absolute physical register numbers. This allows interacting with wide register tuples while adhering to strict memory widths without exposing physical allocation.

* **V40** — A sub-register slice `[N]` or `[N:M]` MUST NOT exceed the bounds of the original register declaration.

---

## 8. Operands

### 8.1 Immediates

Two forms, distinguished by whether the value fits an inline constant slot:

**Inline constants** — integers `-16` through `64`, and the standard inline floats (`0.5`, `-0.5`, `1.0`, `-1.0`, `2.0`, `-2.0`, `4.0`, `-4.0`, and `1/(2π)` on GFX9+). Free; consume no instruction-stream space. Printed in decimal (integers) or canonical float form *(**V17**)*.

Negative inline constants are legal and MUST be accepted. Rejecting them, or printing them as unsigned, is a defect.

**Literal dwords** — any other 32-bit value, encoded as a separate dword in the instruction stream. Printed in hex.

An instruction may use only one literal, though several of its operands may refer to that same literal *(**V18**)*.

VOP3 and VOP3P encodings do not permit 64-bit literal operands, and on pre-GFX10 targets, they do not permit literals at all. Violations pinned to `.enc(VOP3)` or `.enc(VOP3P)` are verification errors *(**V19**)*.

### 8.2 Addresses

```text
[%base]           // no displacement
[%base+64]        // positive displacement
[%base-8]         // negative displacement

```

Displacements are signed and printed in **signed decimal**. Printing a negative displacement as unsigned hex is a defect. Legal displacement ranges are per-encoding (SMEM, FLAT, GLOBAL, SCRATCH, DS, MUBUF each differ) and checked at lowering, not at parse time *(**V20**)*.

### 8.3 Modifiers

Instruction modifiers follow the mnemonic and operand list, space-separated, PTX-style dotted:

```text
v_add_f32 %v3, %v1, %v2 .clamp .mul(2);
global_load_b32 %v5, [%vd1+16] .glc .slc;

```

Modifier legality is per-instruction and per-target. Cache-policy modifiers (`glc`/`slc`/`dlc` on GFX9–GFX11) are target-specific. Code that needs portable ordering SHOULD use `fence` (§12.4) and let lowering select cache policy.

---

## 9. Instructions

### 9.1 Register-class legality

* **V6** — Scalar (`s_*`) instructions MUST write SGPR destinations, `%scc`, or `%m0`. VGPR/AGPR destinations are illegal.
* **V7** — Vector (`v_*`) instructions MUST write VGPR or AGPR destinations, `%vcc`, or a `.lanemask` (for comparisons). SGPR destinations are illegal except for the designated cross-lane readback forms (`v_readfirstlane_b32`, `v_readlane_b32`).
* **V8** — Scalar memory (`s_load_*`, `s_store_*`) MUST address `.constant` or `.global` space with an SGPR base. A VGPR base is illegal. Vector memory MUST use the address-register class its encoding requires (VGPR for FLAT/GLOBAL/SCRATCH/DS; VGPR index plus SGPR descriptor for MUBUF).
* **V9** — Load and store data width MUST equal the declared width of the data register. `s_load_b128` into a `.b32` register is an error, not a truncation. Sub-register slices that equate to the correct load/store memory width are fully supported.

### 9.2 Target-gated instructions

* **V10** — MFMA (`v_mfma_*`) requires a CDNA target.
* **V11** — WMMA (`v_wmma_*`) requires GFX11 or later.
* **V12** — `waitcnt_vscnt` requires GFX10 or GFX11 (see §12.2).
* **V21** — `s_barrier` MUST NOT appear inside divergent control flow. All waves of the workgroup must reach it.
* **V22** — Cross-lane operations (`v_readlane_b32`, `v_writelane_b32`, `ds_bpermute_b32`, `v_permlane*`, DPP row/bank controls) MUST use lane indices below the active wave width, and MUST respect their encoding's row/bank granularity.

### 9.3 Termination

* **V23** — Every reachable exit path of a `.kernel` body terminates with `s_endpgm`.
* **V24** — Every reachable exit path of a `.func` body terminates with `ret`.

### 9.4 Pinned encodings

`.enc(<name>)` asserts a physical encoding. It is checked **after** instruction selection, against the per-target encoding table. If the selected encoding differs, or the chosen encoding cannot represent the operands (too many literals, out-of-range operand slot, SGPR in a slot that forbids it), verification fails *(**V25**)*.

`.enc(auto)` is the default and is never printed.

---

## 10. Control Flow

A body uses **either** structured regions **or** explicit labels. Mixing them within a single body is illegal *(**V26**)*.

### 10.1 Structured form (preferred)

```text
if %p1 { ... } else { ... }

loop {
    ...
    breakif %done;
    ...
    continueif %skip;
    ...
}

```

**Uniformity determines lowering**, and the operand's register class determines uniformity:

| Guard class | Uniformity | Lowering |
| --- | --- | --- |
| `.lanemask` | divergent | EXEC-mask save / and / restore |
| `.sgpr.b32` or `%scc` | uniform | `s_cbranch_scc*` |

An explicit `.uniform` / `.divergent` annotation MAY be written. It is an **assertion**: if it disagrees with the operand's class, verification fails. Assertions that cannot lie are worth having; assertions that merely restate the declaration are not, so they are optional.

* **V27** — Structured regions balance and nest properly.
* **V28** — `breakif` and `continueif` require an enclosing `loop`. `else` requires an immediately preceding `if`.

### 10.2 Explicit form

Standard `label:` definitions with `s_cbranch_*` and `s_branch`.

* **V29** — Labels are uniquely defined within a body and every branch target resolves within the same body.
* **V30** — A branch MUST NOT enter or exit a structured region.

---

## 11. Kernel ABI

### 11.1 Launch directives

Placed at the top of a kernel body, before register declarations. Zero-valued fields are omitted. Column-aligned by the canonical printer.

```text
.kernarg_size               32;
.kernarg_align               8;
.group_segment_size       4096;
.dynamic_group_segment;
.private_segment_size        0;
.reqd_workgroup_size 256, 1, 1;
.max_flat_workgroup_size   256;
.waves_per_eu             1, 4;
.kernarg_preload             4;

```

`.dynamic_group_segment` is a boolean flag (present = enabled, absent = disabled) indicating the use of dynamically sized shared memory.

* **V31** — `.kernarg_align` MUST be a power of two, ≥ 4. `.reqd_workgroup_size` product MUST NOT exceed `.max_flat_workgroup_size`. `.max_flat_workgroup_size` MUST be in `[1, 1024]`. `.waves_per_eu` MUST satisfy `min ≤ max` and lie within the target's occupancy range. `.kernarg_preload` requires a target with kernarg preload support (`gfx90a`, `gfx942`) and MUST be in the range `[0, 127]` dwords. Preloading always begins at dword 0. An unrecognised launch directive is a verification error.

These map onto the assembler's `.amdhsa_*` directives at lowering.

### 11.2 Explicit arguments

Parameters are assigned offsets in declaration order, each at the next offset satisfying its alignment (natural, or `.align`, whichever is stricter), with padding inserted as needed.

* **V33** — Every `.param` lies entirely within `.kernarg_size`.
* **V34** — `.align` values MUST be powers of two and MUST NOT be weaker than the parameter's natural alignment.

### 11.3 Hidden arguments

Hidden implicit arguments are appended after all explicit arguments and are **omitted from the AMDTX text signature** — they are the runtime's contract, not the kernel author's.

Under code object V5 the implicit block is 256 bytes and is appended after all explicit arguments. Its internal layout is normative in `AMDGPUUsage` and is reproduced nowhere in this document; lowering MUST source field offsets from the LLVM definition rather than from AMDTX.

* **V36** — Hidden arguments follow all explicit arguments in memory layout and MUST NOT be reordered or interleaved.

---

## 12. Memory Model and Synchronisation

### 12.1 The core rule

There is **no** implicit ordering between an asynchronous memory operation and any later instruction that reads its destination. Adjacency in the statement list conveys nothing.

### 12.2 Wait counters

| Counter | Tracks | GFX9 range | GFX10 range | GFX11 range |
| --- | --- | --- | --- | --- |
| `vmcnt` | vector memory loads | 0–63 | 0–63 | 0–63 |
| `lgkmcnt` | LDS, scalar memory, message | 0–15 | 0–63 | 0–63 |
| `vscnt` | vector memory **stores** | — | 0–63 | 0–63 |

**`vscnt` is not part of `s_waitcnt`.** On GFX10 and GFX11 it is a separate instruction, `s_waitcnt_vscnt %null, N`. AMDTX writes it as a distinct mnemonic:

```text
waitcnt  vmcnt(0), lgkmcnt(0);
waitcnt_vscnt 0;                  // GFX10/GFX11 only

```

* **V12** — `waitcnt_vscnt` requires GFX10 or GFX11.
* **V37** — Each counter value MUST lie within the target's range for that counter.

### 12.3 The `autowait` pass

`autowait` inserts conservative waits (`vmcnt(0)`, `lgkmcnt(0)`) sufficient to make every read-after-async-write safe. It is an optional normalising pass, not a correctness backstop. Hand-tuned `waitcnt` is preferred and `autowait` MUST NOT weaken an existing explicit wait.

* **W1** — Reading a register with a pending un-waited load is a warning, not an error, because an optional autowait pass may be configured to fix it before verification.

### 12.4 Fences and scopes

```text
fence .acquire  .workgroup;
fence .release  .agent;
fence .seq_cst  .system;

```

Orderings: `.acquire`, `.release`, `.acq_rel`, `.seq_cst`. Scopes: `.system`, `.agent`, `.workgroup`, `.wavefront`, `.singlethread`, plus the `.one_as` variants that restrict synchronisation to a single address space.

These are the AMDHSA sync scopes. Atomic instructions (e.g., `global_atomic_add`) specify cache policies directly via modifiers (`.glc`, `.slc`). Scoped ordering logic directly applies to explicit `fence` instructions, which provide the proper synchronization scaffolding for atomic operations.

* **V38** — Fence ordering and scope MUST both be present. A bare `fence` is rejected.

Lowering expands a fence into the target's cache-maintenance sequence (`buffer_wbl2`, `buffer_inv`, `s_waitcnt`, and the appropriate cache-policy bits).

---

## 13. Escape Hatches

```text
raw "s_nop 0";

raw "v_cmpx_lt_f32 %0, %1"
    .use(%v1, %v2)
    .clobber(%exec, %vcc);

rawbytes 0xbf800000, 0xbf800001
    .clobber(%m0);

```

`raw` emits text verbatim into the assembly stream. `rawbytes` emits dwords verbatim into the instruction stream. Operand substitution uses `%0`, `%1`, … indexed over the combined `.def` and `.use` lists.

Both are **optimisation barriers**: no pass moves an instruction across them, and no pass reasons about their effects beyond the declared lists.

* **V39** — Every register a `raw`/`rawbytes` reads MUST appear in `.use`; every register it writes MUST appear in `.def` or `.clobber`. An undeclared write is undefined behaviour and the verifier rejects it where it can prove it.

---

## 14. Debug Information

```text
.file 1 "gemm.hip";
.file 2 "/opt/rocm/include/hip/hip_runtime.h";
...
.loc 1 42 17;

```

`.loc <file> <line> [<column>]` attaches to the next instruction. A `.loc` at the end of a body with no following instruction is a warning (**W2**) and is dropped by the printer.

* **V2** — Every `.loc` file index refers to a declared `.file`.

---

## 15. Canonical Text Form

The printer is a **pure formatter**. It runs verification first, refuses to print an invalid module, and derives every mapping from IR data rather than from re-parsing its own output. The printer emits no comments at all; therefore, `print(parse(text))` is lossy on comments by design, while `parse(print(m))` is exact.

Normative formatting rules:

1. One statement per line.
2. Body contents indented four spaces per nesting level.
3. Launch directives column-aligned on their values, within a body.
4. Register declarations grouped by class, in declaration order.
5. Inline-constant integers in signed decimal; literal dwords in `0x` lowercase hex, zero-padded to eight digits.
6. Address displacements in signed decimal; zero displacement omitted entirely (`[%v1]`, never `[%v1+0]`).
7. Float immediates in shortest round-trip form.
8. `.enc(auto)` omitted.
9. Zero-valued launch directives omitted.
10. Exactly one blank line between bodies; none at end of file.

**V32 (conformance):** for every valid module *m*, `parse(print(m))` yields a module equal to *m* under structural equality.

---

## 16. Verification Rule Index

| # | Rule | § |
| --- | --- | --- |
| **V1** | Preamble present and correctly ordered | 3.1 |
| **V2** | `.file` declared before any referencing `.loc` | 3.1, 14 |
| **V3** | Kernels are not call targets | 3.2 |
| **V4** | No recursive or indirect calls | 3.2 |
| **V5** | `.wave 32` only on GFX10+; `.wave 64` universally legal | 4.1 |
| **V6** | Scalar ops write scalar destinations | 9.1 |
| **V7** | Vector ops write vector destinations | 9.1 |
| **V8** | Address-operand register classes valid for the encoding | 9.1 |
| **V9** | Memory access width equals data-register width | 9.1 |
| **V10** | MFMA restricted to CDNA targets | 9.2 |
| **V11** | WMMA restricted to GFX11+ | 9.2 |
| **V12** | `waitcnt_vscnt` restricted to GFX10/GFX11 | 9.2, 12.2 |
| **V13** | Register widths are legal tuple sizes | 5 |
| **V14** | `.agpr` requires an AGPR-capable target | 7.2 |
| **V15** | No direct writes to `%exec` outside `raw` | 7.3 |
| **V16** | Every operand register has a matching `.reg` declaration | 7.1 |
| **V17** | Inline constants within legal range, negatives accepted | 8.1 |
| **V18** | At most one literal dword per instruction | 8.1 |
| **V19** | No 64-bit literal under VOP3/VOP3P | 8.1 |
| **V20** | Address displacement within the encoding's range | 8.2 |
| **V21** | `s_barrier` not under divergent control flow | 9.2 |
| **V22** | Cross-lane lane indices below active wave width | 9.2 |
| **V23** | Every kernel exit path ends in `s_endpgm` | 9.3 |
| **V24** | Every function exit path ends in `ret` | 9.3 |
| **V25** | Pinned encoding supports the selected operands | 9.4 |
| **V26** | Structured and explicit control flow not mixed in one body | 10 |
| **V27** | Structured regions balanced and properly nested | 10.1 |
| **V28** | Valid `breakif` / `continueif` / `else` placement | 10.1 |
| **V29** | Labels uniquely defined and resolvable within the body | 10.2 |
| **V30** | No branching across a structured-region boundary | 10.2 |
| **V31** | Launch-directive values within legal ranges | 11.1 |
| **V32** | `parse(print(m))` equals *m* | 15 |
| **V33** | Kernarg extents within `.kernarg_size` | 11.2 |
| **V34** | `.align` values MUST be powers of two and MUST NOT be weaker than the parameter's natural alignment | 11.2 |
| **V36** | Hidden args follow explicit args, unreordered | 11.3 |
| **V37** | Wait-counter values within the target's range | 12.2 |
| **V38** | Fences carry both ordering and scope | 12.4 |
| **V39** | `raw`/`rawbytes` declare all defs, uses, and clobbers | 13 |
| **V40** | Sub-register slice `[N]` or `[N:M]` MUST NOT exceed tuple declaration | 7.5 |
| **V41** | Array initializer lengths MUST match declared array sizes | 3.1 |
| **W1** | *(warning)* Register read with a pending un-waited load | 12.3 |
| **W2** | *(warning)* Trailing `.loc` with no following instruction | 14 |

*Retired: V35.*

---

## 17. The Physical Line *(informative)*

```text
.amdtx text
   │
   ├─► parse ──────────► AMDTX module
   │                        │
   │                        ├─► autowait          (optional normalisation)
   │                        ├─► legalise          (optional normalisation)
   │                        ├─► verify (V1–V34, V36–V41, W1–W2)
   │                        ├─► instruction select (per-target tables, V25)
   │                        ├─► structurise → EXEC-mask expansion
   │                        ├─► register allocate (accum_offset split on CDNA)
   │                        └─► emit
   │                             ├─► .s text
   │                             ├─► kernel descriptor (64 B)
   │                             └─► msgpack metadata note
   │
   └─► print ─────────► .amdtx text        (V32: round-trips)

```

---

## 18. Worked Example

```text
.amdtx 1.0;
.target gfx942;
.wave 64;

.file 1 "saxpy.hip";

.visible .kernel saxpy(
    .param .global .read_only  .b64 x,
    .param .global .read_write .b64 y,
    .param                     .b32 n,
    .param                     .b32 alpha_bits
) {
    .kernarg_size            24;
    .kernarg_align            8;
    .max_flat_workgroup_size 256;
    .waves_per_eu           1, 8;

    .reg .sgpr.b128  %karg;          // x, y  (4 dwords)
    .reg .sgpr.b64   %scal;          // n, alpha_bits
    .reg .sgpr.b32   %s<8>;
    .reg .vgpr.b32   %v<8>;
    .reg .vgpr.b64   %vd<2>;
    .reg .lanemask   %p<2>;

    .loc 1 12 3;
    s_load_b128 %karg, [%kernarg_ptr+0];      // x -> %karg[0:1], y -> %karg[2:3]
    s_load_b64  %scal, [%kernarg_ptr+16];     // n -> %scal[0], alpha -> %scal[1]
    waitcnt lgkmcnt(0);

    .loc 1 13 3;
    s_mul_i32     %s0, %wgid.x, 256;
    v_add_u32     %v0, %s0, %tid.x;
    v_cmp_gt_i32  %p0, %scal[0], %v0;         // scal[0] > v0 is v0 < scal[0]

    if %p0 {
        v_lshlrev_b64 %vd0, 2, %v0;
        v_add_co_u32  %vd0[0], %karg[0], %vd0[0];     // &x[i]
        v_addc_co_u32 %vd0[1], %karg[1], %vd0[1];
        global_load_b32 %v1, [%vd0];

        v_lshlrev_b64 %vd1, 2, %v0;
        v_add_co_u32  %vd1[0], %karg[2], %vd1[0];     // &y[i]
        v_addc_co_u32 %vd1[1], %karg[3], %vd1[1];
        global_load_b32 %v2, [%vd1];

        waitcnt vmcnt(0);
        v_fma_f32 %v3, %scal[1], %v1, %v2;
        global_store_b32 [%vd1], %v3;
    }

    s_endpgm;
}

```

---

## 19. Deferred to v1.1 *(informative)*

1. No call ABI, so no external `.func` linkage.
2. Kernarg layout for aggregate and `.buffer`/`.dynshared` parameter kinds is unspecified even after §11.2 explicit arguments definition — only scalar `.b`*N* params are covered.
3. No `call` instruction is defined in §9 at all, yet **V3** and **V4** constrain call targets. Both rules are correct and load-bearing (V4 in particular guarantees inlining terminates), but the instruction they govern is only implied by the mnemonic-passthrough grammar. v1.1 should define `call` explicitly or state that it is a reserved mnemonic.
4. `s_barrier` semantics for single-wave workgroups (**V21** requires all waves reach it; with one wave the instruction is a no-op and the rule is untestable).