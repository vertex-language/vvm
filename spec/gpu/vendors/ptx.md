# PTX — IR and Text Specification

**Version 1.0 · Target audience: implementers of the IR, `Verify`, the text printer, and any pass over `ptx.Module`**

---

## 0. Front Matter

### 0.1 Document conventions

**MUST**, **MUST NOT**, **SHALL**, **SHOULD**, and **MAY** carry RFC 2119 meanings. Rules labelled **V***n* are `Verify` errors; rules labelled **W***n* are `Verify` warnings — the module still prints. The complete index is §16.

Sections marked *(informative)* are non-normative.

### 0.2 The NVIDIA baseline rule

The normative definition of the language is NVIDIA's *Parallel Thread Execution ISA*. This document does not redefine PTX; it specifies **the subset the `ptx` package models** and **the exact text the printer emits**. Where the package narrows, normalises, or declines to model something, the departure is called out inline as a **Model deviation** with a justification.

Adding an exported symbol that has no counterpart in the PTX grammar is a package defect. Convenience types drawn from Go's type system are not a reason to deviate.

### 0.3 Normative references

* NVIDIA *Parallel Thread Execution ISA* — the language of record.
* NVIDIA *CUDA Binary Utilities* — `ptxas`, `nvdisasm`.
* CUDA Toolkit release notes — the `.version` ↔ toolkit mapping in §4.2.

`ptxas` is the **verifier of record**. `Verify` is a fast structural and version-gating pre-pass, never a substitute.

### 0.4 File extensions

| Extension | Content | Direction |
|---|---|---|
| `.ptx` | PTX assembly text | **out only** |
| `.cubin` / `.sass` | `ptxas` / `nvdisasm` output | out of scope |

**Model deviation:** unlike a round-tripping IR, the package has **no parser**. PTX has no binary wire format — the `.ptx` text *is* the interchange format consumed by `ptxas` and the driver — so decoding is out of scope and there is no `parse(print(m)) == m` obligation. The corresponding guarantee is §15's byte-stability rule instead.

---

## 1. Scope and Design Principles

The `ptx` package is a structured, in-memory IR for a `.ptx` translation unit: module directives, module-scoped variables, kernels (`.entry`), device functions (`.func`), and instruction bodies. It carries no formatting logic; text lives in `ptx/encoding/text`.

**P1 — NVIDIA only.** One `sm_*` target per module. There is no AMD path and no portable-VM abstraction.

**P2 — Grammar-driven.** Every exported symbol corresponds to a construct in the PTX grammar. Types, spaces, opcodes, and special registers are constant enums backed by internal tables, so invalid text cannot be constructed by string manipulation.

**P3 — Instructions are values, not strings.** An `Instr` holds an `Op`, a typed `Quals` struct, positional `Types`, and `Operand`s. The mnemonic is *derived* by `(*Instr).Mnemonic`, never stored.

**P4 — Canonical qualifier order lives in one table.** `opTable` records, per opcode, the order in which qualifier slots print. Equivalent IR prints byte-identically regardless of the order qualifiers were supplied. If a mnemonic ever prints its qualifiers wrongly, `op.go` is the only place to fix it.

**P5 — Bodies are editable.** `Body` supports `Append`, `InsertBefore`, `Replace`, `Remove`, `RemoveRange`. Emit methods return `*Instr`, so analysis and rewrite passes are ordinary Go.

**P6 — No inference.** The package does not type-check operand compatibility, infer rounding modes, or legalise. `Verify` reports structural and gating problems only, never mutates, and never stops at the first finding.

**P7 — Virtual registers only.** `RegFile` hands out names; `ptxas` performs real allocation. The package never assigns a physical register.

**P8 — Exact-bits floating point.** `F32Imm` and `F64Imm` print only in `0f…` / `0d…` form, so no value — including NaN and the infinities — can render as invalid or lossy PTX.

**P9 — Explicit predication.** Guards attach to a returned `*Instr` via `.If(p)` / `.IfNot(p)`, so a guard cannot leak across a label or onto a neighbouring instruction.

**P10 — Escape hatches stay structural.** `Emit` takes a mnemonic string but produces a real `Instr`: it participates in predication, walking, verification, and printing exactly like a modelled opcode.

---

## 2. Lexical Structure *(of the emitted text)*

Case-sensitive. Directives, mnemonics, and type specifiers are lowercase; `.NaN` is the sole capitalised qualifier.

| Token class | Form |
|---|---|
| Identifier | `[A-Za-z_$%][A-Za-z0-9_$]*` |
| Virtual register | `%` prefix decimal, or `%` name |
| Special register | `%` name, optionally `.x`/`.y`/`.z` |
| Directive | `.` identifier |
| Label definition | identifier `:` |
| Integer immediate | signed decimal, or decimal with `U` suffix |
| Float immediate | `0f` + 8 hex digits, or `0d` + 16 hex digits |
| String | `"` … `"`, Go-quoted (`strconv.Quote`) |
| Line comment | `//` to end of line |

Statements terminate with `;`. Blocks use `{ }`. Whitespace is insignificant to `ptxas` and significant only to the printer (§15).

Comments are whitespace. `Instr.Comment` is emitted **only** when the printer is configured with `WithComments(true)`; the default is off. The module header comment block is controlled by `WithHeader` and defaults to `Generated by vertex`.

---

## 3. Module Structure and Grammar

### 3.1 Emission order

`Module` holds declarations in **one ordered list**, not per-kind lists, because PTX requires declaration before use and the relative order of globals, prototypes, and definitions is semantically meaningful.

The printer emits, in this order:

1. **Header comment** — optional, `//`-delimited.
2. **Preamble** — `.version`, `.target` (plus `TargetOpts`), `.address_size`. `.version` and `.address_size` are mandatory; a module missing either fails to print (**V1**, **V2**).
3. **`.blocksareclusters`** — if set (**W1**).
4. **Module `.pragma`** — zero or more.
5. **File table** — `.file` entries in index order, 1-based, preceded by a blank line.
6. **Declarations** — `Module.Decls()` in source order, each preceded by a blank line.

### 3.2 Global rules

* **Declare before use.** The declaration list is the source order; the package does not reorder.
* **One flat module namespace.** Variables, functions, prototypes, and aliases share it. Labels and registers are body-scoped.
* **File dedupe.** `Module.File(name)` returns the existing entry if the name is already registered; indices are assigned `len(files)+1` at first registration.
* **Bodiless callables.** A `Callable` with a nil `Body` prints as a declaration (`;` in place of the block) and MUST carry `.extern` linkage (**V10**).

### 3.3 Grammar

```text
module        := header?
                 ".version" ver
                 ".target" target ("," target-opt)*
                 ".address_size" ("32" | "64")
                 ".blocksareclusters"?
                 module-pragma*
                 file-decl*
                 decl*

ver           := int "." int
target        := "sm_" int ("a" | "f")?
target-opt    := "texmode_unified" | "texmode_independent"
               | "map_f64_to_f32" | "debug"
module-pragma := ".pragma" string ";"
file-decl     := ".file" int string ("," int "," int)?

decl          := var-decl | proto-decl | alias-decl | section-decl
               | kernel-def | func-def

var-decl      := linkage? space align? vec? type name array? attr* init? ";"
linkage       := ".visible" | ".extern" | ".weak" | ".common"
space         := "." space-name ("::" sub-qual)?
align         := ".align" int
vec           := ".v2" | ".v4"
array         := "[]" | "[" int "]"
attr          := ".attribute(" (".managed" | ".unified") ")"
init          := "=" init-expr
init-expr     := imm | sym | "{" init-expr ("," init-expr)* "}"

proto-decl    := name ":" ".callprototype" ret-list? "_" "(" param-decl,* ")"
                 ".noreturn"? ";"
alias-decl    := ".alias" name "," name ";"
section-decl  := ".section" name "{" (".b8" byte,* ";")* "}"

kernel-def    := linkage? ".entry" name "(" param-decl,* ")" tuning* body
func-def      := linkage? ".func" ret-list? name "(" param-decl,* ")"
                 ".noreturn"? tuning* (body | ";")
ret-list      := "(" param-decl,* ")"
param-decl    := ".param" align? type ptr-info? name array?
ptr-info      := ".ptr" space align?

tuning        := ".maxntid" dim3 | ".reqntid" dim3
               | ".minnctapersm" int | ".maxnreg" int
               | ".reqnctapercluster" dim3 | ".maxclusterrank" int
               | ".explicitcluster" | attr | ".pragma" string,+ ";"
dim3          := int "," int "," int

body          := "{" reg-decl* var-decl* item* "}"
reg-decl      := ".reg" type "%" prefix "<" int ">" ";"
               | ".reg" type "%" name ";"

item          := instr | label-bind | loc-dir | branchtargets | calltargets
               | pragma | block
block         := "{" var-decl* item* "}"

instr         := guard? mnemonic operand,* ";" comment?
guard         := "@" reg | "@!" reg
mnemonic      := base qual* type-spec*
label-bind    := name ":"
loc-dir       := ".loc" int int int
                 ("," "function_name" name ("+" int)?)?
                 ("," "inlined_at" int int int)?
branchtargets := name ":" ".branchtargets" name,+ ";"
calltargets   := name ":" ".calltargets" name,+ ";"

operand       := reg | sreg | imm | float-imm | sym | mem | vec-op
               | group | pair
mem           := "[" base "]" | "[" base "+" int "]" | "[" base "-" int "]"
vec-op        := "{" operand,+ "}"
group         := "(" operand,* ")"
pair          := operand "|" operand
```

---

## 4. Targets and Versions

### 4.1 Targets

`Target{SM, Suffix}` prints as `sm_<N>`, `sm_<N>a`, or `sm_<N>f`.

| Suffix | Form | Meaning |
|---|---|---|
| `Base` | `sm_90` | forward compatible |
| `ArchSpc` | `sm_90a` | architecture-specific; **not** forward compatible |
| `Family` | `sm_100f` | family-specific; requires `.version` ≥ 8.8 (**W2**) |

Predeclared: `SM50`, `SM52`, `SM53`, `SM60`, `SM61`, `SM62`, `SM70`, `SM72`, `SM75`, `SM80`, `SM86`, `SM87`, `SM89`, `SM90`, `SM100`, `SM103`, `SM110`, `SM120`, `SM121`; the `a` variants `SM90a`, `SM100a`, `SM103a`, `SM110a`, `SM120a`, `SM121a`; the `f` variants `SM100f`, `SM103f`, `SM110f`, `SM120f`, `SM121f`.

`SM110` is Jetson Thor, named `sm_101` in CUDA 12.8/12.9. Unmodelled future targets are constructed directly: `Target{SM: 130, Suffix: ArchSpc}`.

`TargetOpt` values: `TexmodeUnified`, `TexmodeIndependent`, `MapF64ToF32`, `Debug`. They print comma-separated after the target.

### 4.2 ISA versions

`ISAVersion{Major, Minor}`, compared with `GTE`, tested for absence with `IsZero`.

| Constant | `.version` | Toolkit |
|---|---|---|
| `ISA70`–`ISA78` | 7.0–7.8 | CUDA 11.0–11.8 |
| `ISA80`–`ISA85` | 8.0–8.5 | CUDA 12.0–12.6 |
| `ISA86` | 8.6 | CUDA 12.7 |
| `ISA87` | 8.7 | CUDA 12.8 |
| `ISA88` | 8.8 | CUDA 12.9 (family targets) |
| `ISA90` | 9.0 | CUDA 13.0 (`sm_110`, `.blocksareclusters`) |
| `ISA91`–`ISA93` | 9.1–9.3 | CUDA 13.1–13.3 |

**Model deviation:** `ISA60`, `ISA62`, `ISA63` are declared in `op.go` and `ISA41` in `sregs.go` rather than in `version.go`, because only those tables reference them. `ISA61()` is a function and `ISAetc` an identity function — warts, not design (§19).

### 4.3 Address size

`AddrSize` is `Addr32` (32) or `Addr64` (64). Any other value is rejected by both `Verify` (**V2**) and `Print`.

---

## 5. Types

`Type` is a scalar type specifier. **Vector-ness is never a property of the type** — it is the `.vN` qualifier on an instruction (derived from operand arity) or `Var.Vec` on a declaration.

| Group | Members | Bits | Gating |
|---|---|---|---|
| Signed | `S8` `S16` `S32` `S64` | 8/16/32/64 | — |
| Unsigned | `U8` `U16` `U32` `U64` | 8/16/32/64 | — |
| Float | `F16` `F32` `F64` | 16/32/64 | — |
| Bits | `B8` `B16` `B32` `B64` | 8/16/32/64 | — |
| Bits | `B128` | 128 | ISA 8.3 |
| Predicate | `Pred` | 1 | — |
| Packed FP | `F16x2` | 32 | — |
| Brain float | `BF16` `BF16x2` | 16/32 | ISA 7.0 |
| Tensor float | `TF32` | 32 | ISA 7.0 |
| FP8 | `E4M3` `E5M2` `E4M3x2` `E5M2x2` | 8/8/16/16 | ISA 8.1 |
| FP6/FP4 | `E3M2` `E2M3` `E2M1` `E3M2x2` `E2M3x2` `E2M1x2` | 6/6/4/16/16/8 | ISA 8.6 |
| Scale | `UE8M0` `UE8M0x2` `UE4M3` | 8/16/8 | ISA 8.6 |
| Packed int | `U16x2` `S16x2` | 32 | ISA 8.0 |
| Packed int | `U8x4` `S8x4` | 32 | ISA 9.2 |
| Sub-byte | `U4` `S4` `B4` | 4 | — |
| Tensor packed | `B4x16` (64), `B4x16P64` (128), `B6x16P32` (128) | — | ISA 8.8 |
| Opaque | `TexRef` `SamplerRef` `SurfRef` | 0 | — |

`Type.String()` returns the dotted form (`.f32`), `Name()` the bare form, `Bits()` the width (0 for opaque), `IsFloat()` the FP flag, `MinISA()` the introducing version (**W5**).

The alternate floating-point formats are **instruction types, never storage types**.

---

## 6. State Spaces

`Space` packs a base space in the low byte and an optional sub-qualifier in the high byte, so values stay constant and `==`-comparable.

| Space | Prints | Notes |
|---|---|---|
| `RegSpace` | `.reg` | declared through `RegFile`, never `Var` (**V8**) |
| `SRegSpace` | `.sreg` | predefined; see §7.4 |
| `Const` | `.const` | |
| `Global` | `.global` | |
| `Local` | `.local` | |
| `ParamSpace` | `.param` | |
| `Shared` | `.shared` | |
| `Tex` | `.tex` | deprecated in PTX; retained for compatibility |

**Model deviation:** the register and parameter spaces are named `RegSpace` and `ParamSpace` because `Reg` and `Param` are already the value types.

### 6.1 Sub-qualifiers

`s.Sub(q)` attaches one; `Base()`, `SubQual()` recover the parts. `String()` renders `.shared::cluster`.

| Base space | Legal sub-qualifiers |
|---|---|
| `Shared` | `SubCTA` (`::cta`), `SubCluster` (`::cluster`) |
| `ParamSpace` | `SubEntry` (`::entry`), `SubFunc` (`::func`) |

Anything else is **V5**. The builder does not enforce this; `Verify` does.

---

## 7. Registers

### 7.1 The register file

`RegFile` is a per-body naming allocator grouped by type. `New(t)` returns the next virtual register; `NewN(t, n)` returns `n` of them; `Named(t, name)` declares an explicitly named one.

`Reg` is a handle around an internal `regInfo`. The zero `Reg` is invalid, prints as `%<invalid>`, and does not panic. `Reg.IsValid()` reports provenance; `SameReg(a, b)` compares identity, not text.

### 7.2 Prefixes

Generated names are `%` + class prefix + 1-based index, following `nvcc` convention so output is diffable against it:

| Type | Prefix |
|---|---|
| `Pred` | `p` |
| `F16`, `BF16` | `h` |
| `F32` | `f` |
| `F64` | `fd` |
| `B128` | `rq` |
| any 8- or 16-bit | `rs` |
| any 32-bit | `r` |
| any 64-bit | `rd` |
| anything else | `x` |

The special cases are tested **before** the width switch, so `F32` is `%f` and not `%r`. Types whose width is not 8/16/32/64 and which are not special-cased (the sub-byte and FP6/FP4 forms) fall through to `x`.

### 7.3 Declaration form

Each class collapses into one ranged declaration. Named registers print individually, after the classes.

```text
	.reg .u32   %r<3>;
	.reg .pred  %p<2>;
	.reg .f32   %f<3>;
	.reg .u32   %idx;
```

**Printer rule:** the ranged count is `Count + 1`. Indices are 1-based, so `%r<3>` declares `%r0`–`%r2` while `New` has handed out only `%r1` and `%r2`. `%r0` is declared and never used — this matches `nvcc`'s output and is deliberate.

Duplicate named registers are recorded at declaration time and reported by **V12**.

### 7.4 Special registers

`SReg` values are referenceable without declaration and carry their own type, so the IR knows `%clock64` is 64-bit and `%tid.x` is not. `MovSReg(d, s)` uses `s.Type()` as the instruction type.

| Family | Members | Gating |
|---|---|---|
| Thread / block | `TidX/Y/Z`, `NTidX/Y/Z`, `CtaIdX/Y/Z`, `NCtaIdX/Y/Z` | — |
| Warp / SM | `LaneID`, `WarpID`, `NWarpID`, `SmID`, `NSmID` | — |
| Grid | `GridID` | sm_30 |
| Cluster | `IsExplicitCluster`, `ClusterIdX/Y/Z`, `NClusterIdX/Y/Z`, `ClusterCtaIdX/Y/Z`, `ClusterNCtaIdX/Y/Z`, `ClusterCtaRank`, `ClusterNCtaRank` | ISA 7.8, sm_90 |
| Lane masks | `LaneMaskEq/Le/Lt/Ge/Gt` | sm_20 |
| Clocks | `Clock`, `ClockHi`, `Clock64` | `Clock64`/`ClockHi` sm_20 |
| Timers | `GlobalTimer`, `GlobalTimerLo`, `GlobalTimerHi` | sm_30 |
| Shared mem sizes | `TotalSmemSize`, `DynamicSmemSize` (ISA 4.1, sm_20), `AggrSmemSize` (ISA 8.1, sm_90) | |
| Reserved smem | `ReservedSmemOffsetBegin/End/Cap` | ISA 8.5, sm_90 |
| Graph | `CurrentGraphExec` | ISA 8.0, sm_50 |
| Constant | `WarpSz` | prints `WARP_SZ`, **no leading `%`** |

`IsExplicitCluster` is `.pred`; `GridID`, `Clock64`, the global timers, and `CurrentGraphExec` are `.u64`; everything else is `.u32`.

**Indexed families are constructors, not 32 constants:** `Env(n)` → `%envreg<n>`, `PM(n)` → `%pm<n>`, `PM64(n)` → `%pm<n>_64`, `ReservedSmemOffset(n)` → `%reserved_smem_offset_<n>`. Each returns a `Sym`, so it carries no type and is not gated.

---

## 8. Operands

`Operand` is a closed interface — only this package implements it. `Addressable` narrows it to things that may be the base of a `Mem`.

| Type | Text | Addressable |
|---|---|---|
| `Reg` | `%r1` | ✓ |
| `SReg` | `%tid.x` | ✗ |
| `Imm int64` | signed decimal | ✗ |
| `UImm uint64` | decimal + `U` | ✗ |
| `F32Imm` | `0fXXXXXXXX` | ✗ |
| `F64Imm` | `0dXXXXXXXXXXXXXXXX` | ✗ |
| `Sym string` | verbatim | ✓ |
| `*Var` | name | ✓ |
| `*Param` | name | ✓ |
| `*Func` | name | ✗ |
| `*Label` | name | ✗ |
| `*Proto` | name | ✗ |
| `*BranchTargets` / `*CallTargets` | name | ✗ |
| `Mem` | `[b]`, `[b+8]`, `[b-8]` | ✗ |
| `VecOp` | `{a, b, c, d}` | ✗ |
| `group` *(unexported)* | `(a, b)` | ✗ |
| `pair` *(unexported)* | `d\|p` | ✗ |

### 8.1 Immediates

`UImm` prints with the `U` suffix so values above the `.s64` range are not reinterpreted as negative.

Float immediates are **exact-bits only** (P8). There is no decimal float form, and therefore no rounding decision, no locale dependence, and no way to emit an unrepresentable literal.

### 8.2 Addresses

`At(base)` → `[%rd1]`; `At(base, 16)` → `[%rd1+16]`; a negative offset prints with its own sign, `[%rd1-8]`. A nil base prints `[<nil>]` rather than panicking.

Offsets are `int64` and are not range-checked; the encoding limits are `ptxas`'s business (P6).

### 8.3 Vectors, groups, and pairs

`Vec(a, b, c, d)` builds a `VecOp`. **Its length supplies the `.vN` qualifier**, so the vector width is stated exactly once — `b.inst` scans destinations and sources for a `VecOp` and sets `Q.Vec` from its arity. The caller never writes `.v4`.

`group` is the parenthesised list used by `call` for arguments and return values. `Or(d, p)` builds the `d|p` pair used by `shfl.sync`'s optional predicate destination and by `SetpBool`.

---

## 9. Instructions

### 9.1 Shape

```go
type Instr struct {
	Pred    Pred
	Op      Op
	Q       Quals
	Types   [2]Type
	Dst     []Operand
	Src     []Operand
	Comment string
}
```

`Operands()` returns destinations followed by sources. `Base()` returns the bare mnemonic (or the custom string for `OpCustom`). Type specifiers are **positional and mandatory**; qualifiers are variadic `Qual` values supplied in any order.

**Model deviation:** `Types` is a fixed `[2]Type`. No modelled opcode takes three type specifiers; anything that does belongs in `Emit`'s mnemonic string.

### 9.2 Mnemonic derivation

`Mnemonic()` is, normatively:

1. `Base()`.
2. For each `qualKind` in `opTable[Op].quals`, in order, `Q.text(k)` — the empty string if the slot is unset.
3. For each element of `Types` that is not `NoType`, its dotted form.

This is the only place a mnemonic is assembled. The printer makes no mnemonic decisions (P4).

### 9.3 Qualifier slots

`Quals` has one slot per category; the zero value of each means absent. Supplying two qualifiers of the same category is a diagnostic, not a silent last-wins.

| Slot | Type | Values |
|---|---|---|
| `Round` | `Round` | `.rn .rz .rm .rp .rs .rni .rzi .rmi .rpi` |
| `Sem` | `Sem` | `.weak .relaxed .acquire .release .acq_rel .sc .mmio` |
| `Scope` | `Scope` | `.cta .cluster .gpu .sys` |
| `Space` | `Space` | §6 |
| `Cache` | `CacheOp` | `.ca .cg .cs .lu .cv .wb .wt` |
| `Evict` | `Evict` | `.L1::evict_normal/_first/_last/_unchanged`, `.L1::no_allocate` |
| `Width` | `Width` | `.lo .hi .wide` |
| `Cmp` | `Cmp` | `.eq .ne .lt .le .gt .ge .lo .ls .hi .hs .equ .neu .ltu .leu .gtu .geu .num .nan` |
| `Bool` | `BoolOp` | `.and .or .xor` |
| `Atom` | `AtomOp` | `.add .exch .min .max .inc .dec .cas .and .or .xor` |
| `Shfl` | `ShflMode` | `.up .down .bfly .idx` |
| `Vote` | `VoteMode` | `.all .any .uni .ballot` |
| `Match` | `MatchMode` | `.any .all` |
| `Redux` | `ReduxOp` | `.add .min .max .and .or .xor` |
| `Testp` | `TestpOp` | `.finite .infinite .number .notanumber .normal .subnormal` |
| `Level` | `Level` | `.cta .gl .sys .L1 .L2` |
| `Proxy` | `Proxy` | `.proxy.alias/.async/.async.global/.async.shared/.tensormap`, `.mbarrier_init` |
| `Dir` | `Dir` | `.l .r` |
| `Shf` | `ShfMode` | `.clamp .wrap` |
| `Vec` | `int` | `.v2 .v4 .v8` — set from operand arity, never by the caller |
| flags | `bool` | `.ftz .sat .satfinite .approx .full .uni .aligned .nc .relu .NaN .abs .to .shiftamt` |

### 9.4 Canonical orders

Named orders, reused across the opcode table:

| Name | Order |
|---|---|
| `qArith` | round, ftz, sat |
| `qMulish` | width, round, ftz, sat |
| `qUnFP` | round, approx, ftz |
| `qLoad` | sem, scope, space, nc, cache, evict, vec |
| `qStore` | sem, scope, space, cache, evict, vec |
| `qAtomic` | sem, scope, space, atom, cache |
| *generic* (`OpCustom`) | sem, scope, space, cache, vec |

### 9.5 The opcode table

`types` is the required count of type specifiers (**V17**); `ISA` and `SM` are the gating minimums (**W6**, **W7**). Blank means unrestricted.

**Arithmetic**

| Op | Mnemonic | Quals | types | ISA | SM |
|---|---|---|---|---|---|
| `OpAdd` | `add` | qArith | 1 | | |
| `OpSub` | `sub` | qArith | 1 | | |
| `OpMul` | `mul` | qMulish | 1 | | |
| `OpMad` | `mad` | qMulish | 1 | | |
| `OpFma` | `fma` | qArith | 1 | | |
| `OpDiv` | `div` | round, approx, full, ftz | 1 | | |
| `OpRem` | `rem` | — | 1 | | |
| `OpAbs` | `abs` | ftz | 1 | | |
| `OpNeg` | `neg` | ftz | 1 | | |
| `OpMin` | `min` | abs, NaN, ftz | 1 | | |
| `OpMax` | `max` | abs, NaN, ftz | 1 | | |
| `OpSqrt` | `sqrt` | qUnFP | 1 | | |
| `OpRcp` | `rcp` | qUnFP | 1 | | |
| `OpRsqrt` | `rsqrt` | qUnFP | 1 | | |
| `OpSin` | `sin` | qUnFP | 1 | | |
| `OpCos` | `cos` | qUnFP | 1 | | |
| `OpEx2` | `ex2` | qUnFP | 1 | | |
| `OpLg2` | `lg2` | qUnFP | 1 | | |
| `OpTanh` | `tanh` | approx, ftz | 1 | 7.0 | 75 |
| `OpTestp` | `testp` | testp | 1 | | |
| `OpCopysign` | `copysign` | — | 1 | | |
| `OpSad` | `sad` | — | 1 | | |
| `OpMul24` | `mul24` | width | 1 | | |
| `OpMad24` | `mad24` | width, sat | 1 | | |

**Extended-precision integer**

| Op | Mnemonic | Quals | types |
|---|---|---|---|
| `OpAddCC` | `add.cc` | — | 1 |
| `OpAddC` | `addc` | cmp | 1 |
| `OpSubCC` | `sub.cc` | — | 1 |
| `OpSubC` | `subc` | — | 1 |
| `OpMadCC` | `mad.cc` | width | 1 |
| `OpMadC` | `madc` | width | 1 |

**Bit manipulation**

| Op | Mnemonic | Quals | types | ISA | SM |
|---|---|---|---|---|---|
| `OpPopc` | `popc` | — | 1 | | |
| `OpClz` | `clz` | — | 1 | | |
| `OpBrev` | `brev` | — | 1 | | |
| `OpBfind` | `bfind` | shiftamt | 1 | | |
| `OpFns` | `fns` | — | 1 | 7.6 | 30 |
| `OpBfe` | `bfe` | — | 1 | | |
| `OpBfi` | `bfi` | — | 1 | | |
| `OpSzext` | `szext` | shf | 1 | 7.6 | 70 |
| `OpBmsk` | `bmsk` | shf | 1 | 7.6 | 70 |
| `OpDp4a` | `dp4a` | — | 2 | 6.1 | 61 |
| `OpDp2a` | `dp2a` | width | 2 | 6.1 | 61 |
| `OpPrmt` | `prmt` | — | 1 | | |
| `OpLop3` | `lop3` | — | 1 | 7.0 | 50 |

**Comparison and selection**

| Op | Mnemonic | Quals | types |
|---|---|---|---|
| `OpSet` | `set` | cmp, bool, ftz | 2 |
| `OpSetp` | `setp` | cmp, bool, ftz | 1 |
| `OpSelp` | `selp` | — | 1 |
| `OpSlct` | `slct` | ftz | 2 |

**Logic and shift**

| Op | Mnemonic | Quals | types | SM |
|---|---|---|---|---|
| `OpAnd` `OpOr` `OpXor` `OpNot` `OpCnot` | `and` `or` `xor` `not` `cnot` | — | 1 | |
| `OpShl` `OpShr` | `shl` `shr` | — | 1 | |
| `OpShf` | `shf` | dir, shf | 1 | 60 |

**Data movement and conversion**

| Op | Mnemonic | Quals | types | ISA | SM |
|---|---|---|---|---|---|
| `OpMov` | `mov` | — | 1 | | |
| `OpCvt` | `cvt` | round, ftz, satfinite, sat, relu | 2 | | |
| `OpCvtPack` | `cvt.pack` | sat, round | 2 | 7.0 | 72 |
| `OpCvta` | `cvta` | to, space | 1 | | |
| `OpLd` | `ld` | qLoad | 1 | | |
| `OpLdu` | `ldu` | space, vec | 1 | | |
| `OpSt` | `st` | qStore | 1 | | |
| `OpPrefetch` | `prefetch` | space, level, evict | 0 | | 20 |
| `OpPrefetchU` | `prefetchu` | level | 0 | | 20 |
| `OpIsspacep` | `isspacep` | space | 0 | | 20 |
| `OpMapa` | `mapa` | space | 1 | 8.0 | 90 |
| `OpGetctarank` | `getctarank` | space | 1 | 8.0 | 90 |
| `OpApplyPriority` | `applypriority` | space, evict | 0 | 7.4 | 80 |
| `OpDiscard` | `discard` | space, level | 0 | 7.4 | 80 |

**Control flow**

| Op | Mnemonic | Quals | types | ISA | SM |
|---|---|---|---|---|---|
| `OpBra` | `bra` | uni | 0 | | |
| `OpBrxIdx` | `brx.idx` | uni | 0 | 6.0 | 30 |
| `OpCall` | `call` | uni | 0 | | |
| `OpRet` | `ret` | uni | 0 | | |
| `OpExit` | `exit` | — | 0 | | |

**Synchronisation and communication**

| Op | Mnemonic | Quals | types | ISA | SM |
|---|---|---|---|---|---|
| `OpBarSync` | `bar.sync` | — | 0 | | |
| `OpBarArrive` | `bar.arrive` | — | 0 | | |
| `OpBarRed` | `bar.red` | bool | 1 | | 20 |
| `OpBarWarpSync` | `bar.warp.sync` | — | 0 | 6.0 | 30 |
| `OpBarrierSync` | `barrier.sync` | aligned | 0 | 6.0 | 30 |
| `OpBarrierArrive` | `barrier.arrive` | aligned | 0 | 6.0 | 30 |
| `OpBarrierClusterArrive` | `barrier.cluster.arrive` | sem, aligned | 0 | 7.8 | 90 |
| `OpBarrierClusterWait` | `barrier.cluster.wait` | sem, aligned | 0 | 7.8 | 90 |
| `OpMembar` | `membar` | level | 0 | | |
| `OpFence` | `fence` | proxy, sem, scope, space | 0 | 6.0 | 70 |
| `OpAtom` | `atom` | qAtomic | 1 | | |
| `OpRed` | `red` | qAtomic | 1 | | |
| `OpShflSync` | `shfl.sync` | shfl | 1 | 6.0 | 30 |
| `OpVoteSync` | `vote.sync` | vote | 1 | 6.0 | 30 |
| `OpMatchSync` | `match.sync` | match | 1 | 6.0 | 70 |
| `OpActivemask` | `activemask` | — | 1 | 6.2 | 30 |
| `OpReduxSync` | `redux.sync` | redux, abs, NaN | 1 | 7.0 | 80 |
| `OpElectSync` | `elect.sync` | — | 1 | 8.0 | 90 |
| `OpGridDepLaunch` | `griddepcontrol.launch_dependents` | — | 0 | 7.8 | 90 |
| `OpGridDepWait` | `griddepcontrol.wait` | — | 0 | 7.8 | 90 |

**Stack and miscellaneous**

| Op | Mnemonic | Quals | types | ISA | SM |
|---|---|---|---|---|---|
| `OpAlloca` | `alloca` | — | 1 | 7.3 | 52 |
| `OpStackSave` | `stacksave` | — | 1 | 7.3 | 52 |
| `OpStackRestore` | `stackrestore` | — | 1 | 7.3 | 52 |
| `OpBrkpt` | `brkpt` | — | 0 | | |
| `OpNanosleep` | `nanosleep` | — | 1 | 6.3 | 70 |
| `OpPmevent` | `pmevent` | aligned | 0 | | 20 |
| `OpTrap` | `trap` | — | 0 | | |
| `OpSetMaxNReg` | `setmaxnreg` | dir, aligned | 1 | 8.0 | 90 |

### 9.6 Per-opcode obligations

* **V21** — `Round`, `.approx` and `.full` are mutually exclusive on any instruction.
* **V22** — `fma` has no default rounding mode; a rounding qualifier is required.
* **V23** — `cvta` requires a state space.
* **V24 / V25** — `atom` / `red` require an operation qualifier.
* **V26** — `setp` requires a comparison qualifier.
* **V27** — `shf` requires both a direction and `.clamp` or `.wrap`.

---

## 10. Control Flow

### 10.1 Labels

A `*Label` is a symbolic branch target. **There are no byte offsets, so there is no fixed-point resolution pass.** `Body.Label(name)` uniquifies against every name already issued in the body by appending `_1`, `_2`, …; `Body.Bind(l)` places it.

* **V13** — a label is bound at most once per body.
* **V28** — every `*Label` appearing as a source operand is bound in the same body.

Label definitions print at **column 0**, not at body indentation.

### 10.2 Predication

`Pred{Reg, Neg}` prints as `@%p1` or `@!%p1` and precedes the mnemonic, separated by one space. `.If(p)` and `.IfNot(p)` attach it to the returned instruction — a guard therefore cannot drift onto a neighbour (P9).

* **V15** — the guard register is valid.
* **V16** — the guard register has type `Pred`.

### 10.3 Indexed branches and calls

`BrxIdx(idx, targets)` emits `brx.idx` **and** appends a `$L__bt<n>: .branchtargets …;` directive to the body. `CallIndirect(fn, proto, args, rets, targets)` emits `call` with either a trailing `*Proto` operand or, when `targets` is non-empty, a `$L__ct<n>: .calltargets …;` directive and a trailing `*CallTargets` operand. Sequence numbers are per-body.

**Model deviation:** the directive is a body `Item`, not a module declaration, so it prints at the point of emission rather than at the end of the body.

---

## 11. Callables and the ABI

### 11.1 Callable core

`Callable` is shared by `Kernel` (`.entry`) and `Func` (`.func`): name, linkage, params, body, attributes, pragmas, tuning directives, cluster directives. `Func` adds `Ret []*Param` and `NoReturn`.

`Linkage`: `Default` (no directive), `Visible`, `Extern`, `Weak`, `Common`.

* **V10** — a nil body requires `Extern`.
* **V11** — `Common` applies to variables only.

### 11.2 Parameters

| Constructor | Produces |
|---|---|
| `Param(name, t)` | `.param .u64 A` |
| `ParamPtr(name, t, space, align)` | `.param .u64 .ptr.global .align 8 A` |
| `ParamArray(name, align, n)` | `.param .align 8 .b8 A[64]` |

**Printer note:** for a plain parameter `.align` precedes the type; for a `.ptr` parameter the pointer's own `.align` follows the space. `Len` is ignored in the `.ptr` branch.

### 11.3 Tuning and cluster directives

Emitted between the signature and the body, zero values omitted:

`.maxntid`, `.reqntid` (from `Dim3`), `.minnctapersm`, `.maxnreg`, `.reqnctapercluster`, `.maxclusterrank`, `.explicitcluster`, `.attribute(...)`, `.pragma`.

* **W3** — cluster directives require `.target sm_90` or later.
* **W4** — cluster directives require `.version 8.0` or later.

### 11.4 Function-scope variables

* **V9** — function-scope variables MUST be `.local`, `.shared`, or `.param`.

### 11.5 The call sequence

`Invoke(fn, args, argTypes, rets)` emits the whole ABI dance as a nested `*Block`:

1. one `.param` variable per argument and per return value, declared in the block's local list;
2. `st.param.<t>` marshalling each argument;
3. `call.uni` with the parenthesised return group and argument group;
4. `ld.param.<t>` retrieving each result into the caller's registers.

The inner body shares the caller's `RegFile` and label set, so registers and labels remain unique across the boundary.

`Call` and `CallIndirect` emit the instruction alone; marshalling is the caller's job.

---

## 12. Memory Model *(as modelled)*

The package models memory ordering entirely through qualifier slots — there is no dependency analysis, no wait-counter concept, and no implicit fence insertion (P6).

* **Ordering** is `Sem`: `.weak`, `.relaxed`, `.acquire`, `.release`, `.acq_rel`, `.sc`, `.mmio`.
* **Scope** is `Scope`: `.cta`, `.cluster`, `.gpu`, `.sys`.
* **Cache behaviour** is `CacheOp` and `Evict`, and is a hint, not ordering.
* `Fence(q...)` takes proxy, sem, scope, and space; `Membar(q...)` takes a level.
* `Atom` returns a value; `Red` does not. Both require an `AtomOp` (**V24**, **V25**). `AtomCAS` takes two value operands, everything else one.

The relative order of `.sem` and `.scope` in the printed mnemonic is fixed by the table in §9.4 for every memory opcode: semantics first, then scope, then space.

---

## 13. Escape Hatches

PTX gains instructions faster than any model tracks them. `Emit` is the pressure valve:

```go
b.Emit("cp.async.ca.shared.global", []ptx.Operand{dst, src}, ptx.Imm(16)).If(p)
```

`Emit` builds an `Instr` with `Op = OpCustom` and the mnemonic held verbatim. Such instructions predicate, walk, verify, and print exactly like modelled ones. Qualifiers supplied to `Emit` print in the generic order (sem, scope, space, cache, vec); **anything needing a different order belongs in the mnemonic string.**

`Sym` is the operand-level equivalent: a bare symbol reference for names the package does not model. Prefer the typed handles from `Module.Var`, `Callable.Param`, `Body.Label`, and `Module.Add`.

Two limits, both deliberate:

* `Emit` sets no `Types`; type specifiers go in the mnemonic.
* **V14** does not fire for `OpCustom`, and no type-count check applies. An `Emit` mnemonic is trusted text — `ptxas` is the check.

---

## 14. Debug Information

`Module.File(name)` registers a source file and returns a 1-based `*File`; repeated names return the existing entry. `File.Timestamp` and `File.Size` print only when non-zero.

`Body.Loc(f, line, col)` attaches a `.loc` to the following instruction. `Body.LocInlined(...)` additionally carries `function_name` (a `*Label`, with optional `FuncOff`) and `inlined_at`.

`Module.Section(name, data)` emits a `.section` block of `.b8` payload, 16 bytes per line, for binary DWARF.

---

## 15. Canonical Text Form

The printer controls **layout only**. It makes no decisions about mnemonics or qualifier ordering (P4), so output is byte-stable for a given module.

Normative formatting rules:

1. Indentation is a literal **tab**, one level per nesting depth.
2. Instruction operands begin at **column 24** measured from the start of the mnemonic (including any predicate guard). If the head is 24 characters or longer, exactly one space separates it from the operands.
3. Operands print `Dst` first, then `Src`, comma-space separated, terminated by `;`.
4. Label definitions print at column 0.
5. Register classes print before named registers, both in declaration order, as `.reg <type padded to 6> %<prefix><Count+1>;`.
6. A blank line separates register declarations from local variables, and local variables from the item list, when both sides are non-empty.
7. Every module-scope declaration is preceded by a blank line.
8. `.file` entries print after the preamble, preceded by a blank line.
9. Integers print in signed decimal; unsigned immediates carry `U`; floats print exact-bits only.
10. Zero-valued tuning directives are omitted entirely.
11. Comments print only under `WithComments(true)`.

**Byte-stability (conformance):** for any module *m*, two calls to `Print(m)` yield identical bytes, and two structurally equal modules built with qualifiers supplied in different orders yield identical bytes.

`Print` returns an error only for modules that cannot produce valid text at all — a missing `.version` or an invalid `.address_size`. Everything else is `Verify`'s job.

---

## 16. Verification Rule Index

`Verify` returns `[]Diag`; each `Diag` carries a `Severity`, a `Where` (`"module"`, `"global lut"`, `"vadd[12]"`, `"vadd local tmp"`), and a message. It never mutates and never stops at the first finding.

| # | Rule | Severity | § |
|---|---|---|---|
| **V1** | `.version` is required | error | 3.1 |
| **V2** | `.address_size` is 32 or 64 | error | 4.3 |
| **V3** | Variable has a type | error | 6 |
| **V4** | Variable has a state space | error | 6 |
| **V5** | State-space sub-qualifier legal for its base space | error | 6.1 |
| **V6** | Variable `.align` is a power of two | error | 3.3 |
| **V7** | Variable vector width is 2 or 4 | error | 3.3 |
| **V8** | Register-space variables use `RegFile`, not `Var` | error | 7.1 |
| **V9** | Function-scope variables are `.local`, `.shared` or `.param` | error | 11.4 |
| **V10** | Bodiless callable requires `.extern` | error | 11.1 |
| **V11** | `.common` linkage applies to variables only | error | 11.1 |
| **V12** | Named registers unique within a body | error | 7.3 |
| **V13** | Each label bound at most once | error | 10.1 |
| **V14** | Opcode is modelled or `OpCustom` | error | 9.5 |
| **V15** | Guard predicate is a valid register | error | 10.2 |
| **V16** | Guard predicate has type `.pred` | error | 10.2 |
| **V17** | Type-specifier count matches the opcode's arity | error | 9.5 |
| **V18** | Type specifier names a real type | error | 5 |
| **V19** | Instruction vector width is 2, 4 or 8 | error | 8.3 |
| **V20** | Instruction state-space qualifier names a real space | error | 6 |
| **V21** | Rounding, `.approx` and `.full` are mutually exclusive | error | 9.6 |
| **V22** | `fma` carries an explicit rounding qualifier | error | 9.6 |
| **V23** | `cvta` carries a state space | error | 9.6 |
| **V24** | `atom` carries an operation qualifier | error | 9.6 |
| **V25** | `red` carries an operation qualifier | error | 9.6 |
| **V26** | `setp` carries a comparison qualifier | error | 9.6 |
| **V27** | `shf` carries a direction and `.clamp`/`.wrap` | error | 9.6 |
| **V28** | Branch targets bound in the same body | error | 10.1 |
| **W1** | `.blocksareclusters` requires `.version` 9.0+ | warning | 3.1 |
| **W2** | Family-specific target requires `.version` 8.8+ | warning | 4.1 |
| **W3** | Cluster directives require `sm_90`+ | warning | 11.3 |
| **W4** | Cluster directives require `.version` 8.0+ | warning | 11.3 |
| **W5** | Type postdates the declared `.version` | warning | 5 |
| **W6** | Opcode postdates the declared `.version` | warning | 9.5 |
| **W7** | Opcode postdates the declared `.target` | warning | 9.5 |

---

## 17. The Physical Line *(informative)*

```text
Go builder calls
   │
   ├─► ptx.Module ──┬─► Walk / WalkBody / Bodies   (analysis)
   │                ├─► Body edits                 (rewrite passes)
   │                ├─► ptx.Verify                 (V1–V28, W1–W7)
   │                └─► text.Print ──► .ptx text
   │                                     │
   │                                     ├─► ptxas ──► .cubin   (verifier of record)
   │                                     └─► CUDA driver JIT
   │
   └─(no parser: .ptx is emit-only)
```

`Walk(m, fn)` visits every instruction in source order, descending into nested `*Block`s; returning `false` stops the traversal. `Bodies(m)` returns every non-nil body. `Body.UsedAfter` and `Body.Defs` are conservative straight-line helpers with no control-flow analysis — a negative result from `UsedAfter` proves liveness only in straight-line code.

---

## 18. Worked Example

Builder:

```go
m := ptx.NewModule(ptx.ISA93, ptx.SM90, ptx.Addr64)

k := ptx.NewKernel("vector_add")
k.Linkage = ptx.Visible
pA, pB := k.Param("A", ptx.U64), k.Param("B", ptx.U64)
pC, pN := k.Param("C", ptx.U64), k.Param("n", ptx.U32)

b := k.Body
r := b.Regs
i, n, p := r.New(ptx.U32), r.New(ptx.U32), r.New(ptx.Pred)
a, bb, c := r.New(ptx.U64), r.New(ptx.U64), r.New(ptx.U64)
off := r.New(ptx.U64)
va, vb := r.New(ptx.F32), r.New(ptx.F32)
done := b.Label("done")

b.MovSReg(i, ptx.CtaIdX)
b.Mad(ptx.U32, i, i, ptx.NTidX, ptx.TidX, ptx.MulLo)
b.Ld(ptx.U32, n, ptx.At(pN), ptx.ParamSpace)
b.Setp(ptx.U32, ptx.Ge, p, i, n)
b.Bra(done).If(p)
b.Mul(ptx.U32, off, i, ptx.Imm(4), ptx.MulWide)
b.Ld(ptx.U64, a, ptx.At(pA), ptx.ParamSpace)
b.Ld(ptx.U64, bb, ptx.At(pB), ptx.ParamSpace)
b.Ld(ptx.U64, c, ptx.At(pC), ptx.ParamSpace)
b.Add(ptx.U64, a, a, off)
b.Add(ptx.U64, bb, bb, off)
b.Add(ptx.U64, c, c, off)
b.Ld(ptx.F32, va, ptx.At(a), ptx.Global)
b.Ld(ptx.F32, vb, ptx.At(bb), ptx.Global)
b.Add(ptx.F32, va, va, vb)
b.St(ptx.F32, ptx.At(c), va, ptx.Global)
b.Bind(done)
b.Ret()

m.Add(k)
```

Emitted text (tabs shown expanded; operands at column 24):

```text
//
// Generated by vertex
//
.version 9.3
.target sm_90
.address_size 64

.visible .entry vector_add(
	.param .u64 A,
	.param .u64 B,
	.param .u64 C,
	.param .u32 n
)
{
	.reg .u32   %r<3>;
	.reg .pred  %p<2>;
	.reg .u64   %rd<5>;
	.reg .f32   %f<3>;

	mov.u32                 %r1, %ctaid.x;
	mad.lo.u32              %r1, %r1, %ntid.x, %tid.x;
	ld.param.u32            %r2, [n];
	setp.ge.u32             %p1, %r1, %r2;
	@%p1 bra                done;
	mul.wide.u32            %rd4, %r1, 4;
	ld.param.u64            %rd1, [A];
	ld.param.u64            %rd2, [B];
	ld.param.u64            %rd3, [C];
	add.u64                 %rd1, %rd1, %rd4;
	add.u64                 %rd2, %rd2, %rd4;
	add.u64                 %rd3, %rd3, %rd4;
	ld.global.f32           %f1, [%rd1];
	ld.global.f32           %f2, [%rd2];
	add.f32                 %f1, %f1, %f2;
	st.global.f32           [%rd3], %f1;
done:
	ret;
}
```

Note the three declared-but-unused zero indices (`%r0`, `%p0`, `%rd0`, `%f0`) implied by the `Count+1` rule in §15.5, and that `.pred` pads to six characters where `.u32` pads to six with two trailing spaces.

---

## 19. Known Gaps and Deferred Work

### 19.1 Discrepancies between code and its own documentation

1. **`Verify` does not descend into `*Block`.** `verifyCallable` iterates `Body.items` only, so every instruction produced by `Invoke` — the whole call sequence — is unverified, and labels bound inside a block do not enter the `bound` set. `Walk` descends; `Verify` should too.
2. **`RegFile.Named` does not detect collisions with generated names.** The doc comment promises it; the code checks only the `names` map, which generated registers never enter. `Named(U32, "r1")` silently collides with `%r1`.
3. **`EnvReg(n)` is a stub** returning `NoSReg`, with a comment pointing at a non-existent `EnvRegOf`. `Env(n)` is the working constructor; `EnvReg` should be deleted.
4. **`.vN` is unreachable from `Emit`.** `Q.Vec` is derived from `VecOp` arity inside `b.inst`, which `Emit` bypasses, and `vecQual` is unexported with no exported `Qual` setting `Vec`. An emitted vector instruction must spell `.v4` into its mnemonic string.

### 19.2 Modelling gaps

5. **`BrxIdx` and `CallIndirect` targets are unchecked.** **V28** scans `Src` for `*Label`; `.branchtargets` labels live inside a `*BranchTargets` and `.calltargets` functions inside a `*CallTargets`, so neither is validated for binding or for existence.
6. **`Verify` ignores `*Proto`, `*Alias`, and `*Section`.** A `.alias` to a bodiless `*Func`, or a prototype whose arity disagrees with its call site, passes.
7. **`Body.UsedAfter` does not look inside `group` operands.** A value passed only as a call argument can be reported dead.
8. **Version constants are split across three files** — `version.go`, `op.go` (`ISA60`, `ISA62`, `ISA63`, and the function-shaped `ISA61()`/`ISAetc`), `sregs.go` (`ISA41`). Consolidating means touching only the table that referenced them.
9. **`locDir` always prints a column,** even when zero. PTX permits omitting it.
10. **`paramDecl` is inconsistent about `.align`** — before the type for plain parameters, after the space for `.ptr` parameters — and ignores `Len` entirely in the `.ptr` branch.

### 19.3 Deferred

11. **No parser.** Round-tripping is out of scope by design (§0.4); if it is ever wanted, §15 is the specification it must satisfy.
12. **No modelled opcodes** for `mma`, `wgmma`, `cp.async`, `mbarrier`, `tensormap`, `tex`, `surf`, or `wmma`. All are reachable through `Emit`. The `Tex` state space is retained only for compatibility.
13. **Three-type-specifier opcodes** cannot be modelled while `Instr.Types` is `[2]Type`.
14. **`Addr32` remains accepted** by `Verify` and `Print` although every modelled target is 64-bit. Restricting it is a one-line change whenever a 32-bit target is ruled out for good.