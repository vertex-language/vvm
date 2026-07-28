# `verify` - Semantic Verification for `vir`

`package verify` implements `ir/verify`: the single place that checks a `*vir.Module` is semantically well-formed. `ir/vir` only *constructs* IR — it never validates anything beyond structural shape — so every ordering, typing, dataflow, and naming rule described in the language spec is re-derived here, reading `vir`'s exported types from the outside.

```go
import "github.com/vertex-language/vvm/ir/verify"
```

## Scope & Capabilities

* **Single-module only.** No import awareness, no cross-module reference checking — that is `importer`'s job, which runs `Verify` on each module first and only then checks cross-module references of its own.
* **Deterministic error ordering.** Checks run in the module's fixed §2.1 section order (Structs → Signatures → Constants → Globals → Links → Externs → Imports → Functions), so an error always names the *first* rule violated in file order rather than an arbitrary one.
* **Two-layer instruction checking.** Generic arity/numeric-constraint/result-binding rules are table-driven (`opinfo.go`); opcode-specific shape rules that don't fit the table (`alloca`, `call`, `extract`, reductions, `syscall`) get dedicated logic (`body.go`).
* **Two dataflow passes per function body.** A must-reach analysis for definite assignment (the Join Convention, §4.3) and a paired must/may analysis for `valist` open/close lifetimes (§4.4), both computed to a fixpoint over the CFG.

## File Structure

| File | Purpose |
| --- | --- |
| `verify.go` | Package entrypoint (`Verify`); orchestrates every check in module-section order. |
| `names.go` | `nameTable` — enforces the strict flat namespace (§2.2): zero shadowing across structs, fnsigs, consts, globals, extern fns, and fns. |
| `types.go` | `typeCtx` — structural type well-formedness and struct declare-before-use (§2.2). |
| `target.go` | Validates `target` against the canonical arch/os/abi vocabularies (§7.1), rejecting build-system aliases. |
| `declarations.go` | Struct, fnsig, constant, and global declarations, including const-init legality (§6.2). |
| `links.go` | Link-section validation, link↔extern correspondence, and shared byval/sret param checks (`checkParam`). |
| `imports.go` | Import-decl validation: non-empty, declared at most once. |
| `functions.go` | `fnCtx`, `checkFunctions` — per-function attribute rules (`entry`, `extern_c`, `noreturn`) and param/return checks. |
| `body.go` | Function body shape: block termination, label resolution, per-instruction checks, terminator shape, noreturn call-site placement. |
| `opinfo.go` | `opInfoTable` — arity, numeric-constraint, and result-rule metadata for every opcode, verify's own (separate from `vir`'s internal opcode table). |
| `dataflow.go` | Definite-assignment and `valist`-lifetime dataflow over the function CFG. |

## Core Checks

### 1. Verification Order (`verify.go`)

`Verify` walks the module in the same fixed order it's declared in (§2.1): `checkTarget` → `checkStructs` → `checkFunctionSignatures` → `checkConstants` → `checkGlobals` → `checkLinks` → `checkExterns` → `checkImports` → `checkFunctions`. Function names are collected up front so a global's `addr` initializer can reference a function (§6.2) even though functions are checked last.

### 2. Flat Namespace (`names.go`)

Every struct, fnsig, const, global, extern fn, and fn name is registered in one `nameTable`, checked at declaration time regardless of section. Collisions are illegal *across* kinds, not just within one — declaring a `global` and a `fn` with the same name both fail at the second declaration.

### 3. Types (`types.go`)

* Integer types must have bit width ≥ 1; float types are restricted to `f16`/`f32`/`f64`.
* `vec` element types must be scalar or `ptr` — never aggregate, void, or `valist`.
* Struct types must have been declared strictly earlier in the module (§2.2) unless imported (`Import != ""`), which is deferred to `importer`.

### 4. Target (`target.go`)

Only canonical arch/os/abi spellings are accepted (§7.1). A recognized alias (e.g. `amd64` for `x86_64`) produces a specific "rejected alias" error rather than a generic unknown-value error, since alias resolution belongs exclusively at the build-system boundary.

### 5. Declarations (`declarations.go`)

* **Structs:** at least one field, no duplicate field names, no `valist`/`void` fields.
* **Function signatures:** params/return must be value types, non-aggregate returns only (aggregates go via `sret`).
* **Constants:** compile-time scalars or vectors only (§6.2); literal operand kind must match the declared type.
* **Globals:** const-init trees are validated against the declared type, including `addr` initializers, which may name an earlier global (declare-before-use) or *any* function in the module (functions get the same forward-reference exemption self-recursion has, since the reference resolves as a link-time relocation, not a lexical dependency).

### 6. Links & Externs (`links.go`)

* Link names must be unique and use one of the three closed kinds (`static`/`shared`/`framework`).
* A `target` is required if any `link` is present.
* Every extern group's dependency must match a declared link name byte-for-byte.
* `checkParam` enforces the byval/sret mirror-image ABI rule: both require a `ptr`-typed param naming a declared struct, and the two attributes are mutually exclusive on one param.

### 7. Imports (`imports.go`)

Paths must be non-empty and declared at most once. Whether the imported module actually exists, or its bound name collides with anything, is left entirely to `importer`.

### 8. Functions (`functions.go`)

* `entry` and `extern_c` are mutually exclusive; both require `export`.
* At most one `entry` fn per module; an `entry` fn must not be `noreturn` and must not have byval/sret params.
* `fnCtx` tracks module-wide lookups (`fns`, `externs`, `fnsigs`, `noreturn` sets, `moduleScope`) built incrementally in file order, so a function only ever "sees" itself and functions declared strictly earlier — the same declare-before-use exemption self-recursion gets elsewhere.

### 9. Instruction & Terminator Shape (`body.go`, `opinfo.go`)

* Every block must terminate, and every terminator's target labels must resolve within the function (labels are function-scoped, no duplicates).
* Each instruction's arity, numeric constraint (`cInt`/`cFloat`/`cIntOrFloat`/`cIntOrPtr`), and result rule (`rSuffix`/`rVoid`/`rBool`/`rSpecial`) come from `opInfoTable`; a result name is fixed to its first-assigned type and any conflicting reassignment is rejected (§4.3 rule 2).
* `rSpecial` opcodes (`alloca`, `call`, `syscall`, `extract`, reductions, `min`/`max`) get bespoke shape checks in `resultTypeSpecial` — e.g. `alloca.ptr` takes one operand, `alloca.valist` takes none; `min`/`max` reject integer suffixes with a dedicated message pointing at `smin`/`smax`/`umin`/`umax`.
* `tailcall` requires return-type agreement with the caller and rejects byval/sret callee params (§4.2).
* A direct call to a locally-visible `noreturn` callee must be immediately followed by `unreachable` (after `loc` lines) or itself precede a `trap`/`unreachable` terminator.

### 10. Dataflow (`dataflow.go`)

* **Definite assignment** is a must-reach forward analysis (§4.3 rules 3/5): non-entry blocks start optimistic at the full name universe and shrink via intersection with predecessors until a fixpoint, so any violation caught mid-iteration is already real. Globals and consts (`moduleScope`) are excluded from the flow-sensitive sets entirely and checked as a flat allow-list, since they exist before the function is ever entered.
* **Valist lifetimes** (§4.4) run a paired must/may analysis per open `valist`: `mustOpen` (intersection) backs `va_arg`/`va_end` legality, `mayOpen` (union) backs the re-`va_start` and open-across-`return` checks. A tailcall to a variadic callee while the caller's own variadic valist is still open is rejected, since frame reuse would invalidate the live save area.

---

## Usage Example

```go
m := vir.NewModule("app")
m.SetTarget("x86_64", "linux", "gnu")

fb := m.DeclareFunction("main",
    []vir.Param{{Name: "argc", Type: vir.I32}},
    vir.I32, false, vir.AttributeEntry)

// ... build the body ...

if err := verify.Verify(m); err != nil {
    log.Fatalf("invalid module: %v", err)
}
```

`Verify` returns the first violation encountered, in module-section then in-function order — e.g. an undeclared struct field type is reported before any function body is examined, and within a function, a `block %q line %d` error names the exact instruction that failed.