# ir/vir

[github.com/vertex-language/vvm/ir/vir](https://github.com/vertex-language/vvm/ir/vir)

The Vertex IR data model: the in-memory representation of a verified program, a construction API for building one, and the verifier that enforces every invariant the rest of the pipeline is allowed to assume. Every other package in the repository either produces a `vir.Module` or consumes one.

---

## Import path

```go
import "github.com/vertex-language/vvm/ir/vir"
```

---

## Package layout

* **`module.go`**: Module (incl. module-wide AsmDialect field and Namespace), Target, Struct, FunctionSignature, Constant, Global, ConstInit variants, Link/LinkKind, ExternGroup/ExternFunction, Import, Param, FunctionAttribute, Function (incl. `EntryFunction`), Block, Instruction, Terminator variants, and inline-asm data types (AsmDialect, AsmBlock, AsmBinding, AsmOperand, AsmCodeLine).
* **`opcode.go`**: Opcode — the closed, spec-fixed §4 instruction vocabulary as a Go enum, plus opTable (the single source of truth for each opcode's arity/operand-type-constraint/result-rule), String()/ParseOpcode, and an init()-time completeness check.
* **`types.go`**: Type interface: IntType, FloatType, PtrType, VoidType, VecType, StructType, ArrayType, ValistType; Equal, IsInt/IsFloat/.../IsValueType.
* **`operand.go`**: Operand, OperandKind, and constructors (Ident, QualifiedIdent, IntLiteral, FloatLiteral, StringLiteral, BoolLiteral, NullLiteral, TypeOperand, OrderingOperand, VectorLiteral).
* **`float.go`**: formatFloat — canonical float-literal text formatting.
* **`targets.go`**: Canonical arch/OS/ABI vocabularies, rejected-alias tables, PointerBits, BinFormat/FormatOf, per-arch asm-dialect legality (DialectsForArchitecture), and populated register tables for standard architectures (x86, ARM/AArch64, RISC-V, PowerPC, MIPS, LoongArch, s390x).
* **`mangle.go`**: MangledSymbol — computes the ABI-visible symbol for an exported fn/global (§6.3): a bare symbol under `entry`/`extern_c` or when no namespace is declared, otherwise a length-prefixed Itanium-style mangling of namespace + module + export name.
* **`linkfile.go`**: DeriveLinkFile — computes the on-disk filename a `Link` implies for a given `BinFormat`, per the short-name/exact-name rules `Verify` itself enforces (§7.2/§7.4). Exported so build-layer code (`vvm`'s `dispatch.go`) can resolve the same filename without re-implementing the derivation.
* **`vmeta.go`**: ModuleShape and its per-kind Shape types (StructShape, ConstShape, FnSigShape, FnShape, GlobalShape) — the export-tagged summary a module's `.vmeta` would carry (Stage 0 extraction, §7.3), plus `ExtractShape` to produce one from a `Module`.
* **`builder.go`**: NewModule + FunctionBuilder + AsmBuilder — construction API that mirrors the IR structure without performing checks.
* **`verify.go`**: Verify / VerifyWithImports — the single place invariants are enforced, running passes over module sections and function bodies, with optional Stage A cross-module import checking.

---

## Design: construct, then check

`builder.go` and `verify.go` never overlap. The builder mirrors the IR one-to-one and only appends; `Verify` is the only place that validates anything. A `Module` built by hand, or produced by a decoder, is only as trustworthy as the last call to `Verify` made on it — this package does not track verification state on the value itself.

```go
m := vir.NewModule("add_example")
m.SetTarget("x86_64", "linux", "gnu")

fmtGlobal := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 14},
    vir.InitByteString{Data: []byte("%d + %d = %d\n\x00")})

fb := m.DeclareFunction("main", nil, vir.I32, true)
sum := fb.Add("sum", vir.I32, vir.IntLiteral(7), vir.IntLiteral(35))
fb.Call("r", "printf", vir.Ident(fmtGlobal.Name), vir.IntLiteral(7), vir.IntLiteral(35), sum)
fb.Return(vir.IntLiteral(0))

if err := vir.Verify(m); err != nil {
    panic(err)
}
```

Nothing above validated anything — `Verify` is the first point at which name collisions, type mismatches, or malformed control flow would surface.

---

## Core concepts

### `Opcode`

The §4 instruction vocabulary is closed and spec-fixed, so it's a Go enum (`Opcode`, `opcode.go`), not a string.

```go
vir.OpAdd     // add
vir.OpCtlz    // ctlz
vir.OpFma     // fma

vir.OpAdd.String()        // "add"
vir.ParseOpcode("ctlz")   // (vir.OpCtlz, true) — used by decoders
```

Every `Opcode` constant is registered exactly once in `opTable`, which pairs it with its operand-count and operand-type-constraint (§9.18) and how its result type is computed. `init()` panics at package load if a constant is missing an entry. A newly added opcode cannot silently skip verification the way a name absent from a hand-maintained `map[string]bool` could.

This is deliberately *not* used for everything textual in the package: struct/field/label/link/function names stay `string` (they're open, user-chosen identifiers). Inline-asm mnemonics/registers (`AsmCodeLine.Mnemonic`, `AsmBinding.Register`) stay `string` too, because §4 defines those as open, per-architecture *data tables*, not a fixed vocabulary the verifier reasons about the way it does core opcodes.

### `Type`

One interface, eight implementations. `IsAggregate`/`IsValueType` distinguish memory-only types (`StructType`, `ArrayType`) from types that may name a value. `ValistType` is deliberately excluded from `IsValueType` too — it's legal only as an `alloca` result and a `va_start`/`va_arg`/`va_end` operand, checked structurally at those call sites rather than as a general-purpose type.

```go
vir.I32                 // IntType{32}
vir.F64                 // FloatType{64}
vir.Ptr                 // PtrType{}
vir.Valist              // ValistType{} — opaque, target-defined-layout varargs cursor (§4.5)
vir.VecType{Elem: vir.I32, Len: 4}
vir.ArrayType{Elem: vir.I8, Len: 14}
vir.StructType{Name: "Point"}

vir.Equal(vir.I32, vir.IntType{Bits: 32}) // true — structural equality
```

`StructType` equality is nominal per-origin: two `StructType`s with the same `Name` but different `Import` paths are never `Equal` — cross-module struct identity isn't safe to compare by spelling alone (§7.4).

### `Operand`

A tagged union covering every position an operand can appear in: idents (plain or import-qualified), literals, `null`, a type used in operand position (`index.ptr`), atomic orderings, and vector literals.

```go
vir.Ident("x")                        // ident
vir.QualifiedIdent("mathlib", "add")  // cross-module ident (§7.3), prints "mathlib.add"
vir.IntLiteral(42)                    // integer literal
vir.FloatLiteral(3.14)                // float literal
vir.BoolLiteral(true)                 // bool literal
vir.NullLiteral()                     // null
vir.TypeOperand(vir.I32)              // type-in-operand-position
vir.OrderingOperand("acquire")        // atomic ordering
vir.VectorLiteral(0, 4, 1, 5)         // shuffle mask / vector const
```

### Instructions and terminators

`Instruction.Op` is an `Opcode`; exactly one of `Instruction.Suffix` (a `Type`) or `Instruction.Sig` (an `fnsig` name, for indirect `call`/`tailcall`, or the self-referential token for `va_start`) may be set — the `<op>.<suffix>` split is structural, not string-parsed downstream. Terminators are a separate interface from instructions, so "exactly one terminator, nothing after it" doesn't need to be checked by scanning:

```go
type Terminator interface{ isTerm() }
// Branch, BranchIf, Switch, Return, TailCall, Trap, Unreachable

func Successors(t Terminator) []string // labels a terminator may transfer to
```

### Variadic functions and `valist` (§4.5)

A function's param-list can end in `...` (`FunctionBuilder.SetVariadic`), enabling `va_start`/`va_arg`/`va_end` in the body. The cursor itself is an opaque `alloca.valist` result — `AllocaValist` is the sole legal way to create one, since its layout is target-defined and not something a frontend sizes:

```go
fb.SetVariadic()
cursor := fb.AllocaValist("ap")
fb.VaStart("main", cursor.String(), "lastParam")
v := fb.VaArg("v", vir.I32, cursor)
fb.VaEnd(cursor)
```

`Verify` tracks valist lifetimes as a pair of dataflow analyses per function (§4.5): a "must" forward analysis confirms every `va_arg`/`va_end` is preceded by `va_start` on *every* incoming path, and a "may" forward analysis rejects re-`va_start`-ing a valist that might already be open on *any* path without an intervening `va_end`. A valist left possibly open across a `return` is also rejected.

### Inline assembly

An `AsmBlock` is an ordinary body-line (`BodyLine.Asm`), never a terminator. It carries a list of `AsmBinding`s (`in`/`out`/`clobber`) and a list of `AsmCodeLine`s (mnemonic instructions or block-scoped label declarations). It does **not** carry its own dialect — the syntax governing its `code:` section comes from the enclosing module's `AsmDialect`, set once via `Module.SetAsmDialect`.

`AsmBuilder` accumulates a block via `BeginAsm`/`In`/`Out`/`Clobber`/`Code`, and `End` appends the finished block to the enclosing function's current basic block:

```go
m.SetAsmDialect(vir.DialectIntel)

fb.BeginAsm().
    In("rdi", "exitCode").
    Clobber("rcx", "r11").
    Code(
        vir.AsmInstructionLine("mov", vir.AsmRegister("rax"), vir.AsmImmediate(vir.IntLiteral(60))),
        vir.AsmInstructionLine("syscall"),
    ).
    End()
```

### Namespaces and symbol mangling (§6.3)

`Module.SetNamespace` gates mangling for exported fn/global symbols. `MangledSymbol` (`mangle.go`) computes the ABI-visible name: `entry` and `extern_c` always force a bare symbol regardless of namespace; otherwise a module with no declared namespace also gets a bare symbol, and a namespaced module gets a length-prefixed Itanium-style encoding of `namespace/module/exportName`.

### Cross-module imports (§7.3)

Verification of imports is split into stages, only the first two of which are this package's job:

* **Stage 0 (Extraction)** — `ExtractShape` (`vmeta.go`) pulls every export-tagged struct/const/fnsig/fn-and-global *signature* out of a `Module` into a `ModuleShape` — deliberately omitting fn bodies and global initializers. This is what a module's own `.vmeta` would carry.
* **Stage A (Provisional)** — `VerifyWithImports` checks a module's qualified references (`field.ptr`, `call` to a `QualifiedIdent`, aggregate initializers naming an imported struct, ...) against a caller-supplied `map[string]*ModuleShape`, keyed by import path, as if each were locally declared.
* **Stage B (Structural)** — checking the Stage A assumption against the real compiled exporter is `vvm`'s job at build-orchestration time and out of scope for this package.

```go
shapes := map[string]*vir.ModuleShape{
    "mathlib": vir.ExtractShape(mathlibModule),
}
if err := vir.VerifyWithImports(m, shapes); err != nil {
    // ...
}
```

`Verify(m)` is just `VerifyWithImports(m, nil)` — every qualified reference then fails as unresolved, since no shapes were supplied.

---

## `Verify` — what gets checked

`Verify` / `VerifyWithImports` run one forward pass over module-level sections, then a per-function pass:

```go
func Verify(m *Module) error
func VerifyWithImports(m *Module, shapes map[string]*ModuleShape) error
```

* **Target** — Arch/OS/ABI must be canonical; a recognized alias (`amd64`, `arm64`, `darwin`, ...) fails with the canonical spelling named in the error, not silently rewritten.
* **Module-wide asm dialect** — If any function contains an asm block, the module must have a `Target` and an `AsmDialect`. That dialect must be valid for the target's architecture (`IsDialectValidForArchitecture`).
* **Name resolution** — The module shares one flat namespace across structs, fnsigs, consts, globals, externs, functions, and block labels. Redeclaring a name, or using a reserved keyword, fails immediately.
* **Per-section checks** — Struct fields, fnsig signatures, const/global initializers, link-to-extern-group correspondence, and filename derivation per target `BinFormat` (`DeriveLinkFile`) are verified.
* **Imports** — Each `Import.Path` must be non-empty and declared at most once; under `VerifyWithImports`, every declared path must have a corresponding supplied `ModuleShape`.
* **Function attributes** — At most one function may carry `entry`. `entry` and `extern_c` are mutually exclusive on the same function. Any function carrying `entry` must be exported, must not have `byval`/`sret` parameters, and must not also carry `noreturn`.
* **TLS restrictions** — TLS on `os=none` requires the module's target feature tiers to include a TLS-capable tier (e.g., `tls_support`).
* **Per-function body shape** — Every block must terminate, every label must be both defined and referenced, and `Successors` must resolve successfully.
* **Per-instruction shape** — For every opcode, its registered operand count and operand-type constraint (§9.18) are checked generically off `opTable`. Opcode-specific structural checks are also enforced, such as `syscall` operand count/typing (§9.33), bulk-memory `len`/byte typing (§9.27), `bswap` on `i8` rejection (§9.20), and atomic ordering legality/no-align (§9.25–26).
* **Type fixation** — Each value's type is computed once (`resultType`) and locked in at first assignment; a later assignment with a different type is rejected.
* **Definite assignment** — A forward must-analysis (`in`/`out` sets per block, meet-over-predecessors) confirms every read is preceded by an assignment on every path. Asm `in` bindings are treated as reads and `out` bindings as assignments.
* **Valist lifetimes (§4.5)** — Separate must/may forward analyses confirm every `va_arg`/`va_end` is preceded by `va_start` on every path, reject a possible re-`va_start` without an intervening `va_end`, and reject a valist left possibly open across a `return`. `va_start` itself is checked structurally against the enclosing function: it must be variadic, must have a named parameter to anchor to, and its `last_named` operand must name that function's actual final declared parameter.
* **Tailcall restrictions (§9.29, §4.2)** — A `tailcall`'s callee/fnsig must return the same type as the caller and must not carry `byval`/`sret` parameters. A tailcall from a variadic caller to a variadic-fnsig callee is rejected outright, since a live save area can't safely survive frame reuse.
* **Inline assembly structure** — Register-table membership and width agreement for every binding, binding well-formedness (no duplicate `in`, no split-ident `out`, no register both `clobber` and `out`), and asm-local label scoping (`checkAsmBlockStructure`) are verified.

```go
if err := vir.Verify(m); err != nil {
    // err names the exact rule violated, e.g.:
    // "ctlz legal only on iN / vec[iN, W] (§9.18)"
}
```

### Known gaps

A handful of obligations are intentionally incomplete, each marked `TODO` at its call site rather than silently skipped:

* Feature-tier tables — `Target.Tiers` entries aren't fully validated against per-target tier data yet.
* Deep per-opcode operand-*shape* unification beyond the type-suffix constraint (e.g., confirming both operands of `add` are the same width as each other) (§9.16).
* Shuffle-mask bounds checking for vector literals (§9.31).
* Exact operand counts for `splat`/`extract`/`insert`/reductions/`prefetch` — §4 doesn't pin these in the spec text, so `opTable` leaves them unchecked rather than inventing an arity.
* Full per-dialect mnemonic/operand-shape validation for asm lines (§9.38) — only arity/label-scoping is checked structurally today.
* Full single-entry/single-exit control-flow validation for asm blocks beyond label-reference scoping (§9.40).
* Barrier/fence semantics for asm blocks are a codegen concern and not independently verifier-checkable (§9.41).
* Stage B structural verification of imports against the real compiled exporter — out of scope for this package; that's `vvm`'s job at build-orchestration time (§7.3/§7.4).

---

## Design notes

**Nothing here is a serialization format.** `ir/vir` has no `[]byte` in or out — that's `format/`'s job entirely. This package only ever holds the in-memory shape and the rules it must satisfy.

**Verification is centralized on purpose.** Every downstream package is written under the assumption that whatever `Module` it receives already passed `Verify`.

**The builder never second-guesses you.** `DeclareFunction`, `Emit`, `Branch`, and friends all just append to the structure. Nothing calls `Verify` for you implicitly.

**Closed vocabularies are enums; open vocabularies are data.** `Opcode` covers §4's fixed instruction set and is a Go type the compiler and `opcode.go`'s `init()` both help keep exhaustive. Struct/field/label/link names and asm mnemonics/registers stay `string` because they're genuinely open — a new struct field or a new x86 mnemonic is data, not a new case the verifier needs to be taught by hand.

**Dialect is a module property, not a block property.** `AsmBuilder.BeginAsm` takes no dialect argument and `AsmBlock` has no dialect field of its own; every asm block in a module is interpreted under whatever single dialect `Module.SetAsmDialect` established.

**Import verification is deliberately staged.** This package only ever trusts a caller-supplied `ModuleShape` (Stage A); it never re-derives or re-verifies another module's own internals. Anything requiring the *actual* compiled exporter (Stage B) belongs to `vvm`, not here.