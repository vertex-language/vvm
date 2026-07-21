# testutils

An in-memory, no-`.vir`-files, no-`go test` correctness suite for `ir/vir`
and the rest of the `vvm` pipeline. Every case builds a `*vir.Module`
directly via the `vir.FunctionBuilder` API (`ir/vir/builder.go`), runs it
through `vvm.RunModule`, and checks **one thing**: a single printed
integer, a single printed float, or an exit code. Nothing else about
stdout is checked — no string/format matching, no golden files.

```sh
go run .
```

## Layout

Grouped by IR concept, not by type width — `i8`/`i16`/`i32`/`i64` all live
together, control flow constructs live together, and so on.

```
testutils/
├── main.go            — testCase type, registry, host autodetect, run()
├── helpers.go          — printerModule(s), i32/i64/f32/f64 printing helpers
├── floatconsts.go       — shared NaN / negative-zero float64 constants
├── integers.go          — i8/i16/i32/i64 literal, arith, wraparound
├── arithmetic.go        — div/rem, neg/abs, overflow predicates,
│                          widening multiply, saturating add/sub
├── bitwise.go            — and/or/xor/not, shifts + count masking,
│                          rotl/rotr, ctlz/cttz/popcnt
├── comparisons.go        — signed vs. unsigned int comparisons, ptr cmp
├── conversions.go        — trunc/sext/zext, ptr<->usize-int bitcast
├── floats.go             — f32/f64 arith, min/max (NaN, signed zero),
│                          float comparisons, fpromote/fdemote,
│                          int<->float conversions
├── control_flow.go       — br, br_if, switch, select, loop-carried values
├── memory.go             — alloca, index.ptr, field.ptr, memcopy/memset
├── globals_consts.go     — const-as-operand, global store/load roundtrip
├── calls.go              — recursion, tailcall (direct + indirect),
│                          byval/sret
├── process_exit.go       — plain exit-code cases, no libc involved
└── asm_exit.go           — inline asm raw-syscall exit (unchanged)
```

## One fact per test

Each `testCase` checks exactly one opcode/behavior. Nothing loops or
branches inside a `build` func unless the fact under test is control flow
itself (`control_flow.go`) — an arithmetic or conversion test shouldn't
need a `for`/`BranchIf`/`Switch` to express what it's checking.

## `wantValue` vs `wantFloatValue` vs `wantExit`

- Set `wantValue` for a case that prints one integer via
  `i32PrintingModule`/`i64PrintingModule` and returns exit 0.
- Set `wantFloatValue` for a case that prints one float via
  `f32PrintingModule`/`f64PrintingModule`. Comparison is epsilon-based
  except: `math.NaN()` as the want value asserts the result is NaN, and
  `0.0` as the want value additionally checks the sign bit (so
  `min(-0.0, +0.0) == -0.0` is actually distinguished from `+0.0`, not
  just numerically equal to it).
- Set only `wantExit` for a case where the exit code itself is the thing
  under test (`process_exit.go`, `calls.go`'s tailcall cases,
  `asm_exit.go`) — no libc, no printf.

## Why every extern group names `"c"` explicitly

`ir.md` §1.2 rule 9 / §9.9 and `verify.go` are explicit: **there is no
anonymous/default-namespace extern group.** `DeclareExternGroup("")` fails
`Verify` outright. Every module here that calls `printf` therefore
declares `link shared "c"` and a matching `extern "c" : ... end` group —
same pattern a real `.vir` file would use, and the only thing
`resolveEntryPoint`'s libc-aware `_start` synthesis (`entrythunk.go`)
actually looks for when deciding whether to call libc's `exit()`
(flushing stdio) instead of a raw `SYS_exit`.

## i64 printing uses `%lld`, f32 printing promotes to `%f`

`%d` reads a variadic argument as a 32-bit `int` — for an `i64` value this
silently truncates instead of erroring, so `i64PrintingModule` uses
`"%lld"`. Similarly, a variadic `f32` argument is illegal at the C
boundary without manual promotion to `f64` (ir.md §4 "Variadic Calls");
`f32PrintingModule` inserts that `fpromote` itself so individual `f32`
test cases don't each have to remember it.

## Host gating

Every case here currently sets `hostArches: []string{"x86_64"}`,
`hostOSes: []string{"linux"}`, because that's the only `(arch, os)` pair
with a registered entry thunk right now (`entrythunk.go`). Cases outside
that combination report `SKIP`, not a silent no-op. This restriction goes
away case by case as more entry thunks land.

## Not yet covered

- **`trap` / `unreachable` as an expected outcome.** Both are legitimate,
  spec-defined terminators, but this package has no confirmed convention
  for what `vvm.RunModule` reports in `res.ExitCode` when the process
  dies that way. Add these once that convention is pinned down against a
  real run, rather than guessing an exit code.
- **Vectors, atomics, and the asm dialects beyond `asm_exit.go`'s single
  raw-exit case.** Removed in a previous rewrite to get back to a
  known-correct, finer-grained baseline; re-add following the same
  one-fact-per-file shape as this baseline, not by reviving old files
  as-is.
- **Mutual recursion via the global-slot pattern** (ir.md §1.2 rule 3).
- **Bulk memory's `memmove`** specifically (overlap-safe variant) —
  `memory.go` only exercises `memcopy`/`memset` so far, both
  non-overlapping.