# vvm/testutils

A conformance test suite that builds `vir.Module` values in memory, runs
them through `vvm.RunModule`, and checks the observed result (printed
stdout value or process exit code) against an expected value. It exists to
pin down observable behavior for every opcode and structural feature
described in `ir.md`, one fact at a time.

This is a standalone `package main` — a small test runner, not a `go test`
suite — so it can be invoked directly:

```sh
cd testutils
go run .
```

It prints one `PASS`/`FAIL`/`SKIP` line per case and a final summary, and
exits non-zero if anything failed.

## How it works

Every case is a `testCase` (see `main.go`):

```go
type testCase struct {
    name       string
    hostArches []string // vir-canonical arch names this case can run on; nil = any
    hostOSes   []string // vir-canonical os names this case can run on; nil = any
    build      func(arch, osName string) *vir.Module

    wantValue      *int64   // checked against parsed integer stdout when non-nil
    wantFloatValue *float64 // checked against parsed float stdout when non-nil
    wantExit       int
}
```

`register(testCase{...})` appends a case to the global `registry`. Each
file's own `init()` registers its own cases — there's no separate manual
wiring step. `run()` (called from `main()`) walks the registry, skips
cases whose `hostArches`/`hostOSes` don't match the current host, builds
and runs the rest via `vvm.RunModule`, and checks the result:

- If `wantValue` is set, stdout is parsed as a plain base-10 integer and
  compared exactly.
- If `wantFloatValue` is set, stdout is parsed as a float and compared via
  `floatMatches` — exact for `NaN` and signed zero (so IEEE-754 sign-bit
  and NaN-propagation behavior can't silently pass), epsilon-tolerant
  (`1e-6` relative) otherwise, since `printf("%f")` only carries six
  decimal digits.
- Otherwise, only `wantExit` (default `0`) is checked against the
  process's exit code.

All currently-registered cases are gated to `hostArches: ["x86_64"]`,
`hostOSes: ["linux"]`, since that's the only combination with a
registered entry-thunk today (see `entrythunk.go` / `asm_exit.go`,
referenced from `helpers.go`).

## File layout

Cases are grouped by the IR area they exercise, matching `ir.md`'s own
section structure rather than being one flat list:

| File | Covers |
|---|---|
| `main.go` | `testCase`, `register`, the runner, and `floatMatches` |
| `helpers.go` | Shared module-building helpers (see below) |
| `integers.go` | i8/i16/i32/i64 literals, arithmetic, modular wraparound |
| `arithmetic.go` | div/rem, neg/abs, overflow predicates, widening multiply, saturating add/sub |
| `bitwise.go` | and/or/xor/not, shifts (incl. count masking), rotates, ctlz/cttz/popcnt |
| `comparisons.go` | Signed vs. unsigned integer comparisons, pointer comparisons |
| `control_flow.go` | br/br_if/switch/select, loop-carried values |
| `conversions.go` | trunc/sext/zext, ptr↔int bitcast round-trip |
| `floats.go` | f32/f64 literals & arithmetic, min/max IEEE-754 semantics, float comparisons, fpromote/fdemote, int↔float conversions |
| `floatconsts.go` | Shared NaN / negative-zero constants used by `floats.go` |
| `calls.go` | Recursion, direct/indirect tailcalls, byval/sret ABI attributes |
| `globals_consts.go` | `const` and `global` declarations |
| `memory.go` | alloca, index.ptr, field.ptr, memcopy/memset |
| `process_exit.go` | Plain exit-code cases with no libc linked |

### `helpers.go`

Most cases don't hand-build a full module. Instead they use one of:

- `i32PrintingModule` / `i64PrintingModule` / `f64PrintingModule` /
  `f32PrintingModule` — each builds the smallest module capable of
  computing one value via a `build` func and printing it with the
  matching `printf` format specifier, declaring the `libc` link and
  `extern "c"` group required to call it.
- `identity(fb, name, t, v)` — materializes a literal (or any operand) as
  a named value, using `add x, 0` since the opcode vocabulary has no bare
  identity/mov op.
- `abiFor(osName)` — picks the canonical ABI matching a target OS.

Cases that need more than one function (`calls.go`), a real struct
(`memory.go`, `calls.go`), or a `const`/`global` declaration
(`globals_consts.go`) build their `*vir.Module` by hand instead, since
`printerModule` only ever declares a single function (`main`).

## Conventions for adding a case

- **One fact per case.** A case should check exactly one printed value or
  exit code — never a combination of several opcodes' worth of behavior.
  If a case's `build` func needs a loop or a branch to express what it's
  testing, it likely belongs in `control_flow.go` instead of wherever you
  were about to put it.
- **Put it in the file matching its `ir.md` section**, not the file that
  happens to have a convenient helper already.
- **Prefer the smallest helper that fits** (`i32PrintingModule`, etc.)
  unless the case genuinely needs multiple functions, a struct, or
  global/const state.
- **Use `identity`** rather than reaching for a raw literal operand when
  you need a literal to have a name (e.g. so it can be referenced from a
  later label/branch).
- **Comment the "why," not the "what."** Existing files lean on short
  header comments explaining what's deliberately in/out of scope for that
  file, and inline comments only where the expected value isn't obvious
  from the literals alone (e.g. bit patterns, wraparound arithmetic).

## Known gaps (intentionally not covered yet)

- **Trapping inputs** (div-by-zero, `INT_MIN / -1`, out-of-range
  `stoint`) are deliberately excluded from `arithmetic.go` and
  `floats.go` — there's no confirmed convention yet for what
  `vvm.RunModule` reports for a trapped process.
- **`trap` / `unreachable` as an expected outcome** are deliberately
  excluded from `process_exit.go` for the same reason: signal-based exit
  codes are a convention, not something derivable from this package
  alone.

Both should get dedicated cases once `vvm.RunModule`'s trap-reporting
behavior is pinned down — don't guess an exit code in the meantime.