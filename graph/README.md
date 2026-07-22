# graph

`github.com/vertex-language/vvm/graph`

Computes a dependency-first readiness order over a set of modules, reading
only their `.vmeta` shape data (`format/vmeta/binary`, `ir/vir` §7.3 Stage
1) — never `.vir`/`.vbyte`, never `vir.Verify`, never `vir.Module`. This
is Stage 1 of the pipeline the top-level README describes and, until now,
no package actually implemented: `resolveImportGraph`.

---

## Import path

```go
import "github.com/vertex-language/vvm/graph"
```

## API

```go
func ResolveImportGraph(modules map[string]*vmetabinary.Result) (order []string, err error)
func ShapesForImports(modules map[string]*vmetabinary.Result, key string) (map[string]*vir.ModuleShape, error)
```

`modules` is keyed by each module's own qualified import path —
`vir.ModuleShape.QualifiedID()`'s output, i.e. exactly the string another
module's `import "..."` line references it by. `ResolveImportGraph`
cross-checks every `Result`'s own reported identity against the key it
was stored under rather than trusting the caller's map silently.

`order` lists every key exactly once, dependency-first: a module's key
never precedes any module it directly imports. Ties are broken lexically
for determinism across runs.

`ShapesForImports` is a small convenience over the same data — given a
module already known-good by `ResolveImportGraph`, it returns exactly the
`map[string]*vir.ModuleShape` `vir.VerifyWithImports` wants for that
module's direct imports.

## What gets rejected

* A `.vmeta` `Result` with no `Shape`.
* A `Result` keyed under a string that doesn't match its own
  `Shape.QualifiedID()`.
* An empty import string, a self-import, or a duplicate import within one
  module's own `Imports` list.
* An import naming a module not present in `modules` at all.
* An import cycle — reported with the concrete cycle path, e.g.
  `graph: import cycle detected: a/foo -> b/bar -> a/foo (...)`.

None of these are recoverable-and-continue; every one is a named error,
matching the rest of the pipeline's "fail loudly, never guess" stance
(`vir.Verify`, `objectwriter`'s adapters, `vvm`'s own `dispatch.go`).

## Design notes

**Reads `.vmeta` only, on purpose.** `graph` exists specifically so
readiness ordering can run ahead of every expensive stage — full
`vir.Verify`, lowering, object emission — using only the small artifact
Stage 0 already produced. It imports `ir/vir` for exactly one thing, the
`vir.ModuleShape`/`vir.ModuleShape.QualifiedID()` types `format/vmeta`
itself already carries — never `vir.Verify`, never `vir.NewModule`/the
builder API.

**Deterministic output.** Two independent runs over the same input map
produce the same `order` and, on a cycle, the same reported cycle path.
This isn't just for tests: a build cache keying invalidation off `.vmeta`
content (per the top-level README's caching section) needs the *ordering
decision itself* to be reproducible from that same content, not an
artifact of Go's randomized map iteration.

**Cycle rejection is an inferred requirement, not literal spec text.**
`ir.md` §7.3 never states outright that the import graph must be acyclic
— but Stage A's model ("a module becomes ready once its direct imports'
`.vmeta` is ready") has no coherent fixed point if two modules import
each other, so this package treats a cycle as a hard error rather than a
strictness knob a caller could disable.

## Known gaps / not yet wired anywhere

This package has no caller yet. The multi-module entry point in the
top-level `vvm` package (`Build`/`BuildModule` today only ever handle a
single, import-free module) still needs to be written to actually call
`ResolveImportGraph`/`ShapesForImports` and drive `vir.VerifyWithImports`
per module in the order this package returns. That's tracked separately,
not part of this package.