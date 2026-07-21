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

* **`module.go`**: Module (incl. module-wide AsmDialect field), Target, Struct, FunctionSignature, Constant, Global, ConstInit variants, Link, ExternGroup/ExternFunction, Function, Block, Instruction, Terminator variants, and inline-asm data types (AsmDialect, AsmBlock, AsmBinding, AsmOperand, AsmCodeLine).


* **`opcode.go`**: Opcode — the closed, spec-fixed §4 instruction vocabulary as a Go enum, plus opTable (the single source of truth for each opcode's arity/operand-type-constraint/result-rule), String()/ParseOpcode, and an init()-time completeness check.


* **`types.go`**: Type interface: IntType, FloatType, PtrType, VoidType, VecType, StructType, ArrayType; Equal, IsInt/IsFloat/.../IsValueType.


* **`operand.go`**: Operand, OperandKind, and constructors (Ident, IntLiteral, FloatLiteral, StringLiteral, BoolLiteral, NullLiteral, TypeOperand, OrderingOperand, VectorLiteral).


* **`float.go`**: formatFloat — canonical float-literal text formatting.


* **`targets.go`**: Canonical arch/OS/ABI vocabularies, rejected-alias tables, PointerBits, BinFormat/FormatOf, per-arch asm-dialect legality (DialectsForArchitecture), and populated register tables for standard architectures (x86, ARM/AArch64, RISC-V, PowerPC, MIPS, LoongArch, s390x).


* **`builder.go`**: NewModule + FunctionBuilder + AsmBuilder — construction API that mirrors the IR structure without performing checks.


* **`verify.go`**: Verify — the single place invariants are enforced, running passes over module sections and function bodies.



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

One interface, seven implementations. `IsAggregate`/`IsValueType` distinguish memory-only types (`StructType`, `ArrayType`) from types that may name a value.

```go
vir.I32                 // IntType{32}
vir.F64                 // FloatType{64}
vir.Ptr                 // PtrType{}
vir.VecType{Elem: vir.I32, Len: 4}
vir.ArrayType{Elem: vir.I8, Len: 14}
vir.StructType{Name: "Point"}

vir.Equal(vir.I32, vir.IntType{Bits: 32}) // true — structural equality

```

### `Operand`

A tagged union covering every position an operand can appear in: idents, literals, `null`, a type used in operand position (`index.ptr`), atomic orderings, and vector literals.

```go
vir.Ident("x")                 // ident
vir.IntLiteral(42)             // integer literal
vir.FloatLiteral(3.14)         // float literal
vir.BoolLiteral(true)          // bool literal
vir.NullLiteral()              // null
vir.TypeOperand(vir.I32)       // type-in-operand-position
vir.OrderingOperand("acquire") // atomic ordering
vir.VectorLiteral(0, 4, 1, 5)  // shuffle mask / vector const

```

### Instructions and terminators

`Instruction.Op` is an `Opcode`; exactly one of `Instruction.Suffix` (a `Type`) or `Instruction.Sig` (an `fnsig` name, for indirect `call`/`tailcall`) may be set — the `<op>.<suffix>` split is structural, not string-parsed downstream. Terminators are a separate interface from instructions, so "exactly one terminator, nothing after it" doesn't need to be checked by scanning:

```go
type Terminator interface{ isTerm() }
// Branch, BranchIf, Switch, Return, TailCall, Trap, Unreachable

func Successors(t Terminator) []string // labels a terminator may transfer to

```

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

---

## `Verify` — what gets checked

`Verify` runs one forward pass over module-level sections, then a per-function pass:

```go
func Verify(m *Module) error

```

* **Target** — Arch/OS/ABI must be canonical; a recognized alias (`amd64`, `arm64`, `darwin`, ...) fails with the canonical spelling named in the error, not silently rewritten.


* **Module-wide asm dialect** — If any function contains an asm block, the module must have a `Target` and an `AsmDialect`. That dialect must be valid for the target's architecture (`IsDialectValidForArchitecture`).


* **Name resolution** — The module shares one flat namespace across structs, fnsigs, consts, globals, externs, functions, and block labels. Redeclaring a name, or using a reserved keyword, fails immediately.


* **Per-section checks** — Struct fields, fnsig signatures, const/global initializers, link-to-extern-group correspondence, and filename derivation per target `BinFormat` are verified.


* **Function Attributes** — At most one function may carry the `entry` attribute. Any function carrying `entry` must be exported, must not have `byval` or `sret` parameters, and must not carry the `noreturn` attribute.


* **TLS Restrictions** — TLS on `os=none` requires the module's target feature tiers to include a TLS-capable tier (e.g., `tls_support`).


* **Per-function body shape** — Every block must terminate, every label must be both defined and referenced, and `Successors` must resolve successfully.


* **Per-instruction shape** — For every opcode, its registered operand count and operand-type constraint (§9.18) are checked generically off `opTable`. Opcode-specific structural checks are also enforced, such as `syscall` operand count/typing (§9.33), bulk-memory `len`/byte typing (§9.27), `bswap` on `i8` rejection (§9.20), and atomic ordering legality/no-align (§9.25–26).


* **Type fixation** — Each value's type is computed once (`resultType`) and locked in at first assignment; a later assignment with a different type is rejected.


* **Definite assignment** — A forward must-analysis (`in`/`out` sets per block, meet-over-predecessors) confirms every read is preceded by an assignment on every path. Asm `in` bindings are treated as reads and `out` bindings as assignments.


* **Inline assembly structure** — Register-table membership and width agreement for every binding, binding well-formedness, and asm-local label scoping (`checkAsmBlockStructure`) are verified.



```go
if err := vir.Verify(m); err != nil {
    // err names the exact rule violated, e.g.:
    // "ctlz legal only on iN / vec[iN, W] (§9.18)"
}

```

### Known gaps

A handful of obligations are intentionally incomplete, each marked `TODO` at its call site rather than silently skipped:

* Feature-tier tables — `Target.Tiers` entries aren't fully validated against per-target tier data yet.


* Deep per-opcode operand-*shape* unification beyond the type-suffix constraint (e.g., confirming both operands of `add` are the same width as each other).


* The `noreturn`→`unreachable` adjacency rule.


* Shuffle-mask bounds checking for vector literals.


* Exact operand counts for `splat`/`extract`/`insert`/reductions/`prefetch` — §4 doesn't pin these in the spec text, so `opTable` leaves them unchecked rather than inventing an arity.


* Full per-dialect mnemonic/operand-shape validation for asm lines (§9.38) — only arity/label-scoping is checked structurally today.


* Full single-entry/single-exit control-flow validation for asm blocks beyond label-reference scoping (§9.40).


* Barrier/fence semantics for asm blocks are a codegen concern and not independently verifier-checkable (§9.41).



---

## Design notes

**Nothing here is a serialization format.** `ir/vir` has no `[]byte` in or out — that's `format/`'s job entirely. This package only ever holds the in-memory shape and the rules it must satisfy.

**Verification is centralized on purpose.** Every downstream package is written under the assumption that whatever `Module` it receives already passed `Verify`.

**The builder never second-guesses you.** `DeclareFunction`, `Emit`, `Branch`, and friends all just append to the structure. Nothing calls `Verify` for you implicitly.

**Closed vocabularies are enums; open vocabularies are data.** `Opcode` covers §4's fixed instruction set and is a Go type the compiler and `opcode.go`'s `init()` both help keep exhaustive. Struct/field/label/link names and asm mnemonics/registers stay `string` because they're genuinely open — a new struct field or a new x86 mnemonic is data, not a new case the verifier needs to be taught by hand.

**Dialect is a module property, not a block property.** `AsmBuilder.BeginAsm` takes no dialect argument and `AsmBlock` has no dialect field of its own; every asm block in a module is interpreted under whatever single dialect `Module.SetAsmDialect` established.