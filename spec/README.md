# Vertex IR — Language Specification (v2.0)

**File Extensions:** `.vir` (text), `.vbyte` (binary).

Vertex IR is a unified, CPU-only intermediate representation featuring structured control flow, flat basic blocks, and opcode-first typed instructions. It replaces SSA phi nodes with a value-naming convention and avoids a stack machine.

*Note: Verification rules are merged into their relevant feature sections rather than kept as a standalone checklist. Reference tables (target triples, ABI) are kept in full — these are the parts a summary can't compress without losing information.*

*Note: Inline/native assembly support has been removed from Vertex IR proper. See `asm.md` for where it's going.*

---

## 1. Scope & Design Principles

* **CPU-Only & No Runtime:** Targets real CPUs without garbage collection, exceptions, sandboxing, or support libraries.
* **No Built-In Heap:** All built-in allocation is stack-based (`alloca.ptr`); heap allocation requires `extern fn` calls.
* **Hardware Mapping:** Types map directly to hardware register classes (`iN`, `fN`, `ptr`, `vec`). Memory requires raw pointers.
* **Join Convention:** No SSA phi nodes; values merge across blocks via same-name assignments.
* **Strict Semantics:** One behavior per opcode. No target-dependent semantics, fast-math relaxations, or flag variants. Minimal Undefined Behavior (UB). (Note: target-*dependent codegen* under a fixed, opcode-defined meaning — e.g. varargs register-save layout, §4.4 — does not violate this; the opcode's contract stays constant even where its lowering doesn't.)
* **Self-Contained Modules:** Link dependencies are declared in-module; no external linker flags are used.
* **Pointer Provenance Stance:** Pointers are addresses, full stop. Alias analysis relies only on object bounds and reachability (§5.3), never on how a pointer was constructed — this costs some optimization opportunity but keeps the memory model explainable in one paragraph.

---

## 2. Module Grammar & Order

A module consists of sequential lines with no separators or continuations. Indentation is purely conventional.

### 2.1 Fixed Section Order

Modules must be declared in this strict, exact order to enable one-pass verification:

1. **Header:** `module ident` (Exactly once).
2. **Namespace:** `namespace string` (Optional, organizational only).
3. **Target:** `target arch os abi [tiers]` (Optional, but required if `link` is present).
4. **Declarations:** `struct` → `fnsig` → `const` → `global`.
5. **Dependencies:** `link` → `extern` groups → `import`.
6. **Functions:** `fn` definitions.

### 2.2 Global Rules

* **Declare-Before-Use:** No forward references exist. Only direct self-recursion in function bodies is exempt.
* **Namespacing:** A strict flat namespace is enforced module-wide with zero shadowing. A qualified name (`module.name`) is never itself a flat-namespace entry.
* **Export Behavior:**
  * `export fn`/`global`: Produces a real ABI-visible symbol (mangled if a `namespace` is defined, bare otherwise — see §6.4) and is importable.
  * `export struct`/`const`/`fnsig`: Produces no symbol. It acts purely as a shape-visibility flag for cross-module sharing.
* **`entry`/`extern_c`:** Both require `export`. `entry`: at most one per module, rejected with `byval`/`sret` params or `noreturn`. `extern_c`: forces a bare C symbol even in a namespaced module. The two are mutually exclusive on the same `fn` — they are distinct overrides of symbol naming and are never silently resolved by precedence.

### 2.3 Grammar Definition

```text
module        := module-header
                 namespace-decl?
                 target-decl?
                 struct-decl*
                 fnsig-decl*
                 const-decl*
                 global-decl*
                 link-decl*
                 extern-group*
                 import-decl*
                 fn-def*

module-header := "module" ident
namespace-decl := "namespace" string-literal

target-decl   := "target" arch os abi? tier-list?
arch          := ident
os            := ident
abi           := ident
tier-list     := "[" ident ("," ident)* "]"

struct-decl   := "export"? "struct" ident "(" field ("," field)* ")"
field         := ident type

fnsig-decl    := "export"? "fnsig" ident "(" type-list? ")" type
type-list     := type ("," type)* ("," "...")?

type          := "i" [1-9][0-9]* | "f" (16|32|64) | "ptr" | "void" | "valist"
               | "vec[" type "," int-literal "]"
               | "struct" ident
               | "array[" type "," int-literal "]"

const-decl    := "export"? "const"  ident type "=" literal
global-decl   := "export"? "global" "tls"? ident type ("align" int-literal)? "=" const-init

link-decl     := "link" lib-kind string-literal
lib-kind      := "static" | "shared" | "framework"

extern-group  := "extern" string-literal ":" extern-fn* "end"
extern-fn     := "fn" ident "(" param-list? ")" type fn-attr*

import-decl   := "import" string-literal

fn-def        := "export"? "fn" ident "(" param-list? ")" type fn-attr* ":"
                 entry-block block* "end"

param-list    := param ("," param)* ("," "...")?
param         := ident type param-attr*
param-attr    := "byval" "[" ident "]" | "sret" "[" ident "]"
fn-attr       := "noreturn" | "readonly" | "inline" | "noinline" | "cold" | "entry" | "extern_c"

const-init    := literal | "zero" | "addr" ident
               | "(" const-init ("," const-init)* ")"

entry-block   := body-line* terminator
block         := label-line body-line* terminator
label-line    := ident ":"
body-line     := inst | loc-line
loc-line      := "loc" string-literal int-literal int-literal?

inst          := ident "=" op operand-list? align-clause?
               | op operand-list? align-clause?

op            := ident ("." (ident | type))?
operand-list  := operand ("," operand)*
align-clause  := "," "align" int-literal

terminator    := "br" label
               | "br_if" operand "," label "," label
               | "switch" operand "," label ("," int-literal label)*
               | "return" operand?
               | "tailcall" ident ("," operand)*
               | "tailcall" "." ident operand ("," operand)*
               | "trap"
               | "unreachable"

operand       := ident | qualified-ident | literal | type | ordering
qualified-ident := ident "." ident
ordering      := "relaxed" | "acquire" | "release" | "acqrel" | "seqcst"

literal       := int-literal | float-literal | string-literal | bool-literal | "null"
int-literal   := "-"? [0-9]+
float-literal := "-"? [0-9]+ "." [0-9]+ ("e" "-"? [0-9]+)? | "NaN" | "Inf" | "-Inf"
string-literal:= "\"" [^"]* "\""
bool-literal  := "true" | "false"
```

---

## 3. Types & Lexical Structure

* **Identifiers:** `[A-Za-z_][A-Za-z0-9_]*` (No sigils).
* **Comments:** `//` to end of line.
* **Integers:** `i1` (boolean), `i8`, `i16`, `i32`, `i64`, `i128`.
* **Floats:** `f16`, `f32`, `f64`.
* **Pointer:** `ptr` (Untyped; width matches target's `usize`).
* **Aggregates:** `struct <ident>`, `array[T, N]` (Memory only; never held in named values).
* **Vector:** `vec[T, N]` (Width requires matching feature tier).
* **`valist`:** Opaque, target-defined-layout type representing an in-progress variadic argument cursor (§4.4). Never bitcastable to `ptr` or any other type — its internal shape is deliberately unspecified so no frontend can write layout-dependent code against it. Only legal as an `alloca` result, a `va_start`/`va_arg`/`va_end` operand, or a `byref`-style pointer target (`field.ptr`/`index.ptr` do not apply to it).

---

## 4. Instructions & Control Flow

Instruction format is `<op>.<type>`. Results must be bound (`name =`) if the instruction produces a value.

### 4.1 Operations

* **Math:** `add`, `sub`, `mul`, `neg`, `abs`, `sqrt` (Integers wrap modulo 2^N; no UB-on-overflow). Division/remainder (`udiv`, `sdiv`, `urem`, `srem`) trap on zero divisor; `sdiv`/`srem` additionally trap on `INT_MIN / -1`.
* **Overflow/saturating/widening:** `uaddo`/`saddo`/`usubo`/`ssubo`/`umulo`/`smulo` (return `i1`), `umulh`/`smulh` (high half of double-width product), `uadd_sat`/`sadd_sat`/`usub_sat`/`ssub_sat`. Legal only on `iN`/`vec[iN, W]`.
* **Bitwise & Shifts:** `and`, `or`, `xor`, `not`, `shl`, `lshr`, `ashr`, `rotl`, `rotr`, `ctlz`, `cttz`, `popcnt`. Shift counts mask to operand bit width — no UB, no trap.
* **Floats:** Strict IEEE-754-2019 semantics, round-to-nearest-ties-to-even, no dynamic rounding modes, no exception flags, no fast-math. `min.fN`/`max.fN` follow §9.6 `minimum`/`maximum`: NaN propagates quietly; `-0.0 < +0.0` is ordered. `fma` is the only contracted op and only written explicitly. `min.iN`/`max.iN` are illegal — use `smin`/`smax`/`umin`/`umax`.
* **Comparisons:** Yield `i1` or `vec[i1, N]`. Includes standard integer/float operators and raw address pointer comparisons (`eq.ptr`, `ult`, etc.) — pointers carry no provenance, so cross-object ordering comparisons are defined, if rarely meaningful.
* **Conversions:** Destination-explicit (e.g., `trunc`, `sext`, `stoint_sat.<iN>`). Trapping float-to-int (`stoint`/`utoint`) traps out of bounds (incl. ±Inf, NaN); saturating variants (`_sat`) clamp, NaN→0. `bitcast` between pointers and integers requires exact `usize` match, exact round-trip. `bitcast` is illegal on `valist`.

### 4.2 Calls & Syscalls

* `call`: Direct via identifier or cross-module qualified name.
* `call.<fnsig>`: Indirect call via a function pointer matching `fnsig`.
* `syscall.<type>`: Hardware trap using a `usize` system number and up to six scalar arguments (max seven operands total).
* `tailcall`: Reuses the caller's frame; the verifier ensures return types match and rejects `byval`/`sret`. **A `tailcall` targeting a variadic `fnsig` is rejected if the caller has an active (unclosed) `valist` from its own variadic parameter — the callee frame reuse would invalidate the still-live save area (§4.4).**
* **`noreturn`:** a direct call to a `noreturn` callee must be followed (after `loc`/comments) by `unreachable`, or itself precede a `trap`/`unreachable` terminator. A `readonly` callee must not write through any pointer reachable from its arguments/globals.

### 4.3 Blocks & The Join Convention

Functions are constructed of strictly labeled blocks ending in exactly one terminator (`br`, `br_if`, `switch`, `return`, `tailcall`, `trap`, `unreachable`). Entry block is implicit, unlabeled, unbranchable-to. Labels are function-scoped.

**Join Convention (Replacing Phi Nodes):**

1. **Assignment:** `name = op ...` creates a binding upon first occurrence, updates thereafter, in any block.
2. **Type Fixation:** The first assignment permanently fixes the type (parameters count as entry assignments).
3. **Definite Assignment:** Reading a name is valid *only* if every path from the entry block assigns it first. Within a block, most recent assignment; across blocks, whichever assignment last executed on the taken path.
4. **Loop-carried values** need no special form: an assignment before the loop plus reassignment in the body satisfies rule 3 on both edges.
5. Reading a name unassigned on some path is a verification error — no "undef" at the IR level (memory differs: uninitialized loads yield a frozen unspecified value, §5.2). **A `valist` binding follows the same rule but with an additional linear-use constraint: it must be `va_start`-initialized on every path before any `va_arg`/`va_end` reads it, and re-`va_start`-ing an already-started `valist` without an intervening `va_end` is a verification error (§4.4).**

### 4.4 Variadic Argument Access

A function whose `fnsig`/`fn-def` param-list ends in `...` may read its trailing arguments only through a `valist` cursor, using three dedicated opcodes. This is the sole mechanism for reaching variadic arguments — there is no other way to name them, matching the Join Convention's rule that only declared parameters get entry-block bindings.

* **`va_start.<fnsig> dst, last_named`** — Initializes the `valist` at `dst` (which must be the result of a prior `alloca.valist` in the same function) for reading the arguments following `last_named`, an identifier naming the function's final declared (non-variadic) parameter. `<fnsig>` must structurally match the enclosing function's own signature (self-referential; verified against the function's declared param-list and return type, not a cross-module reference). Illegal outside a variadic function. The actual codegen — spilling incoming register arguments to a save area, recording the incoming-stack-args pointer, or whatever the target ABI requires — is entirely the backend's responsibility; the opcode's meaning ("begin reading varargs after this point") is fixed across every target, per §1's strict-semantics principle.
* **`va_arg.<T> src`** — Reads the next variadic argument from the `valist` at `src` as type `T`, advances `src`'s internal cursor, and yields the value. `T` must be a scalar (`iN`/`fN`), `ptr`, or `vec[T,N]` type; `struct`/`array` are illegal directly (pass a `ptr` to the aggregate instead, matching how real C ABIs pass large varargs by reference under the hood). Reading past the actual number of arguments supplied by the call site is UB (added to §5.4, item 10).
* **`va_end src`** — Marks `src` as closed. Required before a `valist` can be legally re-`va_start`-ed (§4.3) or before the enclosing block's terminator if the function returns; a `valist` left open across a `return` is a verification error, not merely a leak, since it may correspond to live target-specific state (e.g. a register save area tied to the frame). No-op on targets whose ABI needs no cleanup, but never elidable at the source level — explicit always, matching the "no silent scope-exit behavior" stance taken with `entry`/`extern_c` overrides (§2.2).

---

## 5. Memory Model, Traps & UB

### 5.1 Memory & Pointers

* **Allocations:** `alloca.ptr` creates a fresh slot live for the invocation's lifetime; per execution, so slots accumulate per loop iteration. `alloca.valist` is the sole legal way to create a `valist` slot; it follows the same lifetime rule.
* **Access:** `load`, `store` (Standard non-atomic), and `load_vol`, `store_vol` (Volatile — may not be elided, duplicated, reordered against other volatile accesses, or widened/narrowed; not atomic). Neither applies to `valist`.
* **Bulk Ops:** `memcopy` (Non-overlapping — overlap is UB), `memmove` (Overlap-safe), `memset` (`len` is `usize`, `byte` is `i8`).
* **Pointer Navigation:** `field.ptr` computes struct field addresses (struct may be local or imported); `index.ptr` performs `usize` pointer arithmetic. Address calculation wraps normally. Neither applies to `valist`.
* **Atomics:** Require natural alignment (misalignment is UB, not a trap); legal on `i8`–`i128` (per tier) and `ptr`. `atomic_load`/`atomic_store`/`atomic_add|sub|and|or|xor|xchg`/`cmpxchg`/`fence`, each with ordering constraints (loads exclude `release`/`acqrel`; stores exclude `acquire`/`acqrel`; `cmpxchg` failure-ordering not `release`/`acqrel`, not stronger than success-ordering). A data race (≥1 write, ≥1 non-atomic, concurrent) is UB.

### 5.2 Defined Behaviors (never UB, never trapping)

Integer wraparound on `add`/`sub`/`mul`/`neg`/`abs`; masked shift counts; IEEE float results including NaN/Inf; pointer comparisons and pointer↔integer `bitcast` round-trips; `index.ptr`/`field.ptr` wraparound (usability separate, see UB #1); loading a fresh, uninitialized-but-owned `alloca` yields an unspecified but frozen value — not poison, no UB propagation.

### 5.3 Traps

Traps deterministically halt execution — not catchable, resumable, or removable by codegen.

* **Triggers:** Division/remainder by zero, `sdiv`/`srem` of `INT_MIN` by `-1`, out-of-range float-to-int casting, or explicit `trap` terminators.

### 5.4 Exhaustive Undefined Behavior (UB)

There are exactly 10 ways to trigger UB:

1. Accessing outside live object bounds.
2. Using an `alloca` address after its invocation returns.
3. Violating declared or natural alignment (Atomics *always* require natural alignment).
4. Overlapping `memcopy` operands.
5. Data races (Concurrent access, ≥1 write, ≥1 non-atomic).
6. Executing an `unreachable` instruction.
7. Returning from a `noreturn` function, or writing via a `readonly` function.
8. Calling a function with a mismatched signature.
9. Modifying memory that a `byval` copy or `sret` destination is restricted from aliasing.
10. `va_arg`-reading a `valist` past the number of variadic arguments actually supplied at the call site.

---

## 6. ABI, Layout & Initialization

### 6.1 Layout & Attributes

* Structs align to target C ABIs (fields in order, naturally aligned, padded to the largest alignment). Arrays have no inter-element padding. This applies identically whether the struct is local or imported. `valist`'s layout is target-defined and deliberately outside this rule — it is never a struct field, array element, or otherwise embedded in a layout-visible position.
* **`byval[S]`:** The caller passes a `struct S` by value (codegen copies it); the caller's object must remain live and unmutated.
* **`sret[S]`:** Used on the first `ptr` argument of a `void` function to dictate where it writes its return struct.

### 6.2 Constants & Globals

* `const`: Compile-time scalars (`iN`, `fN`, `ptr`, `vec`) yielding a direct value, no runtime storage.
* `global`: Mutable module-level storage. Can be initialized with literals, `zero`, byte strings, aggregate lists, or `addr ident` (relocated pointers to earlier functions/globals). `addr` cannot be used on `tls` (Thread Local Storage) globals, nor on an `extern fn` (its group would have to appear before `global`, which the fixed section order forbids).
* **Restriction:** `global` initializers may not reference `const`s, perform arithmetic, or take offsets into objects — only the literal/`zero`/`addr`/aggregate forms above are legal. `valist` is not a legal `global`/`const` type — it exists only as function-local, call-frame-scoped state (§4.4).

### 6.3 Symbols & Mangling

* **No `namespace`:** exports get a bare, unmangled C symbol (`fn`/`global` only — `struct`/`const`/`fnsig` never had a symbol to mangle).
* **`namespace` declared:** `fn`/`global` exports mangle by default, length-prefixed Itanium-style, to avoid naive-concatenation collisions (e.g. module `a_b` export `c` vs. module `a` export `b_c` both naively giving `a_b_c`):

  ```
  namespace "acme/net", module "http", export "get"  →  _M4acme3net4http3get
  module "mathlib", export "add"                     →  _M7mathlib3add
  ```

* **Carve-outs:** `entry`/`extern_c` functions emit bare symbols even in a namespaced module (`module main`'s `entry` export emits bare `main`). Mangling never depends on whether an export is actually imported by anyone — that would make a symbol's name depend on the link graph.
* `struct`/`const`/`fnsig` exports are **never** mangled — they never produce a symbol at all.

---

## 7. Targets & Profiles

### 7.1 Target Triple

**Format:** `target <arch> <os> <abi> [tiers]`. There is no vendor field — a vendor identifies who ships a target, not what the CPU is.

**`arch`** — real silicon only (bytecode formats, VM targets, other IRs excluded). Fixes pointer width and endianness.

| Canonical | Silicon | Rejected aliases |
| --- | --- | --- |
| `x86`, `x86_64` | Intel/AMD | `i386`, `i686`, `amd64`, `x64` |
| `arm`, `armeb` | 32-bit ARM | `arm32` |
| `aarch64`, `aarch64_be` | 64-bit ARM (`le` default, omitted) | `arm64`, `arm64e`, `arm64ec` |
| `riscv32`, `riscv64` | RISC-V | `rv32`, `rv64` |
| `powerpc`, `powerpc64`, `powerpc64le` | POWER/PowerPC | `ppc`, `ppc64` |
| `mips32`, `mips32el`, `mips64`, `mips64el` | MIPS | `mips`, `mipsel` (bare rejected) |
| `loongarch64` | LoongArch | — |
| `s390x` | IBM Z | `systemz` |

Vendor-specific variants (Apple pointer authentication, MS's x86_64-shaped AArch64 emulation ABI) are an `abi` token or feature-tier flag — never a separate table entry.

**`os`:**

| Canonical | Rejected aliases |
| --- | --- |
| `linux` | — |
| `macos`, `ios`, `watchos`, `tvos`, `visionos` | `darwin` |
| `windows` | `win32`, `nt` |
| `android` | — |
| `freebsd`, `netbsd`, `openbsd` | `bsd` |
| `uefi` | — |
| `none` | `freestanding`, `bare`, `baremetal` |

**`abi`:**

| Canonical | Meaning |
| --- | --- |
| `gnu` | glibc-based |
| `musl` | static-friendly libc |
| `msvc` | Windows/MSVC calling convention + runtime |
| `eabi`, `eabihf` | ARM embedded, soft/hard float |
| `aapcs64` | AArch64 variant with stack-passed variadics |
| `macho` | Apple convention for targets without an OS-specific ABI above |

**Feature Tiers:** Orthogonal to `(arch, os, abi)`; gate hardware vector width legality, masked/gather/scatter availability, wide-atomic (`i128` cmpxchg), and TLS convention on `os = none`. `vec[T, N]` is legal only if N fits the selected tier.

**Aliases:** Resolve *only* at the build-system boundary, in one lookup table outside the IR grammar — the IR's own grammar never accepts an alias, in-file or via build flags. Library-name spellings follow the same rule: `dylib`, `dll`, `so`, `dynamic` are rejected aliases for `shared` (§7.2).

**In-file declaration**, required whenever the module has a `link` section:

```vir
module simd_kernel
target x86_64 linux gnu [avx2]
```

A conflicting build-invocation target is a build-time error, not a verifier error.

### 7.2 Link Dependencies

Every library the binary needs is declared by exactly one `link` line (`static`/`shared`/`framework`); every imported symbol is attributed to its provider by the `extern` group listing it. No external linker flags. `framework` is rejected at build time on non-Mach-O targets. Short names derive `libX.so`/`libX.dylib`/`X.dll` etc.; exact names (containing `.` or a path separator) are emitted verbatim and must match the kind's extension.