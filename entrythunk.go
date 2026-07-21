// entrythunk.go
package vvm

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// EntryThunkFunc synthesizes a raw process-entry function (conventionally
// named "_start") into m: it performs whatever ABI-specific stack setup the
// target OS demands at process start, calls userMain using the calling
// convention implied by sig, and terminates the process with userMain's
// i32 return value. It returns the name of the function the linker should
// actually treat as the entry symbol (almost always "_start").
//
// Registered per (arch, os) rather than per (arch, os, abi): a thunk built
// on raw syscalls (no libc call) doesn't care whether the target is gnu or
// musl — either way there's no libc entry point involved, only the OS's
// own process-start convention and syscall table.
type EntryThunkFunc func(m *vir.Module, userMain string, sig vir.MainSignature, t Target) (entrySymbol string, err error)

type entryThunkKey struct{ arch, os string }

var entryThunkRegistry = map[entryThunkKey]EntryThunkFunc{}

// RegisterEntryThunk registers f as the libc-style entry synthesizer for
// (arch, os). Called from init() by whichever file owns that combination,
// same additive-registration shape as elf's RegisterPatcher etc.
func RegisterEntryThunk(arch, os string, f EntryThunkFunc) {
	entryThunkRegistry[entryThunkKey{arch, os}] = f
}

func lookupEntryThunk(t Target) (EntryThunkFunc, bool) {
	f, ok := entryThunkRegistry[entryThunkKey{t.baseArch(), t.OS}]
	return f, ok
}

// resolveEntryPoint decides what symbol name the linker should be told is
// the process entry point, and — if conditions are met — mutates m to add
// the synthesized libc-style wrapper. It runs after the initial Verify()
// in BuildModule but before lowering; callers must re-Verify afterward
// since it can add a new function.
//
// The gate, in order:
//  1. No `entry`-attributed fn at all → nothing to do; preserve the
//     pre-existing contract that the module defines "_start" itself.
//  2. The entry fn is itself literally named "_start" → the author opted
//     into the raw contract explicitly; never second-guess that.
//  3. Output kind isn't an executable (e.g. a shared library) → no
//     process image, no _start convention; `entry` is only a documentation
//     marker here, wire the fn's own name (never wrapped).
//  4. os is "none" or "uefi" → no libc, no argc/argv/exit(3) convention to
//     synthesize against (kernels, bootloaders, drivers); wire the fn's
//     own name as the raw entry point, unwrapped.
//  5. The fn's signature doesn't match a recognized `main()` shape →
//     assume the author is hand-managing the raw ABI themselves.
//  6. No registered thunk for (arch, os) → fail loudly rather than
//     silently doing nothing or guessing.
//
// Only when all of 1–6 clear does synthesis actually happen.
func resolveEntryPoint(m *vir.Module, t Target) (string, error) {
	ef := m.EntryFunction()
	if ef == nil {
		return "_start", nil
	}
	if ef.Name == "_start" {
		return "_start", nil
	}
	if t.Kind != OutputExecutable {
		return ef.Name, nil
	}
	if !t.isHostedProcessOS() {
		return ef.Name, nil
	}
	sig := vir.RecognizedMainSignature(ef)
	if sig == vir.MainSignatureNone {
		return ef.Name, nil
	}
	thunk, ok := lookupEntryThunk(t)
	if !ok {
		return "", fmt.Errorf(
			"vvm: %s: no entry-thunk registered for automatic main() wiring — "+
				"name your entry fn \"_start\" and write the process-entry "+
				"prologue yourself, or build for a target with one registered", t)
	}
	return thunk(m, ef.Name, sig, t)
}

// ---------------------------------------------------------------------------
// x86_64 / linux entry thunk.
// ---------------------------------------------------------------------------

const sysExitX86_64Linux = 60

func init() {
	RegisterEntryThunk("x86_64", "linux", synthesizeX86_64LinuxStart)
}

// linksLibC reports whether m declares the conventional libc dependency
// (ir.md §7.4's own worked example: `link shared "c"`). This is the same
// signal dispatch.go's addELFLinkDependencies already special-cases when
// resolving "c" to the real runtime soname via AddDefaultNamespace — here
// it answers a different question: not "what file provides libc" but
// "does this module's stdio go through libc's buffered streams at all,"
// which determines how _start must terminate the process (see
// synthesizeX86_64LinuxStart below).
func linksLibC(m *vir.Module) bool {
	for _, l := range m.Links {
		if l.Kind == vir.LinkShared && l.Name == "c" {
			return true
		}
	}
	return false
}

// libcExit returns the name of an extern fn matching the standard C
// `exit(int) noreturn` shape, declaring one on the module's "c" extern
// group if none already exists.
//
// It reuses an existing "exit" declaration rather than blindly appending
// a new one: a module that already calls exit() itself will have already
// declared it, and appending a second same-named extern function would
// collide in the flat namespace (ir.md §1.2 rule 3) and fail the re-Verify
// BuildModule performs immediately after resolveEntryPoint runs. If a name
// "exit" exists but doesn't match the shape we need, that's a genuine
// conflict — fail loudly rather than silently calling something else's
// "exit" with the wrong signature.
func libcExit(m *vir.Module) (string, error) {
	for _, g := range m.Externs {
		for _, f := range g.Functions {
			if f.Name != "exit" {
				continue
			}
			if len(f.Params) == 1 && vir.Equal(f.Params[0].Type, vir.I32) && vir.IsVoid(f.Ret) {
				return f.Name, nil
			}
			return "", fmt.Errorf(
				"vvm: module already declares extern %q with a signature incompatible "+
					"with the standard C exit(int) — needed here to flush libc's stdio "+
					"buffers before the process terminates", f.Name)
		}
	}
	g := externGroupForC(m)
	g.Functions = append(g.Functions, &vir.ExternFunction{
		Name:   "exit",
		Params: []vir.Param{{Name: "code", Type: vir.I32}},
		Ret:    vir.Void,
		Attrs:  []vir.FunctionAttribute{vir.AttributeNoReturn},
	})
	return "exit", nil
}

// externGroupForC returns the module's existing extern group for the "c"
// dependency, or declares a new empty one. A `link shared "c"` line with
// no matching extern group is legal on its own (ir.md §1.2 rule 8,
// "link-only dependencies"), so linksLibC having returned true doesn't
// guarantee a group already exists.
func externGroupForC(m *vir.Module) *vir.ExternGroup {
	for _, g := range m.Externs {
		if g.Dependency == "c" {
			return g
		}
	}
	return m.DeclareExternGroup("c")
}

// synthesizeX86_64LinuxStart builds:
//
//	fn _start() void export:
//	    asm:                      // capture the raw initial %rsp — the
//	      out rax = sp0           // only way to read it; no ordinary IR
//	    code:                     // value carries it (§4 is exactly this
//	      mov rax, rsp            // "asm block as escape hatch" case)
//	    end
//	    sp0_ptr  = bitcast.ptr sp0
//	    argc64   = load.i64 sp0_ptr
//	    argc32   = trunc.i32 argc64
//	    argv_ptr = index.ptr sp0_ptr, i64, 1        // sp0 + 8 = &argv[0]
//	    [argc1   = add.i64 argc64, 1]
//	    [envp_ptr = index.ptr argv_ptr, i64, argc1]  // only if sig wants envp
//	    ret      = call userMain, <args per sig>
//
//	    // Termination is NOT uniformly a raw syscall. A module that links
//	    // libc (link shared "c") may have routed output through libc's
//	    // buffered stdio (printf and friends); those buffers only get
//	    // flushed by libc's own atexit machinery, which runs inside
//	    // libc's exit(), never inside a bare SYS_exit syscall. Skipping
//	    // straight to SYS_exit after such a module's main() silently
//	    // drops any buffered-but-unflushed output — the process exits
//	    // cleanly, with status 0, and nothing was ever written. So:
//	    //
//	    //   if m links libc:
//	    //     call exit, ret     // flushes stdio, then terminates
//	    //     unreachable        // required after a noreturn call, §9.30
//	    //   else:
//	    //     exitcode = syscall.i32 60, ret   // SYS_exit
//	    //     trap                              // defined halt if it
//	    //                                        // somehow returned
//
// Assumes Intel asmdialect (set on the module if not already declared);
// AT&T-dialect modules would need the operands re-spelled, not attempted
// here.
func synthesizeX86_64LinuxStart(m *vir.Module, userMain string, sig vir.MainSignature, t Target) (string, error) {
	if m.AsmDialect == nil {
		m.SetAsmDialect(vir.DialectIntel)
	}

	fb := m.DeclareFunction("_start", nil, vir.Void, true)

	ab := fb.BeginAsm()
	ab.Out("rax", "sp0")
	ab.Code(vir.AsmInstructionLine("mov", vir.AsmRegister("rax"), vir.AsmRegister("rsp")))
	ab.End()

	sp0Ptr := fb.Emit("sp0_ptr", "bitcast", vir.Ptr, vir.Ident("sp0"))
	argc64 := fb.Load("argc64", vir.I64, sp0Ptr)
	argc32 := fb.Emit("argc32", "trunc", vir.I32, argc64)
	argvPtr := fb.IndexPointer("argv_ptr", sp0Ptr, vir.I64, vir.IntLiteral(1))

	var callArgs []vir.Operand
	switch sig {
	case vir.MainSignatureBare:
		// no args
	case vir.MainSignatureArgcArgv:
		callArgs = []vir.Operand{argc32, argvPtr}
	case vir.MainSignatureArgcArgvEnvp:
		argc1 := fb.Add("argc1", vir.I64, argc64, vir.IntLiteral(1))
		envpPtr := fb.IndexPointer("envp_ptr", argvPtr, vir.I64, argc1)
		callArgs = []vir.Operand{argc32, argvPtr, envpPtr}
	default:
		return "", fmt.Errorf("vvm: %s: unreachable main signature", t)
	}

	ret := fb.Call("ret", userMain, callArgs...)

	if linksLibC(m) {
		exitName, err := libcExit(m)
		if err != nil {
			return "", err
		}
		fb.Call("", exitName, ret) // void result: exit() never returns a value
		fb.Unreachable()           // required immediately after a noreturn call, §9.30
	} else {
		fb.Syscall("exitcode", vir.I32, vir.IntLiteral(sysExitX86_64Linux), ret)
		fb.Trap()
	}

	return "_start", nil
}