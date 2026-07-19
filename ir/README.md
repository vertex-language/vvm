# ir/vir

`github.com/vertex-language/vvm/ir/vir`

The Vertex IR data model: the in-memory representation of a verified program, a construction API for building one, and the verifier that enforces every invariant the rest of the pipeline is allowed to assume. Every other package in the repository either produces a `vir.Module` or consumes one.

---

## Import path

```go
import "github.com/vertex-language/vvm/ir/vir"
```

---

## Package layout

```
ir/vir/
├── module.go     Module (incl. module-wide AsmDialect field), Target, Struct,
│                 FunctionSignature, Constant, Global, ConstInit variants,
│                 Link, ExternGroup/ExternFunction, Function, Block,
│                 Instruction, Terminator variants, the inline-asm data types
│                 (AsmDialect, AsmBlock, AsmBinding, AsmOperand, AsmCodeLine, ...)
├── types.go      Type interface: IntType, FloatType, PtrType, VoidType, VecType,
│                 StructType, ArrayType; Equal, IsInt/IsFloat/.../IsValueType
├── operand.go    Operand, OperandKind, constructors (Ident, IntLiteral, FloatLiteral,
│                 StringLiteral, BoolLiteral, NullLiteral, TypeOperand,
│                 OrderingOperand, VectorLiteral)
├── float.go      formatFloat — canonical float-literal text
├── targets.go    canonical arch/OS/ABI vocabularies, rejected-alias tables,
│                 PointerBits, BinFormat/FormatOf, per-arch asm-dialect
│                 legality (DialectsForArchitecture) and register tables
│                 (X86RegisterTable, AArch64RegisterTable, ARMRegisterTable)
├── builder.go    NewModule + FunctionBuilder + AsmBuilder — construction API,
│                 never checks
└── verify.go     Verify — the single place invariants are enforced
```

---

## Design: construct, then check

`builder.go` and `verify.go` never overlap. The builder mirrors the IR one-to-one and only appends; `Verify` is the only place that validates anything. A `Module` built by hand, or produced by a decoder, is only as trustworthy as the last call to `Verify` made on it — this package does not track verification state on the value itself.

```go
m := vir.NewModule("add_example")
m.SetTarget("x86_64", "linux", "gnu")

fmtGlobal := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 14},
    vir.InitByteString{Data: []byte("%d + %d = %d\n\x00")})

fb := m.DeclareFunction("main", nil, vir.I32, true)
a := fb.Emit("a", "mov", vir.I32, vir.IntLiteral(7))
b := fb.Emit("b", "mov", vir.I32, vir.IntLiteral(35))
sum := fb.Add("sum", vir.I32, a, b)
fb.Call("r", "printf", vir.Ident(fmtGlobal.Name), a, b, sum)
fb.Return(vir.IntLiteral(0))

if err := vir.Verify(m); err != nil {
    panic(err)
}
```

Nothing above validated anything — `Verify` is the first point at which name collisions, type mismatches, or malformed control flow would surface.

---

## Core concepts

### `Module`

Field order mirrors the mandatory section order a `.vir` file must follow: `Target`, `AsmDialect`, `Structs`, `FunctionSignatures`, `Constants`, `Globals`, `Links`, `Externs`, `Functions`. Nothing downstream (`format/`, `lower/`, `object/`, `objectfile/`, `objectwriter/`) is allowed to touch an unverified `Module`.

```go
type Module struct {
    Name               string
    Target             *Target     // nil for pure-compute modules
    AsmDialect         *AsmDialect // nil unless declared; module-wide asm syntax dialect
    Structs            []*Struct
    FunctionSignatures []*FunctionSignature
    Constants          []*Constant
    Globals            []*Global
    Links              []*Link
    Externs            []*ExternGroup
    Functions          []*Function
}
```

`AsmDialect` is set once per module via `Module.SetAsmDialect` — it is required if the module contains any inline-asm blocks, and governs every asm block in every function; there is no per-block override.

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
vir.Ident("x")             // ident
vir.IntLiteral(42)         // integer literal
vir.FloatLiteral(3.14)     // float literal
vir.BoolLiteral(true)      // bool literal
vir.NullLiteral()          // null
vir.TypeOperand(vir.I32)   // type-in-operand-position
vir.OrderingOperand("acquire") // atomic ordering
vir.VectorLiteral(0, 4, 1, 5)  // shuffle mask / vector const
```

### Instructions and terminators

`Instruction.Op` holds the bare mnemonic; exactly one of `Instruction.Suffix` (a `Type`) or `Instruction.Sig` (an `fnsig` name, for indirect `call`/`tailcall`) may be set — the `<op>.<suffix>` split is structural, not string-parsed downstream. Terminators are a separate interface from instructions, so "exactly one terminator, nothing after it" doesn't need to be checked by scanning:

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

1. **Target** — arch/OS/ABI must be canonical; a recognized alias (`amd64`, `arm64`, `darwin`, ...) fails with the canonical spelling named in the error, not silently rewritten.
2. **Module-wide asm dialect** — if any function contains an asm block, the module must have a `Target` and an `AsmDialect`, and that dialect must be valid for the target's architecture (`IsDialectValidForArchitecture`). Checked once at module scope, not per block. If the module declares an `AsmDialect` but has no asm blocks, the dialect/architecture pairing is still validated whenever a `Target` is present.
3. **Name resolution** — one flat namespace across structs, fnsigs, consts, globals, externs, functions, and block labels. Redeclaring a name, or using a reserved keyword, fails immediately with the conflicting kind named.
4. **Per-section checks** — struct fields, fnsig signatures, const/global initializers (`checkInit` walks `ConstInit` recursively against the declared type), link-to-extern-group correspondence, filename derivation per target `BinFormat`.
5. **Per-function body shape** — every block terminates, every label is both defined and referenced, `Successors` resolves.
6. **Type fixation** — each value's type is computed once (`resultType`) and locked in at first assignment; a later assignment with a different type is rejected. Asm `out` bindings participate in the same fixation pass: a first-seen `out` ident's type is inferred from its bound register's width.
7. **Definite assignment** — a forward must-analysis (`in`/`out` sets per block, meet-over-predecessors) confirms every read is preceded by an assignment on every path. Asm `in` bindings are treated as reads and `out` bindings as assignments in this same analysis.
8. **Inline assembly structure** — register-table membership and width agreement for every binding, binding well-formedness (no duplicate `in` per register, no split `out` per register, no register both clobbered and `out`), and asm-local label scoping (`checkAsmBlockStructure`). Dialect/architecture legality itself is checked once at module scope in step 2, not per block.

```go
if err := vir.Verify(m); err != nil {
    // err names the exact rule violated, e.g.:
    // "value \"sum\" assigned as i64 here but fixed as i32 at first
    //  assignment (§5 rule 2)"
}
```

### Known gaps

A handful of obligations are intentionally incomplete, each marked `TODO` at its call site rather than silently skipped:

- Feature-tier tables — `Target.Tiers` entries aren't validated against per-target tier data yet.
- Deep per-opcode operand-type unification against an instruction's suffix.
- The `noreturn`→`unreachable` adjacency rule.
- Shuffle-mask bounds checking for vector literals.
- Full per-dialect mnemonic/operand-shape validation for asm lines (§9.38) — only arity/label-scoping is checked structurally today.
- Full single-entry/single-exit control-flow validation for asm blocks beyond label-reference scoping (§9.40).
- Barrier/fence semantics for asm blocks are a codegen concern and not independently verifier-checkable (§9.41).
- TLS on `os=none` is rejected outright rather than allowed under a TLS-capable tier, pending the same tier-table work.
- Only `x86_64`/`x86`, `aarch64`/`aarch64_be`, and `arm`/`armeb` have register tables wired up (`targets.go`); asm blocks on any other architecture are structurally rejected until that data lands. The same architectures are also the only ones listed in `DialectsForArchitecture`, so an asm block's dialect check and its register-table check fail together for any other arch.

---

## Design notes

**Nothing here is a serialization format.** `ir/vir` has no `[]byte` in or out — that's `format/`'s job entirely. This package only ever holds the in-memory shape and the rules it must satisfy.

**Verification is centralized on purpose.** Every downstream package — lowering, object translation, container-file writing — is written under the assumption that whatever `Module` it receives already passed `Verify`. Putting all of that logic in one place means every consumer gets identical guarantees regardless of whether the module came from `.vir` text, `.vbyte` bytes, or a hand-written builder call.

**The builder never second-guesses you.** `DeclareFunction`, `Emit`, `Branch`, and friends all just append to the structure. If you want a hand-built module to be usable by anything downstream, you must call `Verify` yourself — nothing does it for you implicitly.

**Dialect is a module property, not a block property.** `AsmBuilder.BeginAsm` takes no dialect argument and `AsmBlock` has no dialect field of its own; every asm block in a module is interpreted under whatever single dialect `Module.SetAsmDialect` established. This mirrors the `format/vbyte` v3 layout, which moved `AsmDialect` out of the per-block encoding and into the module header for the same reason — one asm syntax per module, never mixed.