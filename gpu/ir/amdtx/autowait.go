package amdtx

// insertAutoWaitcnt walks the stream and inserts a conservative vmcnt(0)/
// lgkmcnt(0) before the first use of any register written by an outstanding
// asynchronous load. This is intentionally simple (no counter tracking); it
// guarantees correctness at the cost of over-synchronizing.
func insertAutoWaitcnt(cb *CodeBuilder) {
	var out []Inst
	pendingVM := map[regKey]bool{}
	pendingLGKM := map[regKey]bool{}

	flush := func(kind WaitKind) {
		out = append(out, Inst{Op: OpWaitcnt, Wait: []Waitcnt{{Kind: kind, Count: 0}}})
	}

	for _, in := range cb.insts {
		// If this instruction reads a pending register, drain the counter first.
		needVM, needLGKM := false, false
		for _, s := range in.Src {
			if r, ok := s.(Reg); ok {
				if pendingVM[key(r)] {
					needVM = true
				}
				if pendingLGKM[key(r)] {
					needLGKM = true
				}
			}
			if a, ok := s.(Addr); ok {
				if pendingLGKM[key(a.Base)] {
					needLGKM = true
				}
			}
		}
		if needVM {
			flush(WaitVM)
			pendingVM = map[regKey]bool{}
		}
		if needLGKM {
			flush(WaitLGKM)
			pendingLGKM = map[regKey]bool{}
		}

		out = append(out, in)

		switch in.Op {
		case OpGlobalLoadDword, OpFlatLoadDword, OpGlobalAtomicAddU32:
			for _, d := range in.Dst {
				pendingVM[key(d)] = true
			}
		case OpSLoadDword, OpSLoadDwordx2, OpSLoadDwordx4, OpDsReadB32:
			for _, d := range in.Dst {
				pendingLGKM[key(d)] = true
			}
		}
	}
	cb.insts = out
}

type regKey struct {
	class RegClass
	num   int
	spec  special
}

func key(r Reg) regKey { return regKey{r.Class, r.Num, r.spec} }