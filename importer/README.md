# importer

```
github.com/vertex-language/vvm/importer
```

Cross-module resolution for a set of decoded `*vir.Module`s, all present in memory at once. Takes every `import "X"`, resolves it to the real module it names, checks every cross-module reference directly against that real module's real declarations, and rewrites the module so no cross-module reference survives into `lower/<arch>`.

`ir/vir` is import-agnostic — pure data model and construction. `ir/verify` is also import-agnostic — it checks one module's own invariants and has no dependency on this package. `importer` is the only package that knows about `import`, qualified-ident operands, or `StructType.Import` as anything more than a name someone wrote down. It sits after both, and neither of them imports it back.

---

## Import path

```go
import "github.com/vertex-language/vvm/importer"
```

---

## Flow

```
folder of .vbyte / .vir files
  → format/vbyte/{binary,text}.Decode        (per file → unverified *vir.Module)
  → importer.NewSet(modules) (*Set, error)    — index by qualified identity
  → set.ResolveImports() error                — every `import "X"` → real module
  → verify.Verify(m) per module               — ir/verify, unchanged, import-agnostic
  → set.CheckReferences() error               — every qualified ref checked
                                                 against the real target decl
  → set.Rewrite() error                       — erase cross-module refs:
                                                   const  → inline literal
                                                   fn/global → real mangled symbol
                                                   struct/fnsig → unchanged
  → lower/<arch>, unchanged from here on
```

`verify.Verify` must succeed on a module before `CheckReferences` looks at it — checking a cross-module reference (e.g. a struct's field types, a call's arity) is only meaningful once the referencing module is known to be internally well-formed. That ordering is the caller's responsibility: this package never imports `ir/verify` and never calls it, so nothing here enforces the precondition for you. `ResolveImports` has no such dependency — it's a pure name lookup over already-in-memory declarations and can run before or after `Verify`.

`Rewrite` re-resolves the same references `CheckReferences` validated and assumes they're still good rather than re-checking from scratch. Running `Rewrite` before `CheckReferences` has succeeded on every module is a caller error, not something this package guards against.

---

## Package layout

* **`importer.go`** — `Set`, `NewSet`. Indexes modules by qualified identity (`identity`: `"namespace/name"` if a module declared one, bare `"name"` otherwise) and rejects duplicate identities up front.
* **`resolve.go`** — `Set.ResolveImports`, `Set.resolvedTarget`. Maps every `import "X"` to the real `*vir.Module` it names; `resolvedTarget` is the shared lookup `CheckReferences` and `Rewrite` both use, and it's also where declare-before-use is enforced for cross-module references — a path that resolves fine against the whole `Set` but was never actually `import`-ed by the referencing module is still rejected.
* **`mangle.go`** — `MangledSymbol`, `SymbolForFunction`, `SymbolForGlobal`. The length-prefixed Itanium-style symbol computation (§6.3) lives here, not in `ir/vir` — `importer` is the only package that ever needs to turn a namespace + module + export name into the ABI-visible symbol another module's object file exports.
* **`check.go`** — `Set.CheckReferences` and everything it calls: struct/fnsig type references (including nested in `vec`/`array` element positions), call-target arity/variadic-ness/export/noreturn checks, and const/global qualified-operand existence/export checks. Also carries the imported-callee noreturn call-site shape check that `ir/verify` deliberately exempts (`body.go`: *"Qualified (imported) callees are exempt; importer checks those once it can see the real callee's attributes"*).
* **`rewrite.go`** — `Set.Rewrite` and everything it calls: erases qualified-ident call targets into real mangled symbols, const references into inline literals, global references into mangled-symbol idents. Struct/fnsig type annotations are left untouched — per the per-kind summary below, `CheckReferences` passing *is* the whole job for those.

There is no `verify.go` in this package, and no `mangle.go` in `ir/vir` — mangling is entirely `importer`'s concern.

---

## Per-kind summary of what `Rewrite` actually does

| Kind | Before | After | Symbol? |
|---|---|---|---|
| `const` | qualified-ident operand | inline literal operand | never had one |
| `fn` | qualified-ident call target | real mangled symbol, extern-style | yes — real linker symbol |
| `global` | qualified-ident load/store target | real mangled symbol, extern-style | yes — real linker symbol |
| `struct`/`fnsig` | type annotation naming an import | unchanged — `CheckReferences` is the only work | never had one |

---

## Example: `const` — the case that disappears entirely

**`mathlib.vir`** (exporter):

```vir
module mathlib
export const MaxRetries i32 = 3
```

**`main.vir`** (importer), before `Rewrite`:

```vir
module main
import "mathlib"

export fn main() i32:
    a = mov.i32 mathlib.MaxRetries
    return a
end
```

`CheckReferences` confirms `mathlib.MaxRetries` really is an exported `i32` constant in the real `mathlib` module. After `Rewrite`, the qualified-ident operand no longer exists anywhere in the instruction stream — `lower/<arch>` sees `mov.i32 3`, an ordinary integer literal. No symbol was ever created for `MaxRetries`, so `mangledCallTarget`/`rewriteQualifiedOperand` never produce one for a const; they substitute `Constant.Value` directly.

## Example: `fn` — becomes an ordinary extern symbol

**`http.vir`** (exporter, namespaced):

```vir
module http
namespace "acme/net"
export fn get(url ptr) i32:
    ...
end
```

**`main.vir`**, before `Rewrite`:

```vir
module main
import "acme/net/http"

export fn main() i32:
    r = call http.get, someurl
    return r
end
```

`CheckReferences` checks arity, variadic-ness, export status, and (if applicable) the noreturn call-site shape against the real `get` declaration. After `Rewrite`:

```vir
module main
import "acme/net/http"   // provenance only

export fn main() i32:
    r = call _M4acme3net4http3get, someurl
    return r
end
```

`_M4acme3net4http3get` is exactly `SymbolForFunction(http, get)` — the same mangled name `http`'s own object file exports for it. `lower/<arch>` treats the rewritten call precisely like a `link`-declared `extern fn` call; the real linker resolves it as always.

## Example: `struct` — the imported declaration is left alone

**`http.vir`**:

```vir
module http
namespace "acme/net"
export struct Response (status i32, body ptr)
```

**`main.vir`**:

```vir
module main
import "acme/net/http"

export fn get(url ptr, out byval[http.Response] resp) i32
```

`byval`/`sret` name a struct directly as a type annotation, so there's no operand for `Rewrite` to touch. `checkStructRef` confirms `Response` exists and is exported in the real `http` module; the `StructType{Name: "Response", Import: "acme/net/http"}` node is left exactly as it was. `CheckReferences` passing *is* the entire job for this kind — there's no deeper structural check possible from this side, since the Go data model gives a `StructType` only a name and an import path, never a field list of its own to diff against the real one.

---

## Resolved design points

**Bare-name vs. namespaced identity.** A bare `import "http"` only resolves against a module that itself declared no `namespace`. A namespaced module is only reachable via its full `namespace/name` string. Two modules can't collide on bare `"http"` unless both have no namespace and the same module name — `NewSet` rejects that outright as a duplicate identity.

**Import cycles.** Legal, unhandled specially. `ResolveImports`/`CheckReferences` only ever read already-in-memory declarations, never bodies, so there's no scheduling step to choke on a cycle. A cross-module struct reference can never itself form the one cycle that *does* bite (an infinitely-recursive struct-by-value across two modules) — `checkStructRef` has nothing to recurse into on a `StructType`, since the Go data model carries no field list for it to walk.

**Extern-fn re-export.** A qualified call resolving to the target module's own `extern fn` (rather than one of its `fn` definitions) is rewritten to that extern's declared name as-is — it's already an ordinary extern symbol in the real linker's sense, so there's no mangling step for it.

**Argument type checking stops at arity.** `CheckReferences` checks a cross-module call's arity and variadic-ness against the real signature, but not per-argument type agreement — that would need the same per-function local type table `ir/verify`'s `checkInstruction` builds internally, which isn't exposed outside that package.

**Mangling lives here, not in `ir/vir`.** `mangle.go`'s `MangledSymbol`/`SymbolForFunction`/`SymbolForGlobal` are this package's own implementation of §6.3 — `ir/vir` has no `mangle.go` and no opinion on symbol names at all; it only gates *whether* mangling applies via `Module.SetNamespace`.