// atomics.go
package main

import "github.com/vertex-language/vvm/ir/vir"

// atomic_load/atomic_store/atomic_add|sub|and|or|xor|xchg/cmpxchg/fence
// (ir.md §5.1 "Atomics"). No concurrency is needed to observe correct
// single-threaded behavior — every rmw op still has to leave the right
// value in memory even with one thread touching it, so these cases just
// check memory contents after the op, the same shape as memory.go's
// store/load-roundtrip cases.
//
// Deliberately not tested here: what each rmw op *returns* (the old value
// vs. the new value). ir.md doesn't spell out that convention anywhere in
// §5.1, and guessing one would risk pinning down behavior nobody has
// confirmed yet — same reasoning process_exit.go/arithmetic.go already
// apply to trapping inputs. Every case below only reads the *memory
// effect* back out via a plain (non-atomic) load, which is unambiguous
// regardless of what the atomic op's own result value means. Ordering is
// fixed to seqcst throughout since these cases are about the op's effect,
// not about the ordering/tier legality matrix (§5.1's exclusion rules
// would need their own dedicated cases).
//
// Every rmw/cmpxchg op is registered in opcode.go/opinfo.go as producing
// a value (ruleSuffix) — the verifier rejects an unbound result name even
// when the caller has no interest in reading it back. So each op below
// binds a throwaway "old" result rather than "", satisfying that rule
// without pretending to assert anything about what it holds.

func init() {
	register(testCase{
		name: "atomic_store_load_roundtrip",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_roundtrip", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Emit("", vir.OpAtomicStore, vir.I32, slot, vir.IntLiteral(456), vir.OrderingOperand("seqcst"))
				return fb.Emit("v", vir.OpAtomicLoad, vir.I32, slot, vir.OrderingOperand("seqcst"))
			})
		},
		wantValue: val(456),
	})

	register(testCase{
		name: "atomic_add_updates_memory",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_add", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(10))
				fb.Emit("old", vir.OpAtomicAdd, vir.I32, slot, vir.IntLiteral(5), vir.OrderingOperand("seqcst"))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(15),
	})

	register(testCase{
		name: "atomic_sub_updates_memory",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_sub", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(10))
				fb.Emit("old", vir.OpAtomicSub, vir.I32, slot, vir.IntLiteral(3), vir.OrderingOperand("seqcst"))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(7),
	})

	register(testCase{
		name: "atomic_and_updates_memory",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_and", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(0b1100))
				fb.Emit("old", vir.OpAtomicAnd, vir.I32, slot, vir.IntLiteral(0b1010), vir.OrderingOperand("seqcst"))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(0b1000),
	})

	register(testCase{
		name: "atomic_or_updates_memory",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_or", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(0b1100))
				fb.Emit("old", vir.OpAtomicOr, vir.I32, slot, vir.IntLiteral(0b0010), vir.OrderingOperand("seqcst"))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(0b1110),
	})

	register(testCase{
		name: "atomic_xor_updates_memory",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_xor", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(0b1100))
				fb.Emit("old", vir.OpAtomicXor, vir.I32, slot, vir.IntLiteral(0b1010), vir.OrderingOperand("seqcst"))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(0b0110),
	})

	register(testCase{
		name: "atomic_xchg_updates_memory",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_xchg", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(10))
				fb.Emit("old", vir.OpAtomicXchg, vir.I32, slot, vir.IntLiteral(99), vir.OrderingOperand("seqcst"))
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(99),
	})

	// cmpxchg success: expected matches what's in memory, so the swap to
	// desired actually happens.
	register(testCase{
		name: "cmpxchg_success_swaps_memory",
		build: func() *vir.Module {
			return i32PrintingModule("cmpxchg_success", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(10))
				fb.EmitInstruction(vir.Instruction{
					Result: "old",
					Op:     vir.OpCmpxchg,
					Suffix: vir.I32,
					Args: []vir.Operand{
						slot, vir.IntLiteral(10), vir.IntLiteral(99),
						vir.OrderingOperand("seqcst"), vir.OrderingOperand("seqcst"),
					},
				})
				return fb.Load("v", vir.I32, slot)
			})
		},
		wantValue: val(99),
	})

	// cmpxchg failure: expected does NOT match what's in memory, so the
	// swap is skipped and the original value survives untouched.
	register(testCase{
		name: "cmpxchg_failure_leaves_memory_unchanged",
		build: func() *vir.Module {
			return i32PrintingModule("cmpxchg_failure", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Store(vir.I32, slot, vir.IntLiteral(10))
				fb.EmitInstruction(vir.Instruction{
					Result: "old",
					Op:     vir.OpCmpxchg,
					Suffix: vir.I32,
					Args: []vir.Operand{
						slot, vir.IntLiteral(999), vir.IntLiteral(99), // wrong "expected"
						vir.OrderingOperand("seqcst"), vir.OrderingOperand("seqcst"),
					},
				})
				return fb.Load("v", vir.I32, slot) // unchanged: still 10
			})
		},
		wantValue: val(10),
	})

	// fence: no observable effect of its own in a single-threaded case, so
	// this only confirms it's legal to emit between two atomic ops without
	// disrupting the sequence around it.
	register(testCase{
		name: "atomic_fence_does_not_disrupt_sequence",
		build: func() *vir.Module {
			return i32PrintingModule("atomic_fence", func(fb *vir.FunctionBuilder) vir.Operand {
				slot := fb.Alloca("slot", vir.IntLiteral(4), 0)
				fb.Emit("", vir.OpAtomicStore, vir.I32, slot, vir.IntLiteral(5), vir.OrderingOperand("seqcst"))
				fb.Emit("", vir.OpFence, nil, vir.OrderingOperand("seqcst"))
				return fb.Emit("v", vir.OpAtomicLoad, vir.I32, slot, vir.OrderingOperand("seqcst"))
			})
		},
		wantValue: val(5),
	})
}