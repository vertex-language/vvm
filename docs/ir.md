# Vertex IR — Language Specification (v1.3)

**File Extension:** `.vir`

Vertex IR is Vertex's unified, modern bytecode — the only artifact `vvm` ever accepts and executes. A CPU-only intermediate representation featuring structured control flow, flat basic blocks, opcode-first typed instructions, and a value-naming convention that replaces phi nodes without needing a stack machine.

## Scope & Design Goals

* **CPU-Only & No Runtime:** Targets real CPUs. No runtime, garbage collection, exceptions, sandboxing, or support libraries.
* **Hardware Mapping:** All types map to hardware register classes (`iN`, `fN`, `ptr`, `vec`). Memory is accessed via raw pointers.
* **No Built-In Heap:** Heap allocation requires standard `extern fn` calls (e.g., `malloc`). The only built-in allocation is stack-based (`alloca.ptr`).
* **C ABI Boundary:** The module uses the standard C ABI at call boundaries (§7).
* **Flat Control Flow:** Blocks are labeled and structured but do not nest.
* **Join Convention:** No strict SSA phi nodes. Values are merged by assigning the same name in multiple predecessor blocks (§5).
* **One Behavior Per Opcode:** No flag-modified variants, no fast-math relaxations, no target-dependent semantics for the same spelling. Where hardware differs, the IR picks one behavior and codegen pays the cost.
* **Minimal Undefined Behavior:** UB exists only where ruling it out would impose per-instruction runtime cost (§6). Everything else is defined, trapping, or an unspecified-but-frozen value.
* **Self-Contained Modules:** All link dependencies are declared in the module itself (§7.4); no external linker flags. A module with a `link` section is self-describing about which target it was written for (§1.2 rule 10, §10.6).
* **Clean Syntax:** No sigils or unnecessary punctuation. `[]` is used for types, `()` for parameters.

---

## 1. Module Grammar & Ordering

A module is a sequence of lines. Line breaks are significant: **one declaration or one instruction per line**, no statement separators, no line continuations. Indentation is conventional (function bodies and extern groups are indented) but carries no meaning.

### 1.1 Grammar

```text
module        := module-header
                 target-decl?
                 struct-decl*
                 fnsig-decl*
                 const-decl*
                 global-decl*
                 link-decl*
                 extern-group*
                 fn-def*

module-header := "module" ident

target-decl   := "target" arch os abi? tier-list?
arch          := ident            // canonical arch name, §10.1
os            := ident            // canonical os name, §10.2
abi           := ident            // canonical abi name, §10.3
tier-list     := "[" ident ("," ident)* "]"   // feature-tier flags, §10.4

struct-decl   := "struct" ident "(" field ("," field)* ")"
field         := ident type

fnsig-decl    := "fnsig" ident "(" type-list? ")" type
type-list     := type ("," type)* ("," "...")?

type          := "i" [1-9][0-9]* | "f" (16|32|64) | "ptr" | "void"
               | "vec[" type "," int-literal "]"
               | "struct" ident
               | "array[" type "," int-literal "]"

const-decl    := "const"  ident type "=" literal
global-decl   := "export"? "global" "tls"? ident type ("align" int-literal)? "=" const-init

link-decl     := "link" lib-kind string-literal
lib-kind      := "static" | "shared" | "framework"

extern-group  := "extern" string-literal? ":"
                 extern-fn*
                 "end"
extern-fn     := "fn" ident "(" param-list? ")" type fn-attr*

fn-def        := "export"? "fn" ident "(" param-list? ")" type fn-attr* ":"
                 entry-block
                 block*
                 "end"

param-list    := param ("," param)* ("," "...")?
param         := ident type param-attr*
param-attr    := "byval" "[" ident "]"
               | "sret" "[" ident "]"
fn-attr       := "noreturn" | "readonly" | "inline" | "noinline" | "cold"

const-init    := literal
               | "zero"
               | "addr" ident
               | "(" const-init ("," const-init)* ")"      // aggregate

entry-block   := body-line* terminator
block         := label-line body-line* terminator
label-line    := ident ":"
body-line     := inst | loc-line
loc-line      := "loc" string-literal int-literal int-literal?   // file, line, col

inst          := ident "=" op operand-list? align-clause? // value-producing
               | op operand-list? align-clause?           // void or niladic

op            := ident ("." (ident | type))?
operand-list  := operand ("," operand)*
align-clause  := "," "align" int-literal

terminator    := "br" label
               | "br_if" operand "," label "," label
               | "switch" operand "," label ("," int-literal label)*
               | "return" operand?
               | "tailcall" ident ("," operand)*
               | "tailcall" "." ident operand ("," operand)* // indirect, suffix = fnsig
               | "trap"
               | "unreachable"

operand       := ident | literal | type | ordering
ordering      := "relaxed" | "acquire" | "release" | "acqrel" | "seqcst"

literal       := int-literal | float-literal | string-literal | bool-literal | "null"
int-literal   := "-"? [0-9]+
float-literal := "-"? [0-9]+ "." [0-9]+ ("e" "-"? [0-9]+)? | "NaN" | "Inf" | "-Inf"
string-literal:= "\"" [^"]* "\""
bool-literal  := "true" | "false"

```

*Note on operand positions:* `field.ptr`, `index.ptr`, and indirect calls consume idents that name compile-time entities (a struct, a field, a type, a `fnsig`). These are ordinary idents in the grammar; the verifier gives them their meaning (§4, §9).

### 1.2 Ordering Rules

The section order is **fixed and enforced**: header, target, structs, fnsigs, consts, globals, links, externs, functions. Any section may be empty, but sections never interleave — a `struct` after the first `const`, a `link` after the first `extern` group, a `target` after the first `struct`, or a `global` after the first `fn`, is a verifier error. Together with the flat namespace, this makes the module verifiable in a single forward pass.

1. **Header first.** Exactly one `module` line, and it must be the first non-comment line. A second `module` line anywhere is rejected.
2. **Declare before use.** There are no forward references of any kind. A `struct` field may name another struct only if that struct was declared on an earlier line. A `const`/`global` initializer may reference only previously declared names (§8). An `extern` group header may reference only a previously declared `link` string (§7.4). A `call` may name only a previously declared `extern fn` or `fn`. (Self-referential data structures are unaffected: `ptr` is untyped and never names its pointee.)
3. **Recursion.** Direct self-recursion is legal (a function's own name is in scope inside its body). Mutual recursion between two `fn` bodies is not *directly* expressible under rule 2; the supported pattern is a `global ptr` slot populated at runtime. A function name used in operand position yields its address as a `ptr` (§4, Addresses), so an initialization function defined after both parties can store the later function's address into the slot before either runs. Static initialization via `addr` (§8) also works when declaration order permits.
4. **Initializers.** `const` takes exactly one scalar literal. `global` takes one `const-init` (§8): a literal, `zero`, an `addr` reference to an earlier declaration, or an aggregate initializer. No expressions, no arithmetic. An optional `align N` clause (N a power of two, ≥ the type's natural alignment) over-aligns the global's storage.
5. **Variadics.** `...` may appear only in an `extern fn` parameter list or a `fnsig`, only once, and only as the final entry. It is rejected in `fn` definitions.
6. **Linkage.** Every `fn` and `global` is module-internal unless marked `export`. Exported names have external linkage with C symbol naming; internal names are invisible to the linker and may be freely renamed or eliminated. `extern` declarations are always imports. `const` and `struct` and `fnsig` are compile-time entities and never have linkage.
7. **Thread-local storage.** `global tls` declares one instance of the global per thread, initialized per thread from its initializer. `addr` of a `tls` global (whether as an initializer or operand) yields the *current thread's* instance and is therefore forbidden in static initializers (§8). Targets with `os = none` reject `tls` unless the feature tier supplies a TLS register convention.
8. **Link declarations.** Each `link` line declares one library dependency (§7.4). Duplicate dependencies — after short-name derivation — are rejected. A `link` line with no matching `extern` group is legal (a link-only dependency, e.g. a framework whose symbols are reached indirectly).
9. **Extern groups.** An `extern "X" :` group's string must byte-for-byte match a previously declared `link` string **as written** (short names match short names, exact names match exact names). Each `link` string may be referenced by at most one group. An anonymous group (`extern :`, no string) imports from the target's default namespace (e.g. the process's already-loaded images on hosted OSes); anonymous groups are rejected on `os = none` and `os = uefi` unless the environment defines a symbol source. Groups contain only `extern-fn` lines, one per line; an empty group is rejected.
10. **Target declaration.** A module may declare its own build target in-file via `target arch os abi? tier-list?`, appearing at most once, immediately after `module` and before any other section. `arch`/`os`/`abi` must be canonical spellings only (§10.1–§10.3); aliases are rejected at this position exactly as they are rejected everywhere else in the grammar (§10.5). Feature-tier flags in `tier-list` are drawn from the target's supported tiers (§10.4). **A `target-decl` is mandatory whenever a `link-decl` is present** — since a module with a `link` section is already per-target by construction (§7.4), an in-file `target` line makes that fact explicit and checkable rather than leaving it implicit in which `link` strings happen to resolve. Pure-compute modules (no `link` section) may omit `target` entirely and remain fully target-independent, buildable for any triple via build flags alone. If a build is invoked with a target flag that conflicts with a module's own `target-decl`, this is a build-time error, not a verifier error — the file states what it was written for, the invocation states what it's being built for, and mismatches must be caught before codegen.

### 1.3 Function Body Rules

1. **Entry block is implicit and unlabeled.** The lines between the signature's `:` and the first label form the entry block. It cannot be branched to; consequently no label may resolve to it.
2. **Every block ends in exactly one terminator**, including the entry block. The first terminator ends the block; any instruction after it and before the next label (or `end`) is rejected as unreachable.
3. **Labels introduce blocks.** A label sits alone on its line with a trailing `:` and must be followed by at least one line before the next label or `end` (its terminator at minimum). Empty blocks are rejected.
4. **Labels are function-scoped names in the flat namespace.** They must be unique module-wide like every other name, and branches may target only labels within the same function.
5. **`end` closes the function** and may appear only immediately after a terminator line.
6. **Result names are not optional.** An instruction that produces a value must be written with `name =`; an instruction whose result type is `void` must not be. Discarding a value requires assigning it (the verifier does not require values to be read — only that reads are definitely assigned per the Join Convention, §5).
7. **Operand positions are values, types, literal strings/numbers, or orderings.** The strict definition of `operand` allows instructions to cleanly parse types (for `index.ptr`), memory alignments, or memory orderings natively. `global` names used as operands yield a `ptr`; `const` names yield their value; `fn` and `extern fn` names yield their address as a `ptr` (§4, Addresses).
8. **`loc` lines** may appear as standalone lines between any instructions or at the start/end of a block. They set the source location for subsequent lexically sequential instructions until the next `loc` line or `end`, carrying over seamlessly across block boundaries. They have no semantic effect and are ignored by the verifier beyond syntax (§11).

* `struct`: Pure data layout declaration; no body. Layout is fixed by §7.
* `fnsig`: A named function signature, used as the type suffix of indirect calls so the verifier can type-check them.
* `global`: Mutable module-level storage. Access yields a **pointer**.
* `const`: Immutable compile-time scalar constant. Access yields the **value**.
* `link`: Declares one library dependency of the module (§7.4). Not a symbol; has no linkage of its own.
* `extern` group: Declares imported functions and names the dependency that provides them. The functions live in the flat namespace like any other name.
* `target`: Declares the build triple (and optional feature tiers) this module was authored for (§1.2 rule 10, §10.6). Purely declarative — it does not affect verification of the compute sections, only cross-checking against the build invocation and gating of target-specific constructs (`tls` on `none`, `framework` links, vector tiers).

Modules remain target-independent in their compute sections when no `target-decl` or `link` section is present: target selection is otherwise a build flag, not module syntax (§10). Modules containing `link` declarations are per-target by construction (§7.4) and must carry a matching `target-decl`. All names share **one flat namespace** (no shadowing).

---

## 2. Types

* **Integers:** `i1`, `i8`, `i16`, `i32`, `i64`, `i128` (`i1` is canonical boolean).
* **Floats:** `f16`, `f32`, `f64`.
* **Pointer:** `ptr`. Untyped. Its width is the target's address width; the integer type of that width is referred to in this spec as **`usize`** (a documentation alias, not IR syntax — write `i64` or `i32` per target-width-generic code's build configuration).
* **Aggregates (Memory-Only):** `struct <ident>`, `array[T, N]`. Aggregates can never be held in named values; they exist only behind pointers (and in initializers, §8).
* **Vector:** `vec[T, N]` (Width must match a hardware vector size, gated by feature tier §10.4).
* **Special:** `void`.

*Note: Parameterized types strictly use square brackets `[]` and integer literals for length.*

---

## 3. Lexical Structure

* **Identifiers:** Bare names: `[A-Za-z_][A-Za-z0-9_]*`. No sigils.
* **Keywords:** `module`, `target`, `struct`, `fnsig`, `const`, `global`, `export`, `tls`, `extern`, `link`, `shared`, `static`, `framework`, `fn`, `end`, `zero`, `addr`, `loc`, `align`, attribute names, terminators, and orderings (`relaxed`, `acquire`, `release`, `acqrel`, `seqcst`) are reserved and may not be used as identifiers.
* **Roles by punctuation:** trailing `:` marks a label (and opens a function or extern group); `=` binds a result or initializer; `.` joins an opcode to its type suffix and appears nowhere else outside float literals and link strings.
* **Literals:** Typed by context — integers (`42`, `-7`), floats (`1.0`, `2.5e3`, `NaN`, `Inf`, `-Inf`), booleans (`true`, `false` as `i1`), `null` (as `ptr`), byte strings (`"bytes\0"`, legal only as `array[i8, N]` initializers, §8), and vector literals (`(0, 4, 1, 5)`). A literal is only legal where the expected type is unambiguous from the opcode suffix or declaration.
* **Orderings:** Treated contextually as keywords that parse into the `ordering` operand used only by atomic instructions and `fence` (§4).
* **Comments:** `//` to end of line. A line that is blank or comment-only is ignored everywhere, including inside function bodies and extern groups. Unlike `loc` lines, comments may trail on the same line as an instruction.

---

## 4. Instructions

Format: **`<op>.<type>`**. The type suffix represents one of three things:

1. **Operand type:** Arithmetic, bitwise, compare, `mov`, `select`, `load`/`store`, atomics, vectors, intrinsics.
2. **Destination type:** Conversions (`trunc`, `sext`, `bitcast`, etc.); for indirect `call`/`tailcall` the suffix is a **`fnsig` name**, not a type.
3. **Literal `.ptr`:** Address producers (`alloca`, `field`, `index`).

### Arithmetic & Bitwise

* **Math:** `add`, `sub`, `mul`, `udiv`, `sdiv`, `urem`, `srem`, `neg`, `abs`, `sqrt`.
* **Integer semantics:** All `iN` add/sub/mul/neg **wrap modulo 2^N**. There is no undefined-overflow variant. `abs` on `iN` is signed; `abs(INT_MIN)` wraps to `INT_MIN`. Division and remainder trap on a zero divisor; `sdiv`/`srem` additionally trap on `INT_MIN / -1` (§6).
* **Overflow predicates:** `uaddo`, `saddo`, `usubo`, `ssubo`, `umulo`, `smulo` — take the same two operands as the corresponding wrapping op and return `i1`: true iff the wrapping result differs from the infinitely-ranged result. Pair with the wrapping op to build checked or multi-word arithmetic without pattern-matching compares. Legal on `iN` and `vec[iN, W]`.
* **Widening Multiply:** `umulh`, `smulh` — multiply two operands at twice the operand width and return only the high half of the full-width product (unsigned and signed respectively). Required for multi-word arithmetic without synthesizing it from narrower ops.
* **Saturating Add/Sub:** `uadd_sat`, `sadd_sat`, `usub_sat`, `ssub_sat` — integer add/subtract that clamps to the representable range instead of wrapping or trapping on overflow (unsigned and signed variants). Legal on both scalar `iN` and `vec[iN, W]` operands.
* **Bits:** `and`, `or`, `xor`, `not`, `shl`, `lshr`, `ashr`, `rotl`, `rotr`, `ctlz`, `cttz`, `popcnt`.
* **Shift counts** are masked to the operand's bit width (`count mod N`). One behavior everywhere; no UB, no trap.

### Float Semantics

All float operations follow IEEE-754-2019, round-to-nearest-ties-to-even, with no access to the floating-point environment: no dynamic rounding modes, no exception flags, no fast-math relaxations. `fma` (below) is the only contracted operation and only when written explicitly — codegen may never contract a separate `mul` + `add` into an FMA.

`min.fN` and `max.fN` follow IEEE-754-2019 §9.6 `minimum`/`maximum`, not the older, looser `minNum`/`maxNum` behavior:

* **NaN propagates.** If either operand is NaN, the result is a quiet NaN, regardless of whether the other operand is a number.
* **Signed zero is ordered.** `-0.0` compares less than `+0.0`; `min(-0.0, +0.0)` returns `-0.0`, `max(-0.0, +0.0)` returns `+0.0`.
* This is a deliberate choice to avoid the ambiguity that split LLVM into two incompatible intrinsic families (`minnum`/`maxnum` vs. `minimum`/`maximum`) with different NaN and zero handling. Vertex IR has exactly one behavior per opcode name; there is no NaN-ignoring variant.

`min.iN`/`max.iN` on bare integer types are **not legal** — signedness is not inferable from the type alone, so integer min/max must always go through the explicit signed/unsigned intrinsics below (`smin`/`smax`/`umin`/`umax`), removing the redundancy between a generic `min`/`max` and the signed/unsigned intrinsic forms.

### Comparisons (Returns `i1` or `vec[i1, N]`)

* `eq`, `ne`, `slt`, `sgt`, `sle`, `sge`, `ult`, `ugt`, `ule`, `uge` (Int).
* `lt`, `gt`, `le`, `ge` (Float).
* **Pointers:** `eq.ptr`, `ne.ptr`, and the unsigned orderings `ult`/`ule`/`ugt`/`uge` on `ptr` are legal and compare raw addresses. Vertex IR pointers carry **no provenance**: a pointer is its address (§6). Ordering comparisons between pointers into different objects are therefore defined (they compare addresses), if rarely meaningful.

### Selection

* `select.<type>` — value-level select: given an `i1`/`vec[i1,N]` predicate and two operands of the destination type, yields the first operand where the predicate is true and the second where it is false. Elementwise on vectors. Both operands are always evaluated; `select` never traps on the not-taken side because operands are values, not expressions. Subject to the same Join Convention rules as any other value producer.

### Memory & Addresses

* **Alloc/Move:** `alloca.ptr`, `load`, `store`.
* **`alloca` lifetime:** each *execution* of an `alloca.ptr` instruction allocates a fresh slot; every slot remains live until the enclosing function invocation returns. An `alloca` executed in a loop therefore allocates per iteration and accumulates stack. Frontends should place `alloca`s in the entry block unless per-iteration slots are genuinely intended.
* **Bulk memory:** `memcopy dst, src, len` (regions must not overlap — overlap is UB), `memmove dst, src, len` (overlap-safe), `memset dst, byte, len`. `len` is a `usize`-width integer; `byte` is `i8`. All three are void.
* **Volatile:** `load_vol.<T>` / `store_vol.<T>` — identical typing to `load`/`store`, but the access is a side effect: it may not be elided, duplicated, reordered against other volatile accesses, or widened/narrowed. Required for MMIO on `none`/`uefi` targets. Volatility does not imply atomicity.
* **Pointers:**
* `p2 = field.ptr p, S, f` — `p` is a `ptr` to a `struct S`; `S` must name a declared struct and `f` one of its fields. Yields the address of field `f` per §7 layout.
* `q = index.ptr p, T, i` — `p` is a `ptr` to an element of type `T` (any sized type, including `struct S` and `array[T, N]`); `i` is a `usize`-width integer treated as **signed**. Yields `p + i * sizeof(T)`. Address arithmetic wraps like any `usize` arithmetic; whether the result is *usable* is governed solely by §6.3(1).
* **Addresses:** a `fn`/`extern fn` name in operand position yields its address as `ptr` (the callee of a direct `call` is not an operand position). A `global` name in operand position yields its address as `ptr`, as before. There is no address-of opcode; names *are* the address producers.
* **Alignment:** every `load`/`store`/`load_vol`/`store_vol` may carry a trailing `, align N` (N a power of two). Absent the clause, the access asserts the **natural alignment** of its type. An access whose address does not satisfy its stated alignment is UB (§6). `alloca.ptr` may also carry `, align N` to over-align the slot; its default is the natural alignment of nothing in particular — the frontend must state what it needs (`alloca.ptr size, align N`; bare `alloca.ptr size` yields `usize`-aligned storage).

### Atomics

All atomics require naturally aligned addresses (misalignment is UB, not a trap) and are legal on `i8`–`i128` (per target tier) and `ptr`.

* `atomic_load.<T> p, <ord>` — ord ∈ `relaxed | acquire | seqcst`.
* `atomic_store.<T> p, v, <ord>` — ord ∈ `relaxed | release | seqcst`. Void.
* `atomic_add | atomic_sub | atomic_and | atomic_or | atomic_xor | atomic_xchg .<iN> p, v, <ord>` — read-modify-write; returns the **old** value; ord ∈ any of the five orderings.
* `cmpxchg.<T> p, expected, desired, <ord_success>, <ord_fail>` — returns the **old** value; the operation succeeded iff `old == expected` (compare with `eq` yourself — no aggregate result). `ord_fail` may not be `release`/`acqrel` and may not be stronger than `ord_success`.
* `fence <ord>` — ord ∈ `acquire | release | acqrel | seqcst`. Void, niladic-plus-ordering.

**Memory model:** C11/C++11 semantics. Atomic operations with the given orderings establish happens-before edges exactly as in C11; `seqcst` operations additionally participate in a single total order. A **data race** — two concurrent accesses to the same location, at least one a write, at least one non-atomic — is UB (§6). Plain `load`/`store` are non-atomic. Volatile accesses are not atomic and do not synchronize.

### Conversions (Suffix is destination type)

Integer↔float conversions are signedness-explicit; there are no signedness-ambiguous conversion opcodes.

* **Width/domain:** `trunc`, `sext`, `zext`, `fdemote`, `fpromote`, `bitcast`.
* **Int → float:** `sfromint.<fN>` (source read as signed), `ufromint.<fN>` (source read as unsigned). Rounding is round-to-nearest-ties-to-even, as everywhere.
* **Float → int, trapping:** `stoint.<iN>` / `utoint.<iN>` — trap if the source float value, truncated toward zero, is outside the representable range of the destination read as signed/unsigned respectively (including ±Infinity and NaN). This is the sole defined behavior for the plain opcodes — they never wrap and never silently saturate.
* **Float → int, saturating:** `stoint_sat.<iN>` / `utoint_sat.<iN>` — same conversion, but clamps out-of-range values to the destination's signed/unsigned min/max instead of trapping, and maps NaN to `0`. Use these whenever a non-trapping conversion is required (e.g., untrusted input, SIMD-lane conversions where a single lane trapping is unacceptable).
* **Pointer/integer conversion:** `bitcast.ptr` from the `usize`-width integer and `bitcast.<usize-int>` from `ptr` are the only pointer/integer conversions. Because pointers carry no provenance, a round-trip is exact and the resulting pointer is valid for whatever object the address lies within (§6).

### Vectors

* `splat`, `extract`, `insert`, `shuffle`.
* **`shuffle`:** `r = shuffle.vec[T,N] a, b, (m0, ..., mN-1)` — `a` and `b` are `vec[T, M]` values of the same type; the mask is a **vector literal** (compile-time constant, never a runtime value) of N integer lane indices, each in `[0, 2M)`, selecting from the lane concatenation `a ‖ b`. Result width N need not equal source width M, but both must be tier-legal.
* **Masked/scatter-gather (tier-gated, §10.4):** `masked_load.vec[T,N] p, mask, passthru` (lanes where `mask` is false read nothing and take `passthru`'s lane), `masked_store p, mask, v` (false lanes write nothing), `gather.vec[T,N] pvec, mask, passthru`, `scatter pvec, mask, v` (`pvec` is `vec[ptr]` — represented as `vec[usize-int, N]` addresses, bitcast per-lane by codegen). Disabled lanes cannot fault. These opcodes are rejected by the verifier unless the selected feature tier provides native masking.

### Intrinsics (Must compile to 1-2 CPU instructions; no libcalls)

* **Floats:** `fma` (fused), `copysign`, `floor`, `ceil`, `trunc_f`, `nearest` (ties-to-even).
* **Ints:** `smin`/`smax`, `umin`/`umax`, `bswap` (illegal on `i8`), `bitrev`.
* **Reductions:** `reduce_add`, `reduce_min`, `reduce_max`, `reduce_and`, `reduce_or`, `reduce_xor`.
* **Hints:** `prefetch` (advisory).

*(Note: `abs` lives solely under Math above — it is core arithmetic, not an intrinsic.)*

### Calls & Control

* `call` (direct: callee is a previously declared `fn`/`extern fn` name) / `call.<fnsig>` (indirect: suffix names a `fnsig`; first operand is the callee `ptr`, remaining operands are checked against the signature's parameter types, and the result type is the signature's return type).
* **Variadic Calls:** Arguments matching `...` require **manual type promotion** (e.g., `f32` to `f64`, or widening sub-`i32` ints). The IR does zero implicit conversions.
* **Attributes at call boundaries:** calling a `noreturn` function makes everything after the call unreachable — the block must still end in a terminator, conventionally `unreachable`. A `readonly` function must not write through any pointer reachable from its arguments or globals; violating this from the callee side is UB.

### Inline Assembly (reserved, minimally specified)

* `asm "template", operand*` (void) and `r = asm.<type> "template", operand*` (single result). The template is a byte string in the target's native assembly syntax; operand-substitution and clobber syntax inside the template are **target-defined** and specified per-target in a codegen appendix, not by this document. The verifier checks only arity-independent rules (result naming, operand well-formedness). `asm` is a full optimization barrier: no motion across it, all memory considered clobbered. Portable modules should not contain `asm`; it exists for `none`/`uefi` targets and will be tightened in v1.1.

---

## 5. Control Flow & Join Convention

* Every block ends in exactly one terminator: `br`, `br_if`, `switch`, `return`, `tailcall`, `trap`, or `unreachable`.
* No fallthrough between blocks.
* **`br_if cond, then_label, else_label`** — `cond` must be `i1`. Branches to the first label if true, the second if false. The two labels may be identical.
* **`switch v, default_label, c0 l0, c1 l1, ...`** — `v` is any `iN` (not `ptr`, not float). The first label is the **default**. Each case is an integer literal of `v`'s type paired with a label; case values must be **unique** and representable in `v`'s type. Matching is exact bit equality; on no match, control transfers to the default. Zero cases is legal (an unconditional branch spelled verbosely; the condition operand is evaluated but discarded).
* **`tailcall`** is a terminator, not an instruction: `tailcall callee, args...` (direct) or `tailcall.<fnsig> ptr, args...` (indirect). The callee's return type must equal the enclosing function's return type; its parameters must not be `byval`/`sret` (those pin caller stack). A `tailcall` is a **guaranteed** tail call — codegen must reuse the caller's frame, and the verifier rejects any `tailcall` it cannot prove eligible. The callee returning transfers directly to the caller's caller.
* **`trap`** is a terminator that deterministically halts execution with the same semantics as a trapping instruction (§6.1). It is the defined way to halt — including on `none`/`uefi` targets, where there is no aborting extern to call.
* **`unreachable`** asserts the point is never executed. Executing it is UB (§6). Use `trap` if a defined halt is wanted.

### Join Convention (normative)

Named values are **mutable bindings** in a single per-function scope (which shares the module's flat namespace):

1. **Assignment.** `name = op ...` creates the binding on its first textual occurrence and updates it thereafter. A name may be assigned any number of times, in any blocks.
2. **Type fixation.** Every assignment to a given name must produce the identical type; the type is fixed by the first textual assignment (parameters count as assignments of their declared type at entry).
3. **Read validity (definite assignment).** A read of `name` at instruction *i* in block *B* is valid iff **every** path from function entry to *i* contains an assignment to `name` before *i*. Within a block, a read observes the most recent prior assignment (program order); across blocks, the value flowing in is whichever assignment was last executed on the path taken — this is the phi replacement.
4. **Loop-carried values** need no special form: an assignment before the loop plus a re-assignment in the loop body satisfies rule 3 on both the entry edge and the back edge.
5. **Verifier algorithm (informative).** Standard forward must-analysis: for each block compute the set of definitely-assigned names at entry as the **intersection** over all predecessors' exit sets; initialize unvisited blocks to the full name set (⊤); the entry block starts with exactly the parameter names; iterate to a fixpoint; then scan each block linearly, adding assignments and checking reads. Rejected reads are reported as "read of possibly-unassigned value."
6. Reading a name that is never assigned on some path is a verification error — there is no "undef" value at the IR level. (Memory is different: loading uninitialized *memory* yields a frozen unspecified value, §6.)

---

## 6. Memory Model, Traps & Undefined Behavior

### 6.1 Traps

A **trap** deterministically halts execution at the trapping instruction. Traps are not exceptions: they cannot be caught, resumed, or observed by the program. On hosted OSes a trap terminates the process abnormally (implementation: an aborting instruction such as `ud2`/`brk`); on `none`/`uefi` it executes the target's canonical trap instruction and what happens next is the platform's business. Codegen may never remove a trap whose instruction executes, and may never replace trapping behavior with wrapping/saturating behavior (or vice versa).

Trapping operations, exhaustively: `udiv`/`sdiv`/`urem`/`srem` with zero divisor; `sdiv`/`srem` of `INT_MIN` by `-1`; `stoint`/`utoint` out of range (incl. ±Inf, NaN); the `trap` terminator.

### 6.2 Defined behaviors (never UB, never trapping)

* Integer `add`/`sub`/`mul`/`neg`/`abs` wrap modulo 2^N.
* Shift counts are masked to the operand width.
* Float operations produce IEEE results including NaN/Inf; no float op traps.
* Pointer comparisons compare addresses; pointer↔integer `bitcast` round-trips exactly; `index.ptr`/`field.ptr` address arithmetic wraps (usability of the result is a separate question, §6.3(1)).
* Loading uninitialized (but validly owned) memory — e.g., a fresh `alloca` slot — yields an **unspecified but frozen** value: some value of the type, stable across repeated loads until overwritten. It is not poison; it does not propagate UB.

### 6.3 Undefined behavior (exhaustive list)

UB in Vertex IR is confined to the following. A program that executes UB has no defined behavior from that point (and codegen may assume it doesn't happen):

1. Any access (load, store, atomic, bulk-memory, volatile) outside the bounds of a live object — globals, live `alloca` slots, and externally provided memory the environment defines as valid.
2. Use of an `alloca` address after the owning function invocation returns (escaped stack pointers). Each execution of an `alloca` produces a distinct slot (§4); all of an invocation's slots die together at its return.
3. An access whose address violates its stated (or default natural) alignment; atomics additionally require natural alignment always.
4. Overlapping `memcopy` operands.
5. A **data race** as defined in §4 Atomics.
6. Executing `unreachable`.
7. A `noreturn` function returning; a `readonly` function writing (§4, Calls).
8. Calling a function (directly, indirectly, or via the C boundary) with a signature that mismatches its definition.
9. Modifying memory a `byval` copy or `sret` destination is required not to alias (§7).

Nothing else is UB. In particular there is no UB from arithmetic, conversions, comparisons, or control flow shape.

### 6.4 Provenance stance

Pointers are addresses, full stop. Alias analysis may rely only on object bounds and reachability facts derivable from §6.3(1)–(2), never on how a pointer was *constructed*. This deliberately trades some optimization power for a memory model that is explainable in one paragraph.

---

## 7. ABI & Data Layout

### 7.1 Struct & array layout

`struct` layout is the target C ABI's layout: fields at increasing offsets in declaration order, each field aligned to its natural alignment, trailing padding to make the struct's size a multiple of its largest field alignment. `array[T, N]` is N contiguous elements with no inter-element padding beyond T's own size. `field.ptr` and `index.ptr` compute offsets from exactly these rules. There is no packed or reordered layout in v1.0. Endianness of multi-byte scalars in memory follows the target (§10.1); `bitcast` reinterprets register bits, not memory bytes. Note that `zero` initialization of an aggregate (§8) guarantees that all implicit alignment padding bytes are securely zeroed.

### 7.2 Calls and aggregates

Aggregates never appear as IR values, so by-value struct passing at the C boundary is expressed through two parameter attributes:

* **`byval[S]`** on a `ptr` parameter: the argument is a pointer to a `struct S` (or array) that is passed **by value** per the target C ABI — codegen materializes the copy into registers and/or stack as the ABI dictates; the callee receives its own copy and writes to it do not affect the caller's object. The caller's pointed-to object must be live and must not be mutated by anyone else during the call (§6.3(9)).
* **`sret[S]`** on the **first** `ptr` parameter: the function returns a `struct S` by value per the C ABI; the pointer names the destination. A function with an `sret` param must have return type `void`. The destination must not alias any argument (§6.3(9)).

Scalar (`iN`, `fN`, `ptr`, legal `vec`) parameters and returns are passed directly per the target C ABI. Internal (non-`export`, non-`extern`) functions use the same ABI in v1.0 — one calling convention everywhere; a private fast-cc is future work.

### 7.3 Symbols

`export`ed functions and globals get their IR name as an unmangled C symbol (plus any target-mandated decoration, e.g. legacy underscore prefixing on `macho`). Functions declared in `extern` groups bind to C symbols of their IR name; the group determines **which dependency provides them** (§7.4). Functions in an anonymous group (`extern :`) resolve against the target's default namespace — on hosted OSes, the process's already-loaded images (libc and friends). Internal definitions have no symbol obligations.

### 7.4 Link dependencies

The link section makes the module its own complete linker input: every library the binary needs is declared by exactly one `link` line, and every imported symbol is attributed to its provider by the `extern` group that lists it. There are no external linker flags.

**Kinds.** The kind names the portable semantic, never a platform's file extension:

| Kind | Meaning | Lowering |
| --- | --- | --- |
| `shared` | Library loaded at runtime by the system loader | `DT_NEEDED` (ELF), `LC_LOAD_DYLIB` (Mach-O), import descriptor (PE) |
| `static` | Archive consumed at build time | Symbols resolved at link time; nothing emitted into the binary |
| `framework` | Apple framework bundle | `LC_LOAD_DYLIB` with the framework loader path; **Mach-O targets only** |

`framework` is deliberately platform-named because no platform-neutral concept underlies it; it is rejected at build time on non-Mach-O targets (same gating model as `tls` on `os = none`).

**Short and exact names.** One rule decides which form a string is:

> If the string contains a `.` or a path separator, it is an **exact name**. Otherwise it is a **short name**.

An exact name is emitted/consumed byte-for-byte, and its extension must agree with the kind: `shared` requires the target format's shared-library extension (`.so` optionally followed by version components on ELF, `.dylib` on Mach-O, `.dll` on PE); `static` requires an archive extension (`.a`, `.lib`). A short name derives its filename from this fixed table — one template per cell, no search:

| Kind | ELF | Mach-O | PE |
| --- | --- | --- | --- |
| `shared "X"` | `libX.so` | `libX.dylib` | `X.dll` |
| `static "X"` | `libX.a` | `libX.a` | `X.lib` |
| `framework "X"` | — | `X.framework/X` | — |

`framework` strings must **always** be short names (bare, no dot, no path); the loader path derivation is Apple's, not `vvm`'s.

*Informative:* on ELF, the short form derives the unversioned `libX.so`, which typically exists only via a dev-package symlink. Modules intended for deployment should use the exact soname long-form (e.g. `"libSDL2-2.0.so.0"`), which is emitted verbatim as the `DT_NEEDED` entry.

**Duplicates.** Duplicate dependencies are rejected **after derivation**: `link shared "SDL2"` and `link shared "libSDL2.so"` on an ELF target name the same file and may not coexist.

**Attribution.** An `extern "X" :` group binds its symbols to the dependency whose `link` string is byte-for-byte `"X"` as written (short matches short, exact matches exact). A `link` line need not have a group (link-only dependencies, e.g. frameworks reached indirectly through `objc_msgSend`); a group must have a `link` line, except the anonymous group `extern :`, which imports from the default namespace and carries no dependency.

**Target-dependence.** Pure-compute modules (no link section) remain fully target-independent. Modules with a link section are per-target by construction — a module linking `"libSDL2-2.0.so.0"` is an ELF module — and must carry a `target-decl` (§1.2 rule 10) naming that same triple explicitly. Frontends emitting per-target `.vir` files is the supported model; the IR does not paper over platform differences that the symbol level reasserts anyway.

**Examples:**

```vir
// short form — everyday case
link shared "SDL2"                  // libSDL2.so / libSDL2.dylib / SDL2.dll
link static "z"                     // libz.a / z.lib
link framework "AppKit"

// long form — when the real filename matters
link shared "libSDL2-2.0.so.0"      // deploy-safe ELF soname
link shared "libobjc.A.dylib"
link shared "user32.dll"
link static "vendor/libfoo.a"       // path separator ⇒ exact

// groups
extern "SDL2" :
    fn SDL_Init(flags i32) i32
end

extern :                            // default namespace (e.g. libc on hosted OSes)
    fn malloc(size i64) ptr
    fn free(p ptr) void
end

// rejected
link shared "libz.a"                // kind/extension mismatch
link framework "AppKit.framework"   // frameworks are always bare names
link framework "Gtk"                // framework on a non-Mach-O target
link shared "SDL2"
link shared "libSDL2.so"            // duplicate after derivation (ELF)

```

*Future work (deliberately deferred):* `extern global` for data imports (`stderr`, `NSApp`, GTK exported variables); weak linking (`link weak framework "..."` for availability-gated Apple APIs).

---

## 8. Constants & Initializers

`const` declarations are **scalars only** (`iN`, `fN`, `ptr`, or a vector literal of legal width): one literal, usable anywhere a literal of that type is, yielding the value.

`global` initializers accept the full `const-init` grammar:

* **Scalar literal** — for scalar-typed globals. `null` for `ptr`.
* **`zero`** — legal for any type including aggregates; the object is all-zero bytes (which is a valid representation for every Vertex IR type: zero integers, +0.0 floats, null pointers). For structs and arrays, this strictly guarantees that all implicit padding bytes are also zeroed.
* **`addr ident`** — legal only for `ptr`-typed positions; `ident` must be a **previously declared** `global`, `fn`, or `extern fn`. Produces a relocated address at link time. `addr` of a `tls` global is rejected (§1.2 rule 7). This, together with function names as runtime operands (§4), closes the function-pointer bootstrap gap.
* **Aggregate initializer** `( e0, e1, ... )` — for `struct S`: exactly one element per field, in declaration order, each recursively a `const-init` of the field's type. For `array[T, N]`: either exactly N elements, or fewer than N with the remainder implicitly `zero`.
* **Byte string** `"..."` — shorthand initializer for `array[i8, N]`. The string's byte length (after escape processing) must be **exactly N**; there is no implicit NUL — write `"\0"` yourself. Escapes: `\0 \n \r \t \\ \" \xNN`.

A `global` may carry `align N` (N a power of two, ≥ the type's natural alignment) to over-align its storage — e.g. cache-line or page alignment. Alignment affects layout only, never the initializer.

Examples:

```text
struct Vec2(x f32, y f32)
global origin struct Vec2 = (0.0, 0.0)
global lut array[i32, 256] align 64 = zero
global banner array[i8, 6] = "hi!\n\0\0"
global on_tick ptr = addr default_tick_handler   // default_tick_handler declared above

```

Note: `addr` of an `extern fn` requires the function's `extern` group (or anonymous group) to appear before the `global` — which the fixed section order forbids, since links and externs follow globals. `addr` of extern functions is therefore unreachable in v1.2 and reserved; use a runtime store of the function name (which yields its `ptr`, §4) instead. Relaxing this is future work alongside offset constants.

`global` initializers still may not reference `const`s by name, do arithmetic, or take offsets into objects (`addr` yields object bases only). Offset constants are future work.

---

## 9. Verifier Obligations

**Module shape**

1. **Section Order:** header, target, structs, fnsigs, consts, globals, links, externs, fns (§1.1). Interleaved sections or a misplaced/duplicate `module` header are rejected.
2. **Declare-Before-Use:** every reference resolves to an earlier line; only direct self-recursion inside an `fn` body is exempt (§1.2).
3. **Naming:** strict single flat namespace; zero shadowing; keywords are not identifiers. Extern-group functions live in the flat namespace like every other name.
4. **Linkage:** `export` only on `fn`/`global`; `tls` only on `global`; `tls` rejected on targets without a TLS convention.
5. **Initializers:** `const` is one scalar literal; `global` initializers match §8 exactly (arity, types, byte-string length, `addr` legality, no `addr` of `tls`); `global align N` is a power of two ≥ natural alignment.
6. **Variadics:** `...` only in `extern fn`/`fnsig`, once, final; variadic arguments cannot be `f32` or narrower than `i32`.

**Target section**
7. **Target declaration:** at most one `target` line, positioned immediately after `module` and before any other section; `arch`/`os`/`abi` must be canonical spellings (§10.1–§10.3, no aliases); tier-list entries must be tiers the named `(arch, os, abi)` actually supports (§10.4). A `target-decl` is **required** if the module contains any `link-decl`, and its `(arch, os, abi)` must be consistent with every `link`/`extern` construct that is target-gated (e.g. `framework` requires Mach-O, `tls` on `os = none` requires a tier that supplies TLS).

**Link section**
8. **Link names:** strings containing `.` or a path separator are exact names whose extension must agree with the kind per the target format; strings with neither are short names derived by the §7.4 table. `framework` strings must be short names; `framework` is legal only on Mach-O targets. Duplicate dependencies after derivation are rejected.
9. **Extern groups:** a named group's string byte-for-byte matches a previously declared `link` string as written; at most one group per `link` string; anonymous groups rejected on `none`/`uefi` targets; empty groups rejected; groups contain only `extern-fn` lines.

**Body shape**
10. **Block Validity:** every block (incl. entry) ends in exactly one terminator; no code after it; empty blocks rejected; `end` follows a terminator; entry is unlabeled and untargetable.
11. **Label Validity:** every label is targeted by at least one branch in its own function; unreferenced labels are rejected. *(Note for pass authors: deleting a branch obligates deleting the orphaned block in the same transformation.)*
12. **Result names** present exactly when the instruction produces a value.
13. **`loc` lines** are syntactically checked and otherwise inert.

**Types & joins**
14. **Types:** a value's type is fixed at its first assignment; every later assignment must match (§5).
15. **Joins:** every read is definitely assigned on all paths from entry (§5, must-analysis).
16. **Suffix rules:** suffixes match operand/destination requirements; aggregates never appear as value types; indirect `call`/`tailcall` suffixes name a declared `fnsig` and arguments match it.
17. **Integer Min/Max:** bare `min.iN`/`max.iN` rejected; only `smin`/`smax`/`umin`/`umax` on integers.
18. **Saturating/Widening/Overflow ops:** `*_sat`, `umulh`/`smulh`, `*addo`/`*subo`/`*mulo` legal only on `iN`/`vec[iN, W]`.
19. **Conversion Traps & Signedness:** `stoint`/`utoint` are verified as trapping; only `stoint_sat`/`utoint_sat` may saturate; no signedness-ambiguous conversion opcodes exist. `bitcast` between `ptr` and integer requires exactly `usize` width.
20. **`bswap`** rejected on `i8`.

**Control flow**
21. **`br_if`** condition operand is `i1`.
22. **`switch`** operand is `iN`; case literals are unique and representable in the operand's type; first label is the default.
23. **`trap`/`unreachable**` are terminators; nothing may follow them in a block.

**Memory, atomics, calls, vectors**
24. **Address producers:** `field.ptr`'s struct operand names a declared struct and its field operand names one of that struct's fields; `index.ptr`'s type operand is a sized type and its index operand is a `usize`-width integer.
25. **Alignment clauses:** `align N` is a power of two; atomics carry no alignment clause (always natural).
26. **Atomic orderings:** loads exclude `release`/`acqrel`; stores exclude `acquire`/`acqrel`; `cmpxchg` failure ordering constraints per §4.
27. **Bulk memory:** `len` operands are `usize`-width; `memset` value is `i8`.
28. **ABI attributes:** `byval[S]`/`sret[S]` only on `ptr` params; `sret` only first param, only with `void` return; the named struct must be declared.
29. **`tailcall`:** return type equals caller's; no `byval`/`sret` params on the callee signature.
30. **`noreturn`:** a direct call to a `noreturn` callee must be followed (after any `loc`/comment lines) by `unreachable` or be itself the last instruction before a terminator that is `unreachable` or `trap`.
31. **`shuffle`:** mask is a vector literal; every index is in `[0, 2M)` for source width M; source and result widths are tier-legal.
32. **Target Limits:** vectors must fit the selected tier; masked/gather/scatter opcodes require a masking-capable tier (§10.4).

---

## 10. Targets & Profiles

Targets are configured via build inputs, or, when the module declares one, via the in-file `target-decl` (§1.2 rule 10, §10.6) — the two must agree. A target is a flat triple:

```text
Target = (arch, os, abi)

```

with hardware vector legality controlled by a separate, orthogonal **feature tier** (§10.4). There is no vendor field: per the CPU-Only design goal, a vendor identifies who *ships* a target, not what the CPU *is*, and carries no information the verifier or codegen needs.

### 10.1 `arch` — real silicon only

An architecture name may only be added to this list if it names an instruction set decoded natively by physical hardware. Bytecode formats, VM targets, and other IRs (e.g. wasm) are explicitly excluded — Vertex IR is CPU-only, and admitting a target that is itself an intermediate representation reintroduces the IR-to-IR indirection the design goals rule out.

Grammar: `<name><bits><endian>`, underscore-separated, where `bits`/`endian` are present only when the architecture actually varies along that axis. The arch fixes pointer width (`usize`) and memory endianness.

| Canonical | Silicon | Rejected aliases |
| --- | --- | --- |
| `x86`, `x86_64` | Intel / AMD | `i386`, `i686`, `amd64`, `x64` |
| `arm`, `armeb` | 32-bit ARM cores | `arm32` |
| `aarch64`, `aarch64_be` | 64-bit ARM cores (`le` is default, omitted) | `arm64`, `arm64e`, `arm64ec` |
| `riscv32`, `riscv64` | RISC-V cores | `rv32`, `rv64` |
| `powerpc`, `powerpc64`, `powerpc64le` | POWER / PowerPC | `ppc`, `ppc64` |
| `mips32`, `mips32el`, `mips64`, `mips64el` | MIPS cores | `mips`, `mipsel` (width required, never bare) |
| `loongarch64` | LoongArch cores |  |
| `s390x` | IBM Z | `systemz` |

Vendor-specific hardware variants (e.g. Apple's pointer-authentication extension, Microsoft's x86_64-shaped emulation ABI on AArch64) are not separate architectures. They are expressed as an `abi` token (§10.3) or a feature-tier flag (§10.4) layered on top of the base architecture — never as a distinct entry in this table.

### 10.2 `os`

| Canonical | Rejected aliases |
| --- | --- |
| `linux` |  |
| `macos`, `ios`, `watchos`, `tvos`, `visionos` | `darwin` (kernel/ABI lineage name, not a target-facing OS) |
| `windows` | `win32`, `nt` |
| `android` |  |
| `freebsd`, `netbsd`, `openbsd` | `bsd` |
| `uefi` |  |
| `none` | `freestanding`, `bare`, `baremetal` (bare metal, one canonical spelling) |

### 10.3 `abi`

| Canonical | Meaning |
| --- | --- |
| `gnu` | glibc-based |
| `musl` | static-friendly libc |
| `msvc` | Windows/MSVC calling convention + runtime |
| `eabi`, `eabihf` | ARM embedded, soft/hard float |
| `aapcs64` | AArch64 procedure-call standard variant where variadic arguments are pushed to the stack instead of passed in registers (used by `aarch64-macos-aapcs64`) — an ordinary `abi` token, looked up like any other, not a bespoke exemption. |
| `macho` | Apple binary/environment convention, for targets not otherwise covered by an OS-specific ABI above |

### 10.4 Feature Tiers

Feature tiers are orthogonal to `(arch, os, abi)` and gate: hardware vector width legality (e.g. AVX2 vs AVX-512 on `x86_64`); availability of masked/gather/scatter vector opcodes (§4); wide-atomic availability (`i128` cmpxchg); and the TLS register convention on `os = none`. A `vec[T, N]` is only legal if `N` fits the tier selected for the current build. When a module carries a `target-decl` with a `tier-list`, that list is the tier selection for the module; without one, tiers are supplied purely as build flags.

### 10.5 Aliases

Aliases (e.g. `x64 -> x86_64`) are resolved **only** at the build-system boundary, in a single lookup table outside the IR grammar. The IR's own type/target grammar never accepts an alias directly — this applies equally to the in-file `target-decl` (§10.6) as to build flags; the verifier rejects any non-canonical spelling reaching it. Library-name spellings follow the same philosophy: `dylib`, `dll`, `so`, and `dynamic` are rejected aliases for the canonical `shared` kind (§7.4).

### 10.6 In-file target declaration

A module may state its own build target with a `target` line (§1.1, §1.2 rule 10):

```vir
module sdl2_example
target x86_64 linux gnu

// ...

```

or, with feature tiers:

```vir
module simd_kernel
target x86_64 linux gnu [avx2]

// ...

```

This is **required** whenever the module has a `link` section (§7.4), since such modules are already per-target by construction — the `target-decl` just makes that fact explicit and checkable rather than leaving it implicit in which `link` strings resolve on which platform. It is **optional and typically absent** for pure-compute modules, which remain buildable for any triple via build flags alone (§10). When present, a build invocation specifying a conflicting target is a build-time error: the file states what it was written for, the invocation states what it's being built for, and the two must match before codegen proceeds. This is a compile-time consistency check, not a relaxation of §10's principle that target selection is fundamentally a build concern — the in-file line is a declaration the build must honor, not new target-conditional grammar in the compute sections themselves.

---

## 11. Debug Locations

`loc "file" line col?` lines attach source positions to subsequent instructions (until the next `loc` or `end`). They are the entire debug-info story in v1.0: no variable metadata, no type metadata, no expression language. Codegen lowers them to line tables (DWARF `.debug_line` / CodeView equivalents). Optimization passes may drop or merge `loc` lines freely; they must never invent one. Richer debug info is explicitly deferred rather than half-specified.