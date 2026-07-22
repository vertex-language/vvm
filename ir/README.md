# ir/vir

[github.com/vertex-language/vvm/ir/vir](https://github.com/vertex-language/vvm/ir/vir)

The Vertex IR data model: the in-memory representation of a program and the construction API for building one. That's it. This package holds no checking logic of any kind — it cannot tell you whether a `Module` is well-formed, only what shape a `Module` is allowed to be built in.

---

## Import path

```go
import "github.com/vertex-language/vvm/ir/vir"
```

---

## Why this package doesn't check anything

Semantic checking — name resolution, type fixation, definite assignment, valist lifetimes, opcode arity/operand-constraint/result-rule enforcement — lives in `ir/verify`, a sibling package. It imports `ir/vir` and reads its exported types from the outside; it never needs privileged access, because `Module`/`Function`/`Block`/`Instruction` are already fully public.

Cross-module resolution (`import`, qualified references, mangled-symbol rewriting) lives in `importer`, a separate top-level package, which resolves against real modules directly.

What's left in `ir/vir` is the thing every other package actually needs to agree on: the data structures themselves, and a construction API that builds them without opinions. `format/vbyte/*` decodes into this shape. `ir/verify` checks this shape. `importer` resolves cross-module references against this shape. `lower/<arch>` consumes this shape. Nobody needs to import `ir/verify` or `importer` just to *hold* a `Module` — that dependency only shows up for the packages whose actual job is checking or resolving.

Inline/native assembly is not part of this package's data model — see `asm.md` for where that's going.

---

## Package layout

* **`module.go`** — `Module`, `Target`, `Field`, `Struct`, `FunctionSignature`, `Constant`, `Global`, `ConstInit` variants (`InitLiteral`, `InitZero`, `InitAddressOf`, `InitAggregate`, `InitByteString`), `Link`/`LinkKind`, `ExternGroup`/`ExternFunction`, `Import`, `Param`, `FunctionAttribute`, `Function` (incl. `HasAttribute`, `EntryFunction`), `Block`, `AllBlocks`, `Instruction`, `Terminator` variants (`Branch`, `BranchIf`, `SwitchCase`, `Switch`, `Return`, `TailCall`, `Trap`, `Unreachable`), and `Successors`.
* **`opcode.go`** — `Opcode`, the closed §4 instruction vocabulary as a Go enum, plus `opTable` (the single source of truth for each opcode's arity/operand-type-constraint/result-rule), `String()`/`ParseOpcode`, and an `init()`-time completeness check.
* **`types.go`** — the `Type` interface: `IntType`, `FloatType`, `PtrType`, `VoidType`, `VecType`, `StructType`, `ArrayType`, `ValistType`; `Equal`, `IsInt`/`IsFloat`/`IsPtr`/`IsVoid`/`IsVec`/`IsValist`/`IsAggregate`/`IsValueType`/`IsScalarType`/`IsVaArgType`/`ElemOrSelf`.
* **`operand.go`** — `Operand`, `OperandKind`, and constructors (`Ident`, `QualifiedIdent`, `IntLiteral`, `FloatLiteral`, `StringLiteral`, `BoolLiteral`, `NullLiteral`, `TypeOperand`, `OrderingOperand`, `VectorLiteral`).
* **`float.go`** — `formatFloat`, canonical float-literal text formatting (`NaN`/`Inf`/`-Inf`, and appending `.0` where the grammar requires a decimal point).
* **`targets.go`** — canonical arch/OS/ABI vocabularies (`CanonicalArch`, `CanonicalOS`, `CanonicalABI`), the rejected-alias tables resolved only at the build-system boundary (`ArchAliases`, `OSAliases`), `PointerBits`, and `BinFormat`/`FormatOf`.
* **`linkfile.go`** — `DeriveLinkFile`, computing the on-disk filename a `Link` implies for a given `BinFormat`, per the short-name/exact-name rules (§7.2/§7.4). Exported so build-layer code (and `ir/verify`) can resolve the same filename without re-implementing the derivation.
* **`builder.go`** — `NewModule` + `FunctionBuilder`, the construction API that mirrors the IR structure one-to-one, appends only, and checks nothing.

There is no `verify.go` and no `vmeta.go` in this package.

---

## Design: this package only ever appends

```go
m := vir.NewModule("add_example")
m.SetTarget("x86_64", "linux", "gnu")

fmtGlobal := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 14},
    vir.InitByteString{Data: []byte("%d + %d = %d\n\x00")})

fb := m.DeclareFunction("main", nil, vir.I32, true)
sum := fb.Add("sum", vir.I32, vir.IntLiteral(7), vir.IntLiteral(35))
fb.Call("r", "printf", vir.Ident(fmtGlobal.Name), vir.IntLiteral(7), vir.IntLiteral(35), sum)
fb.Return(vir.IntLiteral(0))
```

Nothing above validated anything — no name-collision check, no type check, no control-flow check. A `Module` built this way, or produced by a decoder, carries no notion of "verified" as a property of the value itself. Whether it's trustworthy is entirely a question of what's been run on it *afterward*:

```go
import (
    "github.com/vertex-language/vvm/ir/vir"
    "github.com/vertex-language/vvm/ir/verify"
)

if err := verify.Verify(m); err != nil {
    panic(err)
}
```

`ir/vir` has no idea `ir/verify` exists, and never calls into it. The builder never calls `Verify` for you implicitly, and it never will — that's not a gap, it's the point of the split.

---

## Core concepts

### `Opcode`

The §4 instruction vocabulary is closed and spec-fixed, so it's a Go enum, not a string.

```go
vir.OpAdd     // add
vir.OpCtlz    // ctlz
vir.OpFma     // fma
vir.OpVaStart // va_start

vir.OpAdd.String()        // "add"
vir.ParseOpcode("ctlz")   // (vir.OpCtlz, true) — used by decoders
```

Every `Opcode` constant is registered exactly once in `opTable`, pairing it with its operand-count, operand-type-constraint, and how its result type is computed. `init()` panics at package load if a constant is missing an entry — a newly added opcode can't silently skip that registration the way a name absent from a hand-maintained map could. `opTable` is internal; `ir/verify` maintains its own equivalent metadata (`opinfo.go`) rather than reaching into it, so the two packages' registrations are independent and both must stay exhaustive.

Struct/field/label/link/function names stay `string` — they're open, user-chosen identifiers.

### `Type`

One interface, eight implementations. `IsAggregate`/`IsValueType` distinguish memory-only types (`StructType`, `ArrayType`) from types that may name a value. `ValistType` is excluded from `IsValueType` too — legal only as an `alloca` result and a `va_start`/`va_arg`/`va_end` operand.

```go
vir.I32                 // IntType{32}
vir.F64                 // FloatType{64}
vir.Ptr                 // PtrType{}
vir.Valist              // ValistType{} — opaque varargs cursor (§4.4)
vir.VecType{Elem: vir.I32, Len: 4}
vir.ArrayType{Elem: vir.I8, Len: 14}
vir.StructType{Name: "Point"}

vir.Equal(vir.I32, vir.IntType{Bits: 32}) // true — structural equality
```

`StructType` equality is nominal per-origin: two `StructType`s with the same `Name` but different `Import` paths are never `Equal` — cross-module struct identity isn't safe to compare by spelling alone. This package doesn't resolve that identity; it just refuses to pretend two different origins are the same type. Resolving `Import` against a real exporting module is `importer`'s job entirely.

### `Operand`

A tagged union covering every operand position: idents (plain or import-qualified), literals, `null`, a type used in operand position (`index.ptr`), atomic orderings, and vector literals.

```go
vir.Ident("x")                        // ident
vir.QualifiedIdent("mathlib", "add")  // cross-module ident (§7.3), prints "mathlib.add"
vir.IntLiteral(42)
vir.FloatLiteral(3.14)
vir.BoolLiteral(true)
vir.NullLiteral()
vir.TypeOperand(vir.I32)
vir.OrderingOperand("acquire")
vir.VectorLiteral(0, 4, 1, 5)
```

`QualifiedIdent` is just a shape this package knows how to hold and print — `ir/vir` has no opinion on whether the module it names exists, or whether the reference resolves to anything real. That's `importer`'s entire reason for existing: it walks a set of modules, resolves these operands against real declarations, and rewrites them away before `lower/<arch>` ever sees the module.

### Instructions and terminators

`Instruction.Op` is an `Opcode`; exactly one of `Instruction.Suffix` (a `Type`) or `Instruction.Sig` (an `fnsig` name, or `va_start`'s self-referential token) may be set. Terminators are a separate interface, so "exactly one terminator, nothing after it" isn't something this package enforces by scanning — that's `ir/verify`'s job.

```go
type Terminator interface{ isTerm() }
// Branch, BranchIf, Switch, Return, TailCall, Trap, Unreachable

func Successors(t Terminator) []string // labels a terminator may transfer to
```

### Variadic functions and `valist` (§4.4)

```go
fb.SetVariadic()
cursor := fb.AllocaValist("ap")
fb.VaStart("main", "ap", "lastParam")
v := fb.VaArg("v", vir.I32, cursor)
fb.VaEnd(cursor)
```

This package only records the shape of these calls. The lifetime rules — `va_start` before any `va_arg`/`va_end` on every path, no re-`va_start` without an intervening `va_end`, nothing left open across `return`, and the tailcall/open-valist restriction — are dataflow checks `ir/verify` runs, not something `builder.go` enforces at construction time.

### Targets and link filenames

`targets.go` holds the canonical arch/OS/ABI vocabularies and the alias tables that only ever get consulted at the build-system boundary — the IR grammar itself never accepts an alias, so `ir/verify` treats any alias it sees in a `Target` as a straight rejection, not something to canonicalize on the fly.

```go
vir.CanonicalArch["x86_64"]        // true
vir.ArchAliases["amd64"]           // "x86_64" — a rejected alias, not accepted input
vir.PointerBits("arm")             // 32
vir.FormatOf("windows")            // vir.FormatPE
```

`linkfile.go`'s `DeriveLinkFile` computes the same on-disk filename a `Link` implies (`libSDL2.so`, `SDL2.dll`, `SDL2.framework/SDL2`, ...) that `ir/verify`'s extension checks and the build layer both need, so neither has to re-derive it independently.

### Namespaces and symbol mangling (§6.3)

`Module.SetNamespace` gates mangling for exported `fn`/`global` symbols. Mangled-symbol computation reads `Namespace` + module name + export name to produce the ABI-visible name — `entry`/`extern_c` force a bare symbol regardless of namespace; an unnamespaced module also gets a bare symbol.

### Cross-module shape: `Import` and `QualifiedIdent`, and nothing past that

`Module.Imports` holds `import "X"` declarations. `Operand.Qualifier` holds a cross-module reference's import path. `StructType.Import` holds where an imported struct's shape is supposed to come from. That's the entire cross-module surface `ir/vir` exposes — three fields, no lookup, no resolution, no cross-module data model.

Everything past "here's a name and where it claims to come from" is out of scope for this package:

* Whether `Import.Path` actually names a real module → `importer.ResolveImports`
* Whether a `QualifiedIdent`/`byval[S]`/`sret[S]` reference actually matches the real target's declaration → `importer.CheckReferences`
* Rewriting resolved references into inline literals or real mangled symbols → `importer.Rewrite`

`ir/vir` doesn't import `importer`, and `importer` is the only package that imports `ir/vir` for this purpose. The dependency runs one direction only.

---

## Design notes

**Nothing here is a serialization format.** `ir/vir` has no `[]byte` in or out — that's `format/`'s job entirely.

**Nothing here checks anything.** Not name collisions, not type mismatches, not control flow, not opcode legality. `ir/verify` is the only place that validates a `Module`; this package doesn't track a "verified" flag on the value because it has no concept of verification at all.

**The builder never second-guesses you.** `DeclareFunction`, `Emit`, `Branch`, and friends all just append to the structure.

**Closed vocabularies are enums; open vocabularies are data.** `Opcode` covers §4's fixed instruction set and is a Go type the compiler and `opcode.go`'s `init()` both help keep exhaustive. Struct/field/label/link/function names stay `string` because they're genuinely open.

**No inline assembly.** That support has moved out of Vertex IR proper — see `asm.md`.