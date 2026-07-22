# `verify.md`

## `ir/verify`

```
github.com/vertex-language/vvm/ir/verify
```

### What it is

The single place that checks a `*vir.Module` is semantically well-formed. `ir/vir` only builds — it never checks anything, and as of this cut it contains **no verification logic at all**. Anything that used to live in `vir/verify.go` (opcode arity/type-constraint checks, type fixation, definite-assignment dataflow, valist lifetime dataflow, tailcall restrictions) now lives here instead, in its own package, reading `vir`'s exported types from the outside.

`ir/verify` imports `ir/vir`. Never the reverse.

### Entry point

```go
func Verify(m *vir.Module) error
```

Single-module only. No import awareness, no cross-module anything — that's `importer`'s job, and it runs `verify.Verify` on each module *before* doing any cross-module reference checking of its own.

```go
m := vir.NewModule("add_example")
// ... build with vir's construction API ...

if err := verify.Verify(m); err != nil {
    // err names the exact rule violated
    panic(err)
}
```

### What it checks

One forward pass over module-level sections, then one pass per function:

- **Target** — arch/OS/ABI canonical, no aliases.
- **Name resolution** — one flat namespace across structs, fnsigs, consts, globals, externs, functions. (Labels are function-scoped, §4.3 — checked separately, per function, under "Body shape" below, not here.)
- **Per-section shape** — struct fields, fnsig signatures, const/global initializers.
- **Link ↔ extern correspondence** — every `extern` group's dependency string matches a previously declared `link` name, byte-for-byte.
- **`import` declarations** — non-empty, declared at most once. (Whether the named module actually exists, and whether its bound name collides with anything, is `importer`'s problem entirely — see below.)
- **Function attributes** — at most one `entry`, `entry`/`extern_c` mutually exclusive, `entry` constraints (no byval/sret, no noreturn, must be exported).
- **Body shape** — every block terminates exactly once; every referenced label resolves to a label defined in the same function, with uniqueness checked per-function, not module-wide; every read preceded by an assignment on every path (definite assignment / join convention).
- **`noreturn` call sites** — a direct call to a callee whose `noreturn` attribute is visible *within this module* (a local `fn` or an `extern` declaration) must be immediately followed by `unreachable`/`trap`, or itself end the block as the terminator. Purely structural — no analysis of the callee's body. Calls through a qualified (imported) name are exempt here; `importer` checks those once it can see the real callee's attributes.
- **Type fixation** — a value's type is locked at first assignment; a conflicting later assignment is rejected.
- **Valist lifetimes** — must/may dataflow: `va_start` before any `va_arg`/`va_end` on every path, no re-`va_start` without an intervening `va_end`, no valist left open across `return`.
- **Tailcalls** — return type match, no byval/sret, rejected from a variadic caller with an open valist to a variadic callee.

### What it does not check

- **Label reachability.** A block that's never branched to but still terminates correctly is dead code, not unsound code — verify has no opinion on it.
- **`readonly` enforcement.** "Must not write through a pointer reachable from arguments/globals" (§4.2) is a trusted annotation, not something this pass verifies — enforcing it for real would require whole-program alias/effects analysis. Violating it is UB (§5.4 item 7), the same way LLVM never checks that a function marked `readonly`/`readnone` actually behaves that way.
- **Import bound-name collisions**, and whether an imported module exists at all. Both are `importer`'s job, run after `Verify` succeeds on each module individually — same deliberate punt as everything else cross-module.
- Nothing cross-module in general. A qualified reference (`mathlib.MaxRetries`, `http.get`) is checked here only for *shape* (is it a well-formed `import`-qualified operand), never for *correctness* against the real target module.
- Nothing about `.vbyte`/`.vir` framing or syntax — that's the decoders (`format/vbyte/{binary,text}`), which run before this and hand `verify` an already-structurally-parsed `*vir.Module`.

### Where it sits

```
folder of .vbyte / .vir
  → format/vbyte/{binary,text}.Decode   → unverified *vir.Module
  → verify.Verify(m)                    → single-module invariants only
  → importer.ResolveImports / CheckReferences / Rewrite   → cross-module
  → lower/<arch>, ...
```

Every downstream package still assumes whatever `*vir.Module` it receives has already passed `verify.Verify` — that contract doesn't change, it just isn't enforced by code sitting inside `ir/vir` anymore.