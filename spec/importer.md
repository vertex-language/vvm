# `importer.md`

## `importer` — Cross-Module Resolution

```
github.com/vertex-language/vvm/importer
```

### The one job

Take a set of decoded `*vir.Module`s, all present in memory at once. Resolve every
`import "X"` to the real module it names. Check every cross-module reference directly
against that real module's real declarations. Rewrite the module so every cross-module
reference is gone, replaced by either an inline literal or an ordinary extern-style
symbol reference.

`ir/vir` stays single-module and import-agnostic — pure data model and construction.
`ir/verify` stays single-module too — it checks one module's own invariants and has no
dependency on `importer`. `importer` is the only package that knows about cross-module
resolution, and it sits after both.

---

### Flow

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
                                                   struct/fnsig → substituted decl
  → lower/<arch>, unchanged from here on
```

`verify.Verify` must succeed on a module before `CheckReferences` looks at it — checking
a cross-module reference (e.g. a struct's field types) is only meaningful once the
referencing module is known to be internally well-formed. `ResolveImports` is a pure
name lookup and doesn't depend on `verify.Verify` — it can run before or after it.

---

### Example: `const` — the case that disappears entirely

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

`CheckReferences` confirms `mathlib.MaxRetries` really is an `i32` with value `3` in the
real `mathlib` module.

**`main.vir`**, after `Rewrite` — this is what `lower/<arch>` actually receives:

```vir
module main
import "mathlib"     // kept for provenance/debug info only — no longer resolved at lower time

export fn main() i32:
    a = mov.i32 3
    return a
end
```

`mathlib.MaxRetries` the *qualified-ident operand* no longer exists anywhere in the
instruction stream. `lower/<arch>` sees `mov.i32 3` — an ordinary integer literal. No
symbol was ever created for `MaxRetries`, so there's nothing to relocate and nothing for
the linker to check — the check already happened, once, in `CheckReferences`.

---

### Example: `fn` — becomes an ordinary extern symbol

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

`CheckReferences` checks arity/types/variadic-ness/`noreturn`/`readonly` against the real
`get` declaration in `http`.

After `Rewrite`:

```vir
module main
import "acme/net/http"   // provenance only

export fn main() i32:
    r = call _M4acme3net4http3get, someurl
    return r
end
```

`_M4acme3net4http3get` is exactly `vir.MangledSymbol` for `http.get` — the same mangled
name `http`'s own object file exports. `lower/<arch>` treats this precisely like a
`link`-declared `extern fn` call; the real linker resolves/relocates it as always.

---

### Example: `struct` — the imported declaration is substituted in place

**`http.vir`**:

```vir
module http
namespace "acme/net"
export struct Response (status i32, body ptr)
```

**`main.vir`**, before `Rewrite`:

```vir
module main
import "acme/net/http"

export fn get(url ptr, out byval[http.Response] resp) i32
```

`byval`/`sret` name a struct directly as a type annotation, so there's no operand to
rewrite here. What `Rewrite` does instead: `StructType{Name: "Response", Import:
"acme/net/http"}` is checked field-for-field against the real `http.Response`, and the
type node is left as-is — it already carries everything `lower/<arch>` needs.
`CheckReferences` passing *is* the whole job for this kind.

---

### Per-kind summary of what `Rewrite` actually does

| Kind | Before | After | Symbol? |
|---|---|---|---|
| `const` | qualified-ident operand | inline literal operand | never had one |
| `fn` | qualified-ident call target | real mangled symbol, extern-style | yes — real linker symbol |
| `global` | qualified-ident load/store target | real mangled symbol, extern-style | yes — real linker symbol |
| `struct`/`fnsig` | type annotation naming an import | unchanged — `CheckReferences` is the only work | never had one |

---

### Resolved design points

**Bare-name vs. namespaced identity.** A bare `import "http"` only resolves against a
module that itself declared no `namespace`. A namespaced module is only reachable via
its full `namespace/name` string. Two modules can't collide on bare `"http"` unless both
have no namespace and the same module name — `NewSet` rejects that outright as a
duplicate identity.

**Import cycles.** Legal, unhandled specially. `ResolveImports`/`CheckReferences` only
ever read already-in-memory declarations, never bodies, so there's no scheduling step to
choke on a cycle. The only way a cycle actually bites is an infinitely-recursive
struct-by-value (A embeds B by value, B embeds A by value) — the same "unresolvable
layout" error a single-module self-referential struct already triggers.