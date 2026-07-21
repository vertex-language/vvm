# vvmtest

An in-memory, no-`.vir`-files, no-`go test` correctness suite for `ir/vir`
and the rest of the `vvm` pipeline. Every case builds a `*vir.Module`
directly via the `vir.FunctionBuilder` API (ir/vir/builder.go), runs it
through `vvm.RunModule`, and checks **one thing**: either a single printed
integer, or an exit code. Nothing else about stdout is checked — no
string/format matching, no golden files.

```sh
go run ./vvmtest
```

## Layout

```
vvmtest/
├── main.go       — testCase type, registry, host autodetect, run()
├── helpers.go    — printerModule / i32PrintingModule / i64PrintingModule
├── i8.go         — i8 literal + wraparound
├── i16.go        — i16 literal + wraparound
├── i32.go        — i32 literal, add, sub, mul, overflow wrap
├── i64.go        — i64 literal, add, sub, mul (values outside i32 range)
├── exit.go       — plain exit-code cases, no libc involved
└── asm_exit.go   — inline asm raw-syscall exit
```

## One fact per test

Each `testCase` checks exactly one opcode/behavior. Nothing here loops or
branches inside a `build` func — if a case needs a `for`/`BranchIf`/
`Switch` to express what it's testing, it belongs in a future, separately
named file for control flow specifically, not folded into an arithmetic
or literal test.

## `wantValue` vs `wantExit`

- Set `wantValue` for a case that prints one integer via
  `i32PrintingModule`/`i64PrintingModule` and returns exit 0.
- Set only `wantExit` for a case where the exit code itself is the thing
  under test (see `exit.go`, `asm_exit.go`) — no libc, no printf.

## Why every extern group names `"c"` explicitly

`ir.md` §1.2 rule 9 / §9.9 and `verify.go` are explicit: **there is no
anonymous/default-namespace extern group.** `DeclareExternGroup("")` fails
`Verify` outright ("extern group has no dependency string"). Every module
here that calls `printf` therefore declares `link shared "c"` and a
matching `extern "c" : ... end` group — same pattern as a real `.vir` file
would use, and the only thing `resolveEntryPoint`'s libc-aware `_start`
synthesis (see `entrythunk.go`) actually looks for when deciding whether
to call libc's `exit()` (flushing stdio) instead of a raw `SYS_exit`.

## i64 printing uses `%lld`, not `%d`

`%d` reads a variadic argument as a 32-bit `int` — for a value outside
`i32` range this silently truncates instead of erroring. `i64PrintingModule`
uses `"%lld"` for exactly this reason; if you add a new i64 case, use that
helper rather than `i32PrintingModule`, or you'll get a confusing
wrong-value failure instead of a clear type mismatch.

## Host gating

Every case here currently sets `hostArches: []string{"x86_64"}`,
`hostOSes: []string{"linux"}`, because that's the only `(arch, os)` pair
with a registered entry thunk right now (`entrythunk.go`). Cases outside
that combination report `SKIP`, not a silent no-op — you'll always see
which ones ran vs. were skipped and why. This restriction goes away case
by case as more entry thunks land; there's no reason to relax it suite-
wide before that's actually true.

## Not yet covered (intentionally, for now)

Control flow (loops, switch), tailcalls/recursion, structs/pointers,
vectors, atomics, and the asm dialects beyond `asm_exit.go`'s single
raw-exit case were all part of the previous version of this suite and
were removed in this rewrite to get back to a known-correct, finer-grained
baseline (the previous versions of several of these files also relied on
the anonymous-extern-group pattern described above, which doesn't pass
`Verify`). Re-add them following the same one-fact-per-file, no-loops-in-
a-test shape as this baseline, not by reviving the old files as-is.